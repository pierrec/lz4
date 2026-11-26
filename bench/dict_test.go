package bench

import (
	"bytes"
	"testing"

	"github.com/pierrec/lz4/v4"
)

// FuzzDictMatchesC checks that blocks compressed with a dictionary by
// CompressBlockWithDict decode with LZ4_decompress_safe_usingDict, and that
// UncompressBlockWithDict decodes blocks from LZ4_compress_fast_continue and
// LZ4_compress_HC_continue after LZ4_loadDict and LZ4_loadDictHC. It needs
// liblz4, so it runs outside CI, which checks frames against the lz4 command
// in TestDictCommand instead; see README.md.
func FuzzDictMatchesC(f *testing.F) {
	f.Add([]byte("hello world"), []byte("hello"))
	f.Add(bytes.Repeat([]byte("abcd"), 100), bytes.Repeat([]byte("abcd"), 20000))
	for _, in := range benchCorpus(f) {
		// Dictionaries shorter than, equal to and longer than the window.
		for _, dl := range []int{100, 64 << 10, 100 << 10} {
			if dl < len(in.Data) {
				data := in.Data[dl:]
				f.Add(data[:min(len(data), 16<<10)], in.Data[:dl])
			}
		}
	}
	f.Fuzz(func(t *testing.T, src, dict []byte) {
		if len(src) == 0 {
			return
		}
		// The fuzzer may pass overlapping slices, which C's streaming
		// compressor does not support.
		src, dict = bytes.Clone(src), bytes.Clone(dict)
		bound := lz4.CompressBlockBound(len(src))
		out := make([]byte, len(src))

		blk := make([]byte, bound)
		n, err := lz4.CompressBlockWithDict(src, blk, dict)
		if err != nil || n == 0 {
			t.Fatalf("CompressBlockWithDict: %d, %v", n, err)
		}
		if m := DecompressSafeUsingDict(blk[:n], out, dict); m != len(src) || !bytes.Equal(out, src) {
			t.Fatalf("LZ4_decompress_safe_usingDict of Go's block returned %d", m)
		}

		for _, c := range []struct {
			name     string
			compress func(src, dst, dict []byte) int
		}{
			{"fast", func(src, dst, dict []byte) int { return CompressFastDict(src, dst, dict, 1) }},
			{"hc2", func(src, dst, dict []byte) int { return CompressHCDict(src, dst, dict, 2) }},
			{"hc9", func(src, dst, dict []byte) int { return CompressHCDict(src, dst, dict, 9) }},
		} {
			cn := c.compress(src, blk, dict)
			if cn <= 0 {
				t.Fatalf("%s: C compression returned %d", c.name, cn)
			}
			m, err := lz4.UncompressBlockWithDict(blk[:cn], out, dict)
			if err != nil || m != len(src) || !bytes.Equal(out, src) {
				t.Fatalf("%s: UncompressBlockWithDict of C's block: %d, %v", c.name, m, err)
			}
		}
	})
}
