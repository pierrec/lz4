package lz4block

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// The tests in this file check boundary conditions and hostile inputs. Every
// decode runs against both decodeBlock (assembly where available) and
// decodeBlockGo, which must agree.

var testDecoders = []struct {
	name string
	fn   func(dst, src, dict []byte) int
}{
	{"decodeBlock", decodeBlock},
	{"decodeBlockGo", decodeBlockGo},
}

const guardByte = 0xA5

// guardedDecode decodes src into a buffer of dstLen bytes followed by guard
// bytes, and fails if the decoder writes past dstLen or returns more than it.
func guardedDecode(t testing.TB, fn func(dst, src, dict []byte) int, src, dict []byte, dstLen int) ([]byte, int) {
	t.Helper()
	const guard = 64
	buf := make([]byte, dstLen+guard)
	for i := range buf {
		buf[i] = guardByte
	}
	n := fn(buf[:dstLen], src, dict)
	if n > dstLen {
		t.Fatalf("decoder returned %d for a %d byte destination", n, dstLen)
	}
	for i := dstLen; i < len(buf); i++ {
		if buf[i] != guardByte {
			t.Fatalf("guard byte %d overwritten (n=%d)", i-dstLen, n)
		}
	}
	if n < 0 {
		return nil, n
	}
	return buf[:n], n
}

// appendLenExt appends the LSIC extension bytes for a length whose 4-bit
// token field is saturated at 15.
func appendLenExt(b []byte, n int) []byte {
	for ; n >= 255; n -= 255 {
		b = append(b, 255)
	}
	return append(b, byte(n))
}

// appendSeq appends one LZ4 sequence. mlen == 0 means a final, literals only
// sequence.
func appendSeq(b, lits []byte, off, mlen int) []byte {
	var tok byte
	ln := len(lits)
	if ln >= 15 {
		tok = 0xF0
	} else {
		tok = byte(ln) << 4
	}
	ml := mlen - minMatch
	if mlen > 0 {
		if ml >= 15 {
			tok |= 0x0F
		} else {
			tok |= byte(ml)
		}
	}
	b = append(b, tok)
	if ln >= 15 {
		b = appendLenExt(b, ln-15)
	}
	b = append(b, lits...)
	if mlen == 0 {
		return b
	}
	b = append(b, byte(off), byte(off>>8))
	if ml >= 15 {
		b = appendLenExt(b, ml-15)
	}
	return b
}

// refDecodeSeq is what appendSeq's sequence decodes to, given the output so far
// and the dictionary; ok is false if the offset is out of range.
func refDecodeSeq(out, dict, lits []byte, off, mlen int) (_ []byte, ok bool) {
	out = append(out, lits...)
	if mlen == 0 {
		return out, true
	}
	if off == 0 || off > len(out)+len(dict) {
		return nil, false
	}
	for i := 0; i < mlen; i++ {
		pos := len(dict) + len(out) - off
		if pos < len(dict) {
			out = append(out, dict[pos])
		} else {
			out = append(out, out[pos-len(dict)])
		}
	}
	return out, true
}

func testPattern(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7) ^ seed
	}
	return b
}

func TestDecodeLengthBoundaries(t *testing.T) {
	// Literal and match lengths on either side of the 4-bit token limit and of
	// each extra length byte.
	litLens := []int{0, 1, 14, 15, 16, 17, 15 + 254, 15 + 255, 15 + 256, 15 + 255 + 255, 1000}
	matchLens := []int{4, 5, 18, 19, 20, 19 + 254, 19 + 255, 19 + 256, 19 + 255 + 255, 1000}
	offsets := []int{1, 2, 3, 4, 7, 8, 9, 15, 16, 17, 31, 32, 33}
	tail := []byte("final")

	for _, ll := range litLens {
		for _, ml := range matchLens {
			for _, off := range offsets {
				lits := testPattern(ll, 0x3C)
				src := appendSeq(nil, lits, off, ml)
				src = appendSeq(src, tail, 0, 0)
				want, ok := refDecodeSeq(nil, nil, lits, off, ml)
				if ok {
					want = append(want, tail...)
				}
				name := fmt.Sprintf("lit%d/match%d/off%d", ll, ml, off)
				for _, d := range testDecoders {
					for _, slack := range []int{-1, 0, 1, 8, 32} {
						dstLen := len(want) + slack
						if !ok {
							dstLen = 2048 + slack
						}
						if dstLen < 0 {
							continue
						}
						got, n := guardedDecode(t, d.fn, src, nil, dstLen)
						switch {
						case !ok || slack < 0:
							if n >= 0 {
								t.Fatalf("%s %s slack=%d: want error, got %d bytes", d.name, name, slack, n)
							}
						case n < 0:
							t.Fatalf("%s %s slack=%d: unexpected error %d", d.name, name, slack, n)
						case !bytes.Equal(got, want):
							t.Fatalf("%s %s slack=%d: output mismatch", d.name, name, slack)
						}
					}
				}
			}
		}
	}
}

