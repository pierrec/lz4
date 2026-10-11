package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pierrec/lz4/v4"
)

type mockWriter struct {
	options []lz4.Option
}

func (m *mockWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (m *mockWriter) Close() error {
	return nil
}

func (m *mockWriter) Reset(io.Writer) {}

func (m *mockWriter) Apply(options ...lz4.Option) error {
	m.options = append(m.options, options...)
	return nil
}

func TestCompressLevels(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantLevel lz4.CompressionLevel
		wantErr   error
	}{
		{name: "default level", args: nil, wantLevel: lz4.Fast},
		{name: "level 0", args: []string{"-l", "0"}, wantLevel: lz4.Fast},
		{name: "level 1", args: []string{"-l", "1"}, wantLevel: lz4.Level1},
		{name: "level 2", args: []string{"-l", "2"}, wantLevel: lz4.Level2},
		{name: "level 3", args: []string{"-l", "3"}, wantLevel: lz4.Level3},
		{name: "level 4", args: []string{"-l", "4"}, wantLevel: lz4.Level4},
		{name: "level 5", args: []string{"-l", "5"}, wantLevel: lz4.Level5},
		{name: "level 6", args: []string{"-l", "6"}, wantLevel: lz4.Level6},
		{name: "level 7", args: []string{"-l", "7"}, wantLevel: lz4.Level7},
		{name: "level 8", args: []string{"-l", "8"}, wantLevel: lz4.Level8},
		{name: "level 9", args: []string{"-l", "9"}, wantLevel: lz4.Level9},
		{name: "level 10 invalid", args: []string{"-l", "10"}, wantErr: lz4.ErrOptionInvalidCompressionLevel},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockWriter{}
			origNewWriter := newWriter
			newWriter = func(w io.Writer) writer {
				return mock
			}
			t.Cleanup(func() {
				newWriter = origNewWriter
			})

			fs := flag.NewFlagSet("compress", flag.ContinueOnError)
			handler := Compress(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("fs.Parse failed: %v", err)
			}

			tmpDir := t.TempDir()
			srcFile := filepath.Join(tmpDir, "input.txt")
			if err := os.WriteFile(srcFile, nil, 0o644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			_, err := handler(srcFile)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got error %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("handler failed: %v", err)
			}

			wantOpt := lz4.CompressionLevelOption(tc.wantLevel).String()
			var gotOpts []string
			for _, opt := range mock.options {
				s := opt.String()
				if strings.HasPrefix(s, "CompressionLevelOption") {
					gotOpts = append(gotOpts, s)
				}
			}
			if len(gotOpts) != 1 {
				t.Fatalf("expected 1 CompressionLevelOption, got %d: %v", len(gotOpts), gotOpts)
			}
			if gotOpts[0] != wantOpt {
				t.Fatalf("got %s, want %s", gotOpts[0], wantOpt)
			}
		})
	}
}
