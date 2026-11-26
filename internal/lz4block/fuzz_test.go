package lz4block

import (
	"bytes"
	"fmt"
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
	f.Add([]byte(""), []byte(nil))
	f.Add([]byte("a"), []byte(nil))
	f.Add(bytes.Repeat([]byte("a"), 300), []byte(nil))
	f.Add(bytes.Repeat([]byte("ab"), 300), []byte(nil))
	f.Add(bytes.Repeat([]byte("abcdefgh"), 100), []byte(nil))
	f.Add(bytes.Repeat([]byte("0123456789ab"), 60), []byte(nil))
	f.Add(bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 40), []byte(nil))
	rec := make([]byte, 0, 4096)
	for i := range 64 {
		rec = append(rec, bytes.Repeat([]byte{byte(i)}, 64)...)
	}
	f.Add(rec, []byte(nil))
	dict := bytes.Repeat([]byte("0123456789abcdefghij"), 100)
	f.Add([]byte("0123456789abcdefghij"), dict)
	f.Add([]byte("the quick brown fox jumps over the lazy dog. "), []byte("the quick brown fox "))
}

// FuzzCompressorCCompat checks CompressorCCompat and CompressorCCompatHC
// against arbitrary input, destination sizes and accelerations or levels; see
// checkCCompat. A level of 0 selects CompressorCCompat, and the others
// CompressorCCompatHC at level-1, so that its default level is reached too.
func FuzzCompressorCCompat(f *testing.F) {
	for _, src := range ccompatInputs(f) {
		if len(src) > 1<<16 {
			src = src[:1<<16]
		}
		for _, level := range []uint8{0, 3, 4, 10, 13} {
			f.Add(src, uint32(CompressBlockBound(len(src))), uint8(1), level)
			f.Add(src, uint32(len(src)/2), uint8(3), level)
		}
	}
	var c CompressorCCompat
	var hc CompressorCCompatHC
	f.Fuzz(func(t *testing.T, src []byte, dstLen uint32, accel, level uint8) {
		bound := CompressBlockBound(len(src))
		if int(dstLen) > bound {
			dstLen = uint32(bound)
		}
		if level == 0 {
			checkCCompat(t, &c, src, int(dstLen), int(accel))
			return
		}
		checkCCompatHC(t, &hc, src, int(dstLen), int(level%14)-1)
	})
}

func FuzzBlockRoundTrip(f *testing.F) {
	fuzzSeeds(f)
	for _, b := range corpusFiles(f, "../../fuzz/corpus", 64<<10) {
		f.Add(b, []byte(nil))
	}
	f.Fuzz(func(t *testing.T, data, dict []byte) {
		comp := make([]byte, CompressBlockBound(len(data)))
		var c Compressor
		n, err := c.CompressBlockWithDict(data, comp, dict)
		if err != nil {
			t.Fatalf("compress: %v", err)
		}
		if n == 0 {
			// Only a dst shorter than CompressBlockBound may give up.
			t.Fatal("compress: n == 0 with a CompressBlockBound dst")
		}
		comp = comp[:n]
		// Compressing again, reusing c's index of dict, and through the
		// pool must give the same output.
		again := make([]byte, CompressBlockBound(len(data)))
		for name, compress := range map[string]func() (int, error){
			"reused": func() (int, error) { return c.CompressBlockWithDict(data, again, dict) },
			"pooled": func() (int, error) { return CompressBlockWithDict(data, again, dict) },
		} {
			if n2, err := compress(); err != nil || !bytes.Equal(again[:n2], comp) {
				t.Fatalf("%s compress differs: n=%d err=%v", name, n2, err)
			}
		}
		checkBlockDecodes(t, "fast", comp, data, dict)

		// The same with CompressorHC: reusing its index of dict gives the
		// same output, and so does CompressBlock afterwards, without dict,
		// as with a fresh CompressorHC.
		var hc CompressorHC
		for _, depth := range []CompressionLevel{2, 9} {
			hcComp := make([]byte, CompressBlockBound(len(data)))
			n, err := hc.CompressBlockWithDict(data, hcComp, dict, depth)
			if err != nil || n == 0 {
				t.Fatalf("HC %d compress: n=%d err=%v", depth, n, err)
			}
			if n2, err := hc.CompressBlockWithDict(data, again, dict, depth); err != nil || !bytes.Equal(again[:n2], hcComp[:n]) {
				t.Fatalf("HC %d reused compress differs: n=%d err=%v", depth, n2, err)
			}
			checkBlockDecodes(t, fmt.Sprintf("HC %d", depth), hcComp[:n], data, dict)
		}
		n2, err := hc.CompressBlock(data, again, 9)
		n3, err3 := new(CompressorHC).CompressBlock(data, comp[:cap(comp)], 9)
		if err != nil || err3 != nil || !bytes.Equal(again[:n2], comp[:n3]) {
			t.Fatalf("HC CompressBlock after CompressBlockWithDict differs: %d, %v; fresh %d, %v", n2, err, n3, err3)
		}
	})
}

// checkBlockDecodes checks that comp decodes to data with dict into
// destinations with spare room, which must be left untouched.
func checkBlockDecodes(t *testing.T, name string, comp, data, dict []byte) {
	t.Helper()
	const guard = 64
	for _, slack := range []int{0, 1, 15, 16, 17, 31, 32, 33, 64, 1024} {
		dst := make([]byte, len(data)+slack+guard)
		for i := range dst {
			dst[i] = 0xA5
		}
		got := decodeBlock(dst[:len(data)+slack], comp, dict)
		if got != len(data) {
			t.Fatalf("%s slack %d: decodeBlock returned %d, want %d", name, slack, got, len(data))
		}
		if !bytes.Equal(dst[:got], data) {
			t.Fatalf("%s slack %d: round trip mismatch", name, slack)
		}
		for i := len(data) + slack; i < len(dst); i++ {
			if dst[i] != 0xA5 {
				t.Fatalf("%s slack %d: guard byte %d overwritten", name, slack, i-len(data)-slack)
			}
		}
	}
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
		n := min(4*len(b), 1<<16-1)
		f.Add(b, []byte(nil), uint16(n))
	}
	f.Add([]byte("\x11b\x0a\x00\x401234"), []byte("barbazquux"), uint16(10))
	f.Add([]byte("\x1a1\x05\x00\x50abcde"), []byte("---2345"), uint16(20))
	// Seeds for the decoders' long-copy paths: long overlapping matches at
	// short, medium and far offsets, a long literal, and dictionary matches
	// that end in the dictionary or continue into dst.
	for _, m := range [][3]int{{16, 1, 60000}, {16, 3, 5000}, {40, 40, 20000}, {1000, 1000, 30000}, {20000, 20000, 40000}, {1000, 1, 4}} {
		src, dec := buildSingleMatchBlock(m[0], m[1], m[2])
		f.Add(src, []byte(nil), uint16(len(dec)))
	}
	dict := bytes.Repeat([]byte("0123456789abcdefghij"), 100)
	for _, m := range [][2]int{{1500, 1000}, {500, 3000}} {
		var buf bytes.Buffer
		writeToken(&buf, 0, m[1]-minMatch)
		buf.Write([]byte{byte(m[0]), byte(m[0] >> 8)})
		writeExtended(&buf, m[1]-minMatch-15)
		writeToken(&buf, 1, 0)
		buf.WriteByte('x')
		f.Add(buf.Bytes(), dict, uint16(m[1]+1))
	}
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