func TestDecodeDictBoundaries(t *testing.T) {
	tail := []byte("final")
	for _, dictLen := range []int{0, 1, 3, 4, 15, 16, 17, 1<<16 - 1, 1 << 16, 1<<16 + 1, 1 << 20} {
		dict := testPattern(dictLen, 0x5A)
		for _, ll := range []int{0, 3, 20} {
			for _, off := range []int{1, 3, 4, 16, dictLen - 1, dictLen, dictLen + ll, dictLen + ll + 1, 1<<16 - 1} {
				if off < 1 || off > 1<<16-1 {
					continue
				}
				for _, ml := range []int{4, 19, 100} {
					lits := testPattern(ll, 0xC3)
					src := appendSeq(nil, lits, off, ml)
					src = appendSeq(src, tail, 0, 0)
					want, ok := refDecodeSeq(nil, dict, lits, off, ml)
					if ok {
						want = append(want, tail...)
					}
					name := fmt.Sprintf("dict%d/lit%d/off%d/match%d", dictLen, ll, off, ml)
					for _, d := range testDecoders {
						got, n := guardedDecode(t, d.fn, src, dict, ll+ml+len(tail))
						switch {
						case !ok:
							if n >= 0 {
								t.Fatalf("%s %s: want error, got %d bytes", d.name, name, n)
							}
						case n < 0:
							t.Fatalf("%s %s: unexpected error %d", d.name, name, n)
						case !bytes.Equal(got, want):
							t.Fatalf("%s %s: output mismatch\n got %x\nwant %x", d.name, name, got, want)
						}
					}
				}
			}
		}
	}
}

// Every prefix of a valid block, and every single byte corruption of it, must
// be handled identically by both decoders without writing out of bounds.
func TestDecodeTruncatedAndCorruptBlocks(t *testing.T) {
	var src []byte
	src = appendSeq(src, testPattern(20, 1), 3, 40)
	src = appendSeq(src, testPattern(300, 2), 17, 300)
	src = appendSeq(src, nil, 1, 4)
	src = appendSeq(src, testPattern(7, 3), 0, 0)
	dict := testPattern(100, 4)

	check := func(name string, blk []byte) {
		t.Helper()
		for _, dstLen := range []int{0, 1, 100, 671, 672, 1024} {
			for _, dc := range [][]byte{nil, dict} {
				want, wn := guardedDecode(t, decodeBlockGo, blk, dc, dstLen)
				got, gn := guardedDecode(t, decodeBlock, blk, dc, dstLen)
				if (gn < 0) != (wn < 0) || (gn >= 0 && (gn != wn || !bytes.Equal(got, want))) {
					t.Fatalf("%s dst=%d dict=%d: decodeBlock=%d decodeBlockGo=%d", name, dstLen, len(dc), gn, wn)
				}
			}
		}
	}
	for i := 0; i <= len(src); i++ {
		check(fmt.Sprintf("prefix%d", i), src[:i])
	}
	blk := make([]byte, len(src))
	for i := range src {
		for _, v := range []byte{0, 1, 0x0F, 0x10, 0x7F, 0xF0, 0xFF, src[i] ^ 0x80} {
			copy(blk, src)
			blk[i] = v
			check(fmt.Sprintf("byte%d=%#x", i, v), blk)
		}
	}
}

func compressionInputs() map[string][]byte {
	rnd := rand.New(rand.NewSource(1))
	random := func(n int) []byte {
		b := make([]byte, n)
		rnd.Read(b)
		return b
	}
	text := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 2000)
	in := map[string][]byte{
		"empty":        {},
		"text64K":      text[:1<<16],
		"text64K+1":    text[:1<<16+1],
		"random1K":     random(1024),
		"random64K":    random(1 << 16),
		"zeros64K":     make([]byte, 1<<16),
		"zeros64K+100": make([]byte, 1<<16+100),
		"mixed":        append(append(random(3000), make([]byte, 3000)...), random(3000)...),
	}
	// Sizes around mfLimit and minMatch, where the compressors switch to
	// emitting only literals.
	for _, n := range []int{1, 2, 3, 4, 5, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20} {
		in[fmt.Sprint("zeros", n)] = make([]byte, n)
		in[fmt.Sprint("random", n)] = random(n)
	}
	return in
}

// Compressors must not panic or overrun dst whatever its size, must always
// succeed with CompressBlockBound, and must produce decodable output.
func TestCompressBlockDstBoundaries(t *testing.T) {
	compressors := []struct {
		name string
		fn   func(src, dst []byte) (int, error)
	}{
		{"fast", CompressBlock},
		{"hc1", func(src, dst []byte) (int, error) { return CompressBlockHC(src, dst, 1<<9) }},
		{"hc9", func(src, dst []byte) (int, error) { return CompressBlockHC(src, dst, 1<<17) }},
		{"hc0", func(src, dst []byte) (int, error) { return CompressBlockHC(src, dst, 0) }},
	}
	for name, src := range compressionInputs() {
		for _, c := range compressors {
			bound := CompressBlockBound(len(src))
			full := make([]byte, bound)
			fn, err := c.fn(src, full)
			if err != nil || fn <= 0 {
				t.Fatalf("%s %s: with CompressBlockBound got n=%d err=%v", c.name, name, fn, err)
			}
			for _, dstLen := range []int{0, 1, fn - 1, fn, fn + 1, len(src) - 1, len(src), len(src) + 1, bound - 1, bound} {
				if dstLen < 0 || dstLen > bound {
					continue
				}
				buf := make([]byte, dstLen+64)
				for i := range buf {
					buf[i] = guardByte
				}
				n, err := c.fn(src, buf[:dstLen])
				for i := dstLen; i < len(buf); i++ {
					if buf[i] != guardByte {
						t.Fatalf("%s %s dst=%d: wrote past dst", c.name, name, dstLen)
					}
				}
				if n < 0 || n > dstLen {
					t.Fatalf("%s %s dst=%d: n=%d out of range", c.name, name, dstLen, n)
				}
				if dstLen == bound && (err != nil || n == 0) {
					t.Fatalf("%s %s dst=bound: n=%d err=%v", c.name, name, n, err)
				}
				if err != nil || n == 0 {
					continue
				}
				dec := make([]byte, len(src))
				dn, err := UncompressBlock(buf[:n], dec, nil)
				if err != nil || !bytes.Equal(dec[:dn], src) {
					t.Fatalf("%s %s dst=%d: round trip failed: %v", c.name, name, dstLen, err)
				}
			}
		}
	}
}
