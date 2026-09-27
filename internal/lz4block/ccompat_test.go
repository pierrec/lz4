package lz4block

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

func ccompatInputs(t testing.TB) map[string][]byte {
	t.Helper()
	text, err := os.ReadFile("../../fuzz/corpus/pg1661.txt")
	if err != nil {
		t.Fatal(err)
	}
	rnd := rand.New(rand.NewSource(1))
	random := make([]byte, 1<<17)
	rnd.Read(random)
	in := map[string][]byte{
		"empty":  {},
		"text":   text,
		"random": random,
		"zeros":  make([]byte, 1<<18),
	}
	// Either side of the minimum compressible length and of the switch
	// between 16- and 32-bit table entries.
	for _, n := range []int{1, 12, 13, 14, ccompatU16Limit - 1, ccompatU16Limit, ccompatU16Limit + 1} {
		in[fmt.Sprint("text", n)] = text[:n]
		in[fmt.Sprint("zeros", n)] = make([]byte, n)
	}
	return in
}

// checkCCompat compresses src into a dst of dstLen bytes followed by guard
// bytes, and checks that nothing is written outside the output and that it
// decompresses back to src.
func checkCCompat(t testing.TB, c *CompressorCCompat, src []byte, dstLen, accel int) {
	t.Helper()
	const guard = 64
	buf := make([]byte, dstLen+guard)
	for i := range buf {
		buf[i] = 0xA5
	}
	n, err := c.CompressBlock(src, buf[:dstLen], accel)
	if err != nil || n < 0 || n > dstLen {
		t.Fatalf("len(src)=%d dst=%d accel=%d: n=%d err=%v", len(src), dstLen, accel, n, err)
	}
	bound := CompressBlockBound(len(src))
	if n == 0 {
		if dstLen >= bound {
			t.Fatalf("len(src)=%d accel=%d: failed with a dst of CompressBlockBound", len(src), accel)
		}
		for i := dstLen; i < len(buf); i++ {
			if buf[i] != 0xA5 {
				t.Fatalf("len(src)=%d dst=%d accel=%d: wrote past dst", len(src), dstLen, accel)
			}
		}
		return
	}
	// Literal copies overrun, but the rest of the output covers them.
	for i := n; i < len(buf); i++ {
		if buf[i] != 0xA5 {
			t.Fatalf("len(src)=%d dst=%d accel=%d: wrote %d bytes past the output", len(src), dstLen, accel, i-n+1)
		}
	}
	out := make([]byte, len(src))
	m, err := UncompressBlock(buf[:n], out, nil)
	if err != nil || !bytes.Equal(out[:m], src) {
		t.Fatalf("len(src)=%d dst=%d accel=%d: round trip failed: %v", len(src), dstLen, accel, err)
	}
}

func TestCompressorCCompat(t *testing.T) {
	var c CompressorCCompat
	for name, src := range ccompatInputs(t) {
		t.Run(name, func(t *testing.T) {
			bound := CompressBlockBound(len(src))
			for _, accel := range []int{-1, 0, 1, 2, 9, 1 << 20} {
				for _, d := range []int{bound, bound - 1, len(src), len(src) / 2, 16, 1, 0} {
					if d >= 0 {
						checkCCompat(t, &c, src, d, accel)
					}
				}
			}
		})
	}
}

// Reusing a CompressorCCompat, including across the two table layouts, must
// not change its output.
func TestCompressorCCompatReuse(t *testing.T) {
	var reused CompressorCCompat
	rnd := rand.New(rand.NewSource(2))
	text := ccompatInputs(t)["text"]
	for i := 0; i < 200; i++ {
		n := []int{100, 5000, ccompatU16Limit - 1, ccompatU16Limit, 300000}[rnd.Intn(5)]
		off := rnd.Intn(len(text) - n)
		src := text[off : off+n]
		var fresh CompressorCCompat
		want := make([]byte, CompressBlockBound(n))
		got := make([]byte, CompressBlockBound(n))
		wn, _ := fresh.CompressBlock(src, want, 1)
		gn, _ := reused.CompressBlock(src, got, 1)
		if !bytes.Equal(want[:wn], got[:gn]) {
			t.Fatalf("call %d, n=%d: output differs from a fresh CompressorCCompat", i, n)
		}
	}
}

// More acceleration must not compress better on text, and the default must
// compress text reasonably.
func TestCompressorCCompatAcceleration(t *testing.T) {
	var c CompressorCCompat
	text := ccompatInputs(t)["text"]
	dst := make([]byte, CompressBlockBound(len(text)))
	prev := 0
	for _, accel := range []int{1, 2, 4, 8, 16} {
		n, _ := c.CompressBlock(text, dst, accel)
		if n < prev {
			t.Errorf("acceleration %d: %d bytes, less than %d with less acceleration", accel, n, prev)
		}
		prev = n
	}
	if n, _ := c.CompressBlock(text, dst, 1); n > len(text)*7/10 {
		t.Errorf("text compresses to %d of %d bytes", n, len(text))
	}
}
