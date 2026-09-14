package lz4block

import (
	"bytes"
	"testing"
)

// The assembly decoders overrun on purpose when there is room, so both
// fuzzers check that nothing is written past the destination. Run them
// with -fuzz on each architecture that has an assembly decoder.

func fuzzSeeds(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("a"))
	f.Add(bytes.Repeat([]byte("a"), 300))
	f.Add(bytes.Repeat([]byte("ab"), 300))
	f.Add(bytes.Repeat([]byte("abcdefgh"), 100))
	f.Add(bytes.Repeat([]byte("0123456789ab"), 60))
	f.Add(bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 40))
	rec := make([]byte, 0, 4096)
	for i := 0; i < 64; i++ {
		rec = append(rec, bytes.Repeat([]byte{byte(i)}, 64)...)
	}
	f.Add(rec)
}

func FuzzBlockRoundTrip(f *testing.F) {
	fuzzSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		comp := make([]byte, CompressBlockBound(len(data)))
		var c Compressor
		n, err := c.CompressBlock(data, comp)
		if err != nil {
			t.Fatalf("compress: %v", err)
		}
		comp = comp[:n]
		if n == 0 {
			// n == 0 means incompressible.
			return
		}
		const guard = 64
		for _, slack := range []int{0, 1, 15, 16, 17, 31, 32, 33, 64, 1024} {
			dst := make([]byte, len(data)+slack+guard)
			for i := range dst {
				dst[i] = 0xA5
			}
			got := decodeBlock(dst[:len(data)+slack], comp, nil)
			if got != len(data) {
				t.Fatalf("slack %d: decodeBlock returned %d, want %d", slack, got, len(data))
			}
			if !bytes.Equal(dst[:got], data) {
				t.Fatalf("slack %d: round trip mismatch", slack)
			}
			for i := len(data) + slack; i < len(dst); i++ {
				if dst[i] != 0xA5 {
					t.Fatalf("slack %d: guard byte %d overwritten", slack, i-len(data)-slack)
				}
			}
		}
	})
}

func FuzzDecodeBlockMutated(f *testing.F) {
	var c Compressor
	for _, data := range [][]byte{
		bytes.Repeat([]byte("a"), 300),
		bytes.Repeat([]byte("abcdefgh"), 100),
		bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 40),
	} {
		comp := make([]byte, CompressBlockBound(len(data)))
		n, err := c.CompressBlock(data, comp)
		if err != nil || n == 0 {
			f.Fatalf("seed compress: n=%d err=%v", n, err)
		}
		f.Add(comp[:n], uint16(len(data)))
		f.Add(comp[:n], uint16(len(data)/2))
		f.Add(comp[:n], uint16(len(data)*2))
	}
	f.Fuzz(func(t *testing.T, src []byte, dstLen uint16) {
		const guard = 64
		dst := make([]byte, int(dstLen)+guard)
		for i := range dst {
			dst[i] = 0xA5
		}
		n := decodeBlock(dst[:dstLen], src, nil)
		if n > int(dstLen) {
			t.Fatalf("decodeBlock returned %d for a %d byte destination", n, dstLen)
		}
		for i := int(dstLen); i < len(dst); i++ {
			if dst[i] != 0xA5 {
				t.Fatalf("guard byte %d overwritten (n=%d)", i-int(dstLen), n)
			}
		}
	})
}
