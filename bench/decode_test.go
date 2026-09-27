package bench

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/pierrec/lz4/v4"
	"github.com/pierrec/lz4/v4/internal/benchdata"
)

// BenchmarkUncompressBlock decodes the same compressed blocks with both
// implementations: decoder/source/input/blocksize, where decoder is go
// (lz4.UncompressBlock) or c (LZ4_decompress_safe), and source is the
// compressor that produced the blocks. Throughput is of uncompressed bytes.
func BenchmarkUncompressBlock(b *testing.B) {
	b.Logf("liblz4 %s", Version())
	var fast lz4.Compressor
	gohc := lz4.CompressorHC{Level: lz4.Level2}
	sources := []struct {
		name     string
		compress func(src, dst []byte) int
	}{
		{"gofast", func(src, dst []byte) int { n, _ := fast.CompressBlock(src, dst); return n }},
		{"cfast", func(src, dst []byte) int { return CompressFast(src, dst, 1) }},
		{"gohc2", func(src, dst []byte) int { n, _ := gohc.CompressBlock(src, dst); return n }},
		{"chc9", func(src, dst []byte) int { return CompressHC(src, dst, 9) }},
	}
	decoders := []struct {
		name   string
		decode func(src, dst []byte) int
	}{
		{"go", func(src, dst []byte) int { n, _ := lz4.UncompressBlock(src, dst); return n }},
		{"c", DecompressSafe},
	}
	for _, s := range sources {
		for _, in := range benchCorpus(b) {
			for _, bs := range benchdata.BlockSizes {
				var comp [][]byte
				for _, blk := range benchdata.Blocks(in.Data, bs) {
					dst := make([]byte, lz4.CompressBlockBound(len(blk)))
					comp = append(comp, dst[:s.compress(blk, dst)])
				}
				for _, d := range decoders {
					b.Run(fmt.Sprintf("%s/%s/%s/%dK", d.name, s.name, in.Name, bs>>10), func(b *testing.B) {
						out := make([]byte, bs)
						// Check the decoder once, outside the timing.
						for i, c := range comp {
							n := d.decode(c, out)
							if want := benchdata.Blocks(in.Data, bs)[i]; n != len(want) || !bytes.Equal(out[:n], want) {
								b.Fatalf("block %d: decoded %d bytes, want %d", i, n, len(want))
							}
						}
						b.SetBytes(int64(len(in.Data)))
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							for _, c := range comp {
								d.decode(c, out)
							}
						}
					})
				}
			}
		}
	}
}
