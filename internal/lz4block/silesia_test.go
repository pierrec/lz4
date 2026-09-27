package lz4block

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"testing"
)

// TestSilesiaDecodeDifferential compresses the Silesia corpus in 64 KiB
// blocks with each compressor and checks that the decoder in use (assembly
// unless built with noasm) and the pure-Go one both reproduce every block.
// It skips unless testdata/fetch_silesia.sh has fetched the corpus.
func TestSilesiaDecodeDifferential(t *testing.T) {
	data, err := os.ReadFile("../../testdata/silesia.tar")
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("testdata/silesia.tar is missing: run testdata/fetch_silesia.sh")
	}
	if err != nil {
		t.Fatal(err)
	}
	var (
		fast   Compressor
		hc     CompressorHC
		ccompt CompressorCCompat
	)
	for _, tc := range []struct {
		name     string
		compress func(src, dst []byte) (int, error)
	}{
		{"fast", fast.CompressBlock},
		{"ccompat", func(src, dst []byte) (int, error) { return ccompt.CompressBlock(src, dst, 1) }},
		// Depth 1<<9 is the public Level2.
		{"level2", func(src, dst []byte) (int, error) { return hc.CompressBlock(src, dst, 1<<9) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const bs = 64 << 10
			comp := make([]byte, CompressBlockBound(bs))
			dec := make([]byte, bs)
			ref := make([]byte, bs)
			for off := 0; off < len(data); off += bs {
				src := data[off:min(off+bs, len(data))]
				n, err := tc.compress(src, comp)
				if err != nil {
					t.Fatalf("block at %d: compress: %v", off, err)
				}
				m := decodeBlock(dec, comp[:n], nil)
				r := decodeBlockGo(ref, comp[:n], nil)
				if m != len(src) || !bytes.Equal(dec[:m], src) {
					t.Fatalf("block at %d: decodeBlock returned %d of %d bytes or wrong data", off, m, len(src))
				}
				if r != len(src) || !bytes.Equal(ref[:r], src) {
					t.Fatalf("block at %d: decodeBlockGo returned %d of %d bytes or wrong data", off, r, len(src))
				}
			}
		})
	}
}
