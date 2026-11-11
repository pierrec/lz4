package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var lz4cBin string

func TestMain(m *testing.M) {
	code := func() int {
		tmpDir, err := os.MkdirTemp("", "lz4c-test-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
			return 1
		}
		defer os.RemoveAll(tmpDir)

		bin := filepath.Join(tmpDir, "lz4c")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", bin, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to build lz4c: %v\n%s\n", err, out)
			return 1
		}
		lz4cBin = bin

		return m.Run()
	}()
	os.Exit(code)
}

func runCLI(t *testing.T, stdin []byte, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(lz4cBin, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stdout.String(), stderr.String()
	}
	t.Fatalf("failed to execute %v: %v", args, err)
	return -1, "", ""
}

func TestCLI_ErrorOutputAndExitStatus(t *testing.T) {
	tmpDir := t.TempDir()
	validFile := filepath.Join(tmpDir, "valid.txt")
	if err := os.WriteFile(validFile, []byte("sample content to compress"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tests := []struct {
		name                 string
		args                 []string
		stdin                []byte
		wantExitCode         int
		wantStderrSubstrings []string
		assertStdoutEmpty    bool
	}{
		{
			name:                 "compress nonexistent file",
			args:                 []string{"compress", filepath.Join(tmpDir, "does-not-exist.txt")},
			wantExitCode:         1,
			wantStderrSubstrings: []string{"does-not-exist.txt"},
			assertStdoutEmpty:    true,
		},
		{
			name:                 "compress invalid block size",
			args:                 []string{"compress", "-size", "3K", validFile},
			wantExitCode:         1,
			wantStderrSubstrings: []string{"lz4: invalid block size: 3072"},
			assertStdoutEmpty:    true,
		},
		{
			name:                 "compress unparseable block size format",
			args:                 []string{"compress", "-size", "invalid", validFile},
			wantExitCode:         1,
			wantStderrSubstrings: []string{"byte quantity must be a positive integer"},
			assertStdoutEmpty:    true,
		},
		{
			name:                 "uncompress corrupted stdin",
			args:                 []string{"uncompress"},
			stdin:                []byte("invalid compressed data payload"),
			wantExitCode:         1,
			wantStderrSubstrings: []string{"lz4: bad magic number"},
			assertStdoutEmpty:    true,
		},
		{
			name:                 "uncompress nonexistent file",
			args:                 []string{"uncompress", filepath.Join(tmpDir, "does-not-exist.lz4")},
			wantExitCode:         1,
			wantStderrSubstrings: []string{"does-not-exist.lz4"},
			assertStdoutEmpty:    true,
		},
		{
			name:                 "unknown subcommand",
			args:                 []string{"nonexistent-subcommand"},
			wantExitCode:         1,
			wantStderrSubstrings: []string{"no command"},
			assertStdoutEmpty:    true,
		},
		{
			name:                 "invalid subcommand flag",
			args:                 []string{"compress", "-invalid-flag-name"},
			wantExitCode:         2,
			wantStderrSubstrings: []string{"flag provided but not defined: -invalid-flag-name"},
			assertStdoutEmpty:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCLI(t, tc.stdin, tc.args...)

			if exitCode != tc.wantExitCode {
				t.Errorf("exit code = %d, want %d (stderr: %q, stdout: %q)", exitCode, tc.wantExitCode, stderr, stdout)
			}

			for _, sub := range tc.wantStderrSubstrings {
				if !strings.Contains(stderr, sub) {
					t.Errorf("stderr %q does not contain expected substring %q", stderr, sub)
				}
			}

			if tc.assertStdoutEmpty && stdout != "" {
				t.Errorf("expected empty stdout, got %q", stdout)
			}
		})
	}
}

func TestCLI_SuccessRoundTrip(t *testing.T) {
	origContent := []byte("The quick brown fox jumps over the lazy dog repeated text 1234567890")

	// Test stdin-to-stdout compress then uncompress
	exitCode, compressedOutput, stderr := runCLI(t, origContent, "compress")
	if exitCode != 0 {
		t.Fatalf("compress failed with exit code %d: %s", exitCode, stderr)
	}
	if stderr != "" {
		t.Errorf("expected empty stderr on successful compress, got: %s", stderr)
	}
	if len(compressedOutput) == 0 {
		t.Fatal("expected non-empty compressed output")
	}

	exitCode, uncompressedOutput, stderr := runCLI(t, []byte(compressedOutput), "uncompress")
	if exitCode != 0 {
		t.Fatalf("uncompress failed with exit code %d: %s", exitCode, stderr)
	}
	if stderr != "" {
		t.Errorf("expected empty stderr on successful uncompress, got: %s", stderr)
	}
	if !bytes.Equal([]byte(uncompressedOutput), origContent) {
		t.Fatalf("uncompressed content mismatch:\ngot  %q\nwant %q", uncompressedOutput, string(origContent))
	}
}

func TestCLI_OverwriteFiles(t *testing.T) {
	for _, command := range []string{"compress", "uncompress"} {
		for _, content := range []string{"", "shorter content"} {
			for _, oldSize := range []int{-1, 11000} {
				t.Run(fmt.Sprintf("%s/%s/%d", command, content, oldSize), func(t *testing.T) {
					code, compressed, stderr := runCLI(t, []byte(content), "compress")
					if code != 0 {
						t.Fatalf("failed to prepare compressed input: %s", stderr)
					}
					input, want := []byte(content), []byte(compressed)
					filename := filepath.Join(t.TempDir(), "data")
					output := filename + lz4Extension
					if command == "uncompress" {
						filename, output = output, filename
						input, want = want, input
					}
					if err := os.WriteFile(filename, input, 0o640); err != nil {
						t.Fatal(err)
					}
					if oldSize < 0 {
						oldSize = len(want)
					}
					if err := os.WriteFile(output, bytes.Repeat([]byte("x"), oldSize), 0o600); err != nil {
						t.Fatal(err)
					}
					code, _, stderr = runCLI(t, nil, command, filename)
					if code != 0 {
						t.Errorf("exit code = %d, want 0: %s", code, stderr)
					}
					got, err := os.ReadFile(output)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, want) {
						t.Errorf("output contents differ: got %d bytes, want %d", len(got), len(want))
					}
					got, err = os.ReadFile(filename)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, input) {
						t.Error("input contents changed")
					}
				})
			}
		}
	}
}

