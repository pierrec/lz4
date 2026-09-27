package lz4_test

import (
	"bufio"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/pierrec/lz4/v4"
	"github.com/pierrec/lz4/v4/internal/benchdata"
)

// The benchmarks and golden test below compress the benchdata corpus as
// independent blocks of each benchdata.BlockSizes size. Compare compressors
// or changes with benchstat, anchoring the pattern, for example:
//
//	go test -run '^$' -bench '^BenchmarkCompressBlock$/fast' -count 8 . > new.txt
//
// The bench module benchmarks the C reference implementation on the same
// corpus.

var (
	corpusOnce sync.Once
	corpus     []benchdata.Input
	corpusErr  error
)

func benchCorpus(tb testing.TB) []benchdata.Input {
	tb.Helper()
	corpusOnce.Do(func() { corpus, corpusErr = benchdata.Corpus("testdata") })
	if corpusErr != nil {
		tb.Fatal(corpusErr)
	}
	return corpus
}

type blockCompressor struct {
	name     string
	golden   bool // covered by the golden test (the slowest HC levels are not)
	compress func(src, dst []byte) int
}

func blockCompressors() []blockCompressor {
	var fast lz4.Compressor
	hc := func(level lz4.CompressionLevel) func(src, dst []byte) int {
		c := lz4.CompressorHC{Level: level}
		return func(src, dst []byte) int {
			n, _ := c.CompressBlock(src, dst)
			return n
		}
	}
	return []blockCompressor{
		{"fast", true, func(src, dst []byte) int {
			n, _ := fast.CompressBlock(src, dst)
			return n
		}},
		{"hc1", true, hc(lz4.Level1)},
		{"hc2", true, hc(lz4.Level2)},
		{"hc9", false, hc(lz4.Level9)},
	}
}

// BenchmarkCompressBlock reports the throughput of each compressor, and the
// compressed size as a percentage of the input.
func BenchmarkCompressBlock(b *testing.B) {
	for _, c := range blockCompressors() {
		for _, in := range benchCorpus(b) {
			for _, bs := range benchdata.BlockSizes {
				blocks := benchdata.Blocks(in.Data, bs)
				b.Run(fmt.Sprintf("%s/%s/%dK", c.name, in.Name, bs>>10), func(b *testing.B) {
					dst := make([]byte, lz4.CompressBlockBound(bs))
					total := 0
					for _, blk := range blocks {
						total += c.compress(blk, dst)
					}
					b.SetBytes(int64(len(in.Data)))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						for _, blk := range blocks {
							c.compress(blk, dst)
						}
					}
					// After the loop: ResetTimer deletes reported metrics.
					b.ReportMetric(100*float64(total)/float64(len(in.Data)), "%size")
				})
			}
		}
	}
}

var updateGolden = flag.Bool("update", false, "rewrite testdata/compress.golden")

const goldenPath = "testdata/compress.golden"

// TestCompressGolden checks that compressed output is unchanged, so that a
// change claiming identical output can show it. Regenerate the file with
// go test -run TestCompressGolden -update when output changes on purpose;
// the sizes it records show the effect on compression.
func TestCompressGolden(t *testing.T) {
	var lines []string
	for _, c := range blockCompressors() {
		if !c.golden {
			continue
		}
		for _, in := range benchCorpus(t) {
			for _, bs := range benchdata.BlockSizes {
				h := sha256.New()
				dst := make([]byte, lz4.CompressBlockBound(bs))
				total := 0
				for _, blk := range benchdata.Blocks(in.Data, bs) {
					n := c.compress(blk, dst)
					total += n
					h.Write(dst[:n])
				}
				lines = append(lines, fmt.Sprintf("%s/%s/%dK %d %x", c.name, in.Name, bs>>10, total, h.Sum(nil)))
			}
		}
	}
	if *updateGolden {
		if err := os.WriteFile(goldenPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.Open(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		if f := strings.SplitN(s.Text(), " ", 2); len(f) == 2 {
			want[f[0]] = f[1]
		}
	}
	if len(want) != len(lines) {
		t.Errorf("%s has %d entries, want %d", goldenPath, len(want), len(lines))
	}
	for _, line := range lines {
		f := strings.SplitN(line, " ", 2)
		if want[f[0]] != f[1] {
			t.Errorf("%s: got %s, want %s", f[0], f[1], want[f[0]])
		}
	}
}
