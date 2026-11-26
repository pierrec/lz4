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
	checkCompressor(t, fmt.Sprintf("accel=%d", accel), src, dstLen,
		func(src, dst []byte) (int, error) { return c.CompressBlock(src, dst, accel) })
}

// checkCCompatHC is checkCCompat for CompressorCCompatHC.
func checkCCompatHC(t testing.TB, c *CompressorCCompatHC, src []byte, dstLen, level int) {
	t.Helper()
	checkCompressor(t, fmt.Sprintf("level=%d", level), src, dstLen,
		func(src, dst []byte) (int, error) { return c.CompressBlock(src, dst, level) })
}

func checkCompressor(t testing.TB, name string, src []byte, dstLen int, compress func(src, dst []byte) (int, error)) {
	t.Helper()
	const guard = 64
	buf := make([]byte, dstLen+guard)
	for i := range buf {
		buf[i] = 0xA5
	}
	n, err := compress(src, buf[:dstLen])
	if err != nil || n < 0 || n > dstLen {
		t.Fatalf("len(src)=%d dst=%d %s: n=%d err=%v", len(src), dstLen, name, n, err)
	}
	bound := CompressBlockBound(len(src))
	if n == 0 {
		if dstLen >= bound {
			t.Fatalf("len(src)=%d %s: failed with a dst of CompressBlockBound", len(src), name)
		}
		for i := dstLen; i < len(buf); i++ {
			if buf[i] != 0xA5 {
				t.Fatalf("len(src)=%d dst=%d %s: wrote past dst", len(src), dstLen, name)
			}
		}
		return
	}
	// Literal copies overrun, but the rest of the output covers them.
	for i := n; i < len(buf); i++ {
		if buf[i] != 0xA5 {
			t.Fatalf("len(src)=%d dst=%d %s: wrote %d bytes past the output", len(src), dstLen, name, i-n+1)
		}
	}
	out := make([]byte, len(src))
	m, err := UncompressBlock(buf[:n], out, nil)
	if err != nil || !bytes.Equal(out[:m], src) {
		t.Fatalf("len(src)=%d dst=%d %s: round trip failed: %v", len(src), dstLen, name, err)
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
	for i := range 200 {
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

func TestCompressorCCompatHC(t *testing.T) {
	var c CompressorCCompatHC
	for name, src := range ccompatInputs(t) {
		// Longer than the window, but short enough for the slow levels.
		src = src[:min(len(src), 1<<17)]
		t.Run(name, func(t *testing.T) {
			bound := CompressBlockBound(len(src))
			for _, level := range []int{-1, 0, 1, 2, 3, 9, 10, 12, 13} {
				for _, d := range []int{bound, bound - 1, len(src), len(src) / 2, 16, 1, 0} {
					if d >= 0 {
						checkCCompatHC(t, &c, src, d, level)
					}
				}
			}
		})
	}
}

// Reusing a CompressorCCompatHC, across levels and past the point where it
// clears its tables, must not change its output.
func TestCompressorCCompatHCReuse(t *testing.T) {
	var reused CompressorCCompatHC
	rnd := rand.New(rand.NewSource(2))
	text := ccompatInputs(t)["text"]
	for i := range 60 {
		if i == 30 {
			// The next calls clear the tables.
			reused.next = hcIndexReset - 100000
		}
		n := []int{100, 5000, 1 << 16, 1<<16 + 1, 150000}[rnd.Intn(5)]
		level := 2 + rnd.Intn(11)
		off := rnd.Intn(len(text) - n)
		src := text[off : off+n]
		var fresh CompressorCCompatHC
		want := make([]byte, CompressBlockBound(n))
		got := make([]byte, CompressBlockBound(n))
		wn, _ := fresh.CompressBlock(src, want, level)
		gn, _ := reused.CompressBlock(src, got, level)
		if !bytes.Equal(want[:wn], got[:gn]) {
			t.Fatalf("call %d, n=%d level=%d: output differs from a fresh CompressorCCompatHC", i, n, level)
		}
	}
	if reused.next > hcIndexReset {
		t.Fatalf("the tables were not cleared: next index %d", reused.next)
	}
}

// Higher levels must not compress text less, from level 3, and must
// compress it more than CompressorHC from level 9.
func TestCompressorCCompatHCLevels(t *testing.T) {
	var c CompressorCCompatHC
	text := ccompatInputs(t)["text"]
	dst := make([]byte, CompressBlockBound(len(text)))
	prev := len(dst)
	for level := 3; level <= 12; level++ {
		n, _ := c.CompressBlock(text, dst, level)
		if n > prev {
			t.Errorf("level %d: %d bytes, more than %d at a lower level", level, n, prev)
		}
		prev = n
	}
	n9, _ := c.CompressBlock(text, dst, 9)
	hc, _ := CompressBlockHC(text, dst, 1<<17)
	if n9 >= hc {
		t.Errorf("level 9: %d bytes, CompressorHC: %d", n9, hc)
	}
}
