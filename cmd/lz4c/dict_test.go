package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLI_Dictionary checks that -D compresses with a dictionary that
// uncompress needs, from stdin and from files, and that the lz4 command, if
// installed, reads the output with the same dictionary.
func TestCLI_Dictionary(t *testing.T) {
	dir := t.TempDir()
	dict := []byte(`{"service":"checkout","level":"info","msg":"request served","status":200}` + "\n")
	dictFile := filepath.Join(dir, "dict")
	if err := os.WriteFile(dictFile, dict, 0o600); err != nil {
		t.Fatal(err)
	}
	// Small records like the dictionary, each written on its own.
	var b strings.Builder
	for i := range 3 {
		fmt.Fprintf(&b, `{"service":"checkout","level":"info","msg":"request served","status":%d}`+"\n", 200+i)
	}
	content := []byte(b.String())

	code, plain, stderr := runCLI(t, content, "compress")
	if code != 0 {
		t.Fatalf("compress: exit code %d: %s", code, stderr)
	}
	code, withDict, stderr := runCLI(t, content, "compress", "-D", dictFile)
	if code != 0 {
		t.Fatalf("compress -D: exit code %d: %s", code, stderr)
	}
	if len(withDict) >= len(plain) {
		t.Errorf("compressed with the dictionary to %d bytes, without to %d", len(withDict), len(plain))
	}

	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"with the dictionary", []string{"uncompress", "-D", dictFile}, 0},
		{"without the dictionary", []string{"uncompress"}, 1},
		{"missing dictionary file", []string{"uncompress", "-D", filepath.Join(dir, "nope")}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out, stderr := runCLI(t, []byte(withDict), tc.args...)
			if code != tc.wantCode {
				t.Fatalf("exit code %d, want %d: %s", code, tc.wantCode, stderr)
			}
			if code == 0 && out != string(content) {
				t.Fatalf("uncompressed %d bytes, want %d", len(out), len(content))
			}
		})
	}

	t.Run("files", func(t *testing.T) {
		name := filepath.Join(dir, "input.json")
		if err := os.WriteFile(name, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if code, _, stderr := runCLI(t, nil, "compress", "-D", dictFile, name); code != 0 {
			t.Fatalf("compress -D: exit code %d: %s", code, stderr)
		}
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
		if code, _, stderr := runCLI(t, nil, "uncompress", "-D", dictFile, name+lz4Extension); code != 0 {
			t.Fatalf("uncompress -D: exit code %d: %s", code, stderr)
		}
		if got, err := os.ReadFile(name); err != nil || !bytes.Equal(got, content) {
			t.Fatalf("uncompressed file: %d bytes, %v", len(got), err)
		}
	})

	t.Run("lz4 command", func(t *testing.T) {
		lz4cmd, err := exec.LookPath("lz4")
		if err != nil {
			t.Skip("no lz4 binary to test against")
		}
		cmd := exec.Command(lz4cmd, "-d", "-c", "-D", dictFile)
		cmd.Stdin = strings.NewReader(withDict)
		out, err := cmd.Output()
		if err != nil || !bytes.Equal(out, content) {
			t.Fatalf("lz4 -d -D: %d bytes, %v", len(out), err)
		}
	})
}
