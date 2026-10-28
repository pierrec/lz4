package lz4_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/pierrec/lz4/v4"
)

func TestCompressingReader(t *testing.T) {
	goldenFiles := []string{
		"testdata/e.txt.gz",
		"testdata/gettysburg.txt.gz",
		"testdata/Mark.Twain-Tom.Sawyer.txt.gz",
		"testdata/Mark.Twain-Tom.Sawyer_long.txt.gz",
		"testdata/pg1661.txt.gz",
		"testdata/pi.txt.gz",
		"testdata/random.data.gz",
		"testdata/repeat.txt.gz",
		"testdata/issue102.data.gz",
	}

	for _, fname := range goldenFiles {
		for _, option := range []lz4.Option{
			lz4.BlockChecksumOption(true),
		} {
			label := fmt.Sprintf("%s/%s", fname, option)
			t.Run(label, func(t *testing.T) {
				fname := fname
				option := option
				t.Parallel()

				raw, err := os.ReadFile(fname)
				if err != nil {
					t.Fatal(err)
				}
				r := io.NopCloser(bytes.NewReader(raw))

				// Compress.
				zcomp := lz4.NewCompressingReader(r)
				if err := zcomp.Apply(option, lz4.CompressionLevelOption(lz4.Level1)); err != nil {
					t.Fatal(err)
				}

				zout, err := io.ReadAll(zcomp)
				if err != nil {
					t.Fatal(err)
				}

				// Uncompress.
				zr := lz4.NewReader(bytes.NewReader(zout))
				out, err := io.ReadAll(zr)
				if err != nil {
					t.Fatal(err)
				}

				// The uncompressed data must be the same as the initial input.
				if got, want := len(out), len(raw); got != want {
					t.Errorf("invalid sizes: got %d; want %d", got, want)
				}

				if !bytes.Equal(out, raw) {
					t.Fatal("uncompressed data does not match original")
				}
			})
		}
	}
}

func TestCompressingReader_Dictionary(t *testing.T) {
	dict := []byte("schema:id,name,email,timestamp,event,status,metadata,payload,response")
	src := []byte("schema:id=1001,name=Alice,email=alice@example.com,timestamp=1690000000,event=login,status=ok\n" +
		"schema:id=1002,name=Bob,email=bob@example.com,timestamp=1690000001,event=logout,status=ok\n")

	cr := lz4.NewCompressingReader(io.NopCloser(bytes.NewReader(src)))
	if err := cr.Apply(lz4.DictionaryOption(dict)); err != nil {
		t.Fatalf("cr Apply DictionaryOption failed: %v", err)
	}
	compressed, err := io.ReadAll(cr)
	if err != nil {
		t.Fatalf("cr ReadAll failed: %v", err)
	}

	zr := lz4.NewReader(bytes.NewReader(compressed))
	if err := zr.Apply(lz4.DictionaryOption(dict)); err != nil {
		t.Fatalf("zr Apply DictionaryOption failed: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("zr ReadAll failed: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Fatalf("CompressingReader roundtrip mismatch")
	}

	// Non-Fast level with dictionary should return ErrOptionNotApplicable.
	cr2 := lz4.NewCompressingReader(io.NopCloser(bytes.NewReader(src)))
	if err := cr2.Apply(lz4.CompressionLevelOption(lz4.Level1), lz4.DictionaryOption(dict)); !errors.Is(err, lz4.ErrOptionNotApplicable) {
		t.Fatalf("expected ErrOptionNotApplicable, got %v", err)
	}
}