func TestCLI_UncompressErrorTruncatesOutput(t *testing.T) {
	content := []byte("content decoded before the truncated checksum")
	code, compressed, stderr := runCLI(t, content, "compress", "-sc")
	if code != 0 {
		t.Fatalf("failed to prepare compressed input: %s", stderr)
	}
	for _, tc := range []struct {
		name  string
		input []byte
		want  []byte
	}{
		{"invalid header", []byte("invalid compressed data"), nil},
		{"truncated checksum", []byte(compressed[:len(compressed)-1]), content},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "data")
			filename := output + lz4Extension
			if err := os.WriteFile(filename, tc.input, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(output, bytes.Repeat([]byte("x"), 11000), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, stderr := runCLI(t, nil, "uncompress", filename)
			if code != 1 {
				t.Errorf("exit code = %d, want 1: %s", code, stderr)
			}
			got, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("output contents differ: got %d bytes, want %d", len(got), len(tc.want))
			}
			got, err = os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tc.input) {
				t.Error("input contents changed")
			}
		})
	}
}

func TestCLI_OutputDevices(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux device and proc files")
	}
	for _, command := range []string{"compress", "uncompress"} {
		for _, target := range []string{os.DevNull, "/proc/self/comm"} {
			t.Run(command+"/"+target, func(t *testing.T) {
				input := []byte("device output")
				filename := filepath.Join(t.TempDir(), "data")
				output := filename + lz4Extension
				if command == "uncompress" {
					code, compressed, stderr := runCLI(t, input, "compress")
					if code != 0 {
						t.Fatalf("failed to prepare compressed input: %s", stderr)
					}
					input = []byte(compressed)
					filename, output = output, filename
				}
				if err := os.WriteFile(filename, input, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, output); err != nil {
					t.Fatal(err)
				}
				code, _, stderr := runCLI(t, nil, command, filename)
				if code != 0 {
					t.Errorf("exit code = %d, want 0: %s", code, stderr)
				}
				got, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, input) {
					t.Error("input contents changed")
				}
			})
		}
	}
}

func TestCLI_RejectInputAsOutput(t *testing.T) {
	tests := []struct {
		command string
		alias   string
	}{
		{"uncompress", "same path"},
		{"uncompress", "hard link"},
		{"uncompress", "symlink"},
		{"compress", "hard link"},
		{"compress", "symlink"},
	}
	for _, tc := range tests {
		t.Run(tc.command+"/"+tc.alias, func(t *testing.T) {
			input := []byte("content that must not be overwritten")
			filename := filepath.Join(t.TempDir(), "data")
			output := filename + lz4Extension
			if tc.command == "uncompress" {
				code, compressed, stderr := runCLI(t, input, "compress")
				if code != 0 {
					t.Fatalf("failed to prepare compressed input: %s", stderr)
				}
				input = []byte(compressed)
				if tc.alias == "same path" {
					output = filename
				} else {
					filename, output = output, filename
				}
			}
			if err := os.WriteFile(filename, input, 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch tc.alias {
			case "hard link":
				err = os.Link(filename, output)
			case "symlink":
				err = os.Symlink(filename, output)
			}
			if err != nil {
				t.Fatal(err)
			}
			code, _, stderr := runCLI(t, nil, tc.command, filename)
			if code != 1 {
				t.Errorf("exit code = %d, want 1: %s", code, stderr)
			}
			for _, name := range []string{filename, output} {
				got, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, input) {
					t.Errorf("input contents changed through %s", name)
				}
			}
		})
	}
}
