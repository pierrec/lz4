package lz4_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pierrec/lz4/v4"
)

// BenchmarkCorpus decodes each file in the directory named by LZ4_CORPUS
// (see testdata/fetch_corpus.sh), as frames from the Writer (go4M) and, if
// present, the C CLI: NAME.B7D.lz4 and NAME.B4D.lz4 (lz4 -BD -B7 and -B4,
// linked 4 MiB and 64 KiB blocks). It skips if LZ4_CORPUS is unset.
func BenchmarkCorpus(b *testing.B) {
	dir := os.Getenv("LZ4_CORPUS")
	if dir == "" {
		b.Skip("LZ4_CORPUS is not set")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		b.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".lz4") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			b.Fatal(err)
		}
		for _, enc := range []struct {
			tag  string
			comp func(b *testing.B) []byte
		}{
			{"go4M", func(b *testing.B) []byte { return writeFrame(b, raw) }},
			{"cLinked4M", func(b *testing.B) []byte { return readOptional(b, filepath.Join(dir, name+".B7D.lz4")) }},
			{"cLinked64K", func(b *testing.B) []byte { return readOptional(b, filepath.Join(dir, name+".B4D.lz4")) }},
		} {
			b.Run(name+"/"+enc.tag, func(b *testing.B) {
				comp := enc.comp(b)
				r := bytes.NewReader(comp)
				zr := lz4.NewReader(r)
				b.SetBytes(int64(len(raw)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					r.Reset(comp)
					zr.Reset(r)
					if n, err := io.Copy(io.Discard, zr); err != nil || n != int64(len(raw)) {
						b.Fatalf("decoded %d of %d bytes: %v", n, len(raw), err)
					}
				}
			})
		}
	}
}

// readOptional reads path, skipping the test or benchmark if it does not
// exist.
func readOptional(tb testing.TB, path string) []byte {
	tb.Helper()
	d, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		tb.Skipf("%s is missing", path)
	}
	if err != nil {
		tb.Fatal(err)
	}
	return d
}
