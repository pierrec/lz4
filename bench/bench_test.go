package bench

import (
	"fmt"
	"sync"
	"testing"

	"github.com/pierrec/lz4/v4"
	"github.com/pierrec/lz4/v4/internal/benchdata"
)

var (
	corpusOnce sync.Once
	corpus     []benchdata.Input
	corpusErr  error
)

func benchCorpus(tb testing.TB) []benchdata.Input {
	tb.Helper()
	corpusOnce.Do(func() { corpus, corpusErr = benchdata.Corpus("../testdata") })
	if corpusErr != nil {
		tb.Fatal(corpusErr)
	}
	return corpus
}

// BenchmarkCompressBlock is the lz4 package's benchmark of the same name, run
// with the C implementation: fast is LZ4_compress_default, and hcN is
// LZ4_compress_HC at level N. The HC levels of the two implementations are
// not equivalent: compare their throughput together with their %size.
func BenchmarkCompressBlock(b *testing.B) {
	b.Logf("liblz4 %s", Version())
	compressors := []struct {
		name     string
		compress func(src, dst []byte) int
	}{
		{"fast", func(src, dst []byte) int { return CompressFast(src, dst, 1) }},
		{"hc2", func(src, dst []byte) int { return CompressHC(src, dst, 2) }},
		{"hc9", func(src, dst []byte) int { return CompressHC(src, dst, 9) }},
	}
	for _, c := range compressors {
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

// TestRoundTrip checks the C output decompresses with the lz4 package.
func TestRoundTrip(t *testing.T) {
	t.Logf("liblz4 %s", Version())
	for _, in := range benchCorpus(t) {
		for _, bs := range benchdata.BlockSizes {
			for _, blk := range benchdata.Blocks(in.Data, bs) {
				dst := make([]byte, lz4.CompressBlockBound(len(blk)))
				n := CompressFast(blk, dst, 1)
				out := make([]byte, len(blk))
				m, err := lz4.UncompressBlock(dst[:n], out)
				if err != nil || m != len(blk) {
					t.Fatalf("%s/%d: %v", in.Name, bs, err)
				}
			}
		}
	}
}
