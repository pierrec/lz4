package lz4block_test

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/pierrec/lz4/v4/internal/lz4block"
)

// Runs of zeros of varying lengths, then one run longer than the window.
func TestCompressBlockHCRuns(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var src []byte
	for len(src) < 100<<10 {
		src = append(src, make([]byte, 16+rng.Intn(64))...)
		src = append(src, 1)
	}
	src = append(src, make([]byte, 200<<10)...)
	for _, n := range []int{64 << 10, len(src)} {
		dst := make([]byte, lz4block.CompressBlockBound(n))
		m, err := lz4block.CompressBlockHC(src[:n], dst, 1024)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]byte, n)
		if k, err := lz4block.UncompressBlock(dst[:m], got, nil); err != nil || k != n || !bytes.Equal(got, src[:n]) {
			t.Fatalf("%d bytes: round trip failed: %v", n, err)
		}
	}
}
