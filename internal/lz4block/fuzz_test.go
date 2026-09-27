package lz4block

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The fuzzers for blocks. The assembly decoders overrun on purpose when there
// is room, so the fuzzers check that nothing is written past the
// destination, and FuzzDecodeBlockDifferential checks them against the Go
// decoder: run them with -fuzz on each architecture that has an assembly
// decoder. They are seeded with the corpora that go-fuzz collected in
// ../../fuzz. The frame fuzzers are in the lz4 package.

// corpusFiles returns the files in dir of at most max bytes.
func corpusFiles(tb testing.TB, dir string, max int) [][]byte {
	tb.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		tb.Fatal(err)
	}
	var files [][]byte
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			tb.Fatal(err)
		}
		if len(b) <= max {
			files = append(files, b)
		}
	}
	return files
}

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
	for _, b := range corpusFiles(f, "../../fuzz/corpus", 64<<10) {
		f.Add(b)
	}
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

// FuzzDecodeBlockDifferential checks the assembly decoder against the Go one
// on arbitrary input, dictionary and destination size.
func FuzzDecodeBlockDifferential(f *testing.F) {
	var c Compressor
	for _, data := range compressionInputs() {
		comp := make([]byte, CompressBlockBound(len(data)))
		n, _ := c.CompressBlock(data, comp)
		f.Add(comp[:n], []byte(nil), uint16(len(data)))
		f.Add(comp[:n], data[:len(data)/2], uint16(len(data)/2))
	}
	// Blocks go-fuzz found for UncompressBlock.
	for _, b := range corpusFiles(f, "../../fuzz/uncompress/corpus", 1<<20) {
		n := 4 * len(b)
		if n > 1<<16-1 {
			n = 1<<16 - 1
		}
		f.Add(b, []byte(nil), uint16(n))
	}
	f.Add([]byte("\x11b\x0a\x00\x401234"), []byte("barbazquux"), uint16(10))
	f.Add([]byte("\x1a1\x05\x00\x50abcde"), []byte("---2345"), uint16(20))
	f.Fuzz(func(t *testing.T, src, dict []byte, dstLen uint16) {
		want, wn := guardedDecode(t, decodeBlockGo, src, dict, int(dstLen))
		got, gn := guardedDecode(t, decodeBlock, src, dict, int(dstLen))
		if (gn < 0) != (wn < 0) {
			t.Fatalf("decodeBlock=%d decodeBlockGo=%d", gn, wn)
		}
		if gn >= 0 && (gn != wn || !bytes.Equal(got, want)) {
			t.Fatalf("output mismatch: decodeBlock=%d decodeBlockGo=%d", gn, wn)
		}
	})
}

// FuzzCompressBlockDst compresses arbitrary input into an arbitrarily sized
// destination with each compressor.
func FuzzCompressBlockDst(f *testing.F) {
	for _, data := range compressionInputs() {
		f.Add(data, uint32(len(data)), uint8(0))
		f.Add(data, uint32(len(data)/2), uint8(9))
	}
	for _, data := range corpusFiles(f, "../../fuzz/corpus", 64<<10) {
		f.Add(data, uint32(len(data)), uint8(0))
	}
	f.Fuzz(func(t *testing.T, src []byte, dstLen uint32, level uint8) {
		bound := CompressBlockBound(len(src))
		if int(dstLen) > bound {
			dstLen = uint32(bound)
		}
		buf := make([]byte, int(dstLen)+64)
		for i := range buf {
			buf[i] = guardByte
		}
		var n int
		var err error
		if level%10 == 0 {
			n, err = CompressBlock(src, buf[:dstLen])
		} else {
			n, err = CompressBlockHC(src, buf[:dstLen], CompressionLevel(1<<(8+level%10)))
		}
		for i := int(dstLen); i < len(buf); i++ {
			if buf[i] != guardByte {
				t.Fatalf("wrote past dst")
			}
		}
		if n < 0 || n > int(dstLen) {
			t.Fatalf("n=%d out of range for dst=%d", n, dstLen)
		}
		if int(dstLen) == bound && (err != nil || n == 0) {
			t.Fatalf("dst=bound: n=%d err=%v", n, err)
		}
		if err != nil || n == 0 {
			return
		}
		dec := make([]byte, len(src))
		dn, err := UncompressBlock(buf[:n], dec, nil)
		if err != nil || !bytes.Equal(dec[:dn], src) {
			t.Fatalf("round trip failed: %v", err)
		}
	})
}
