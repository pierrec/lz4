package lz4block_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/pierrec/lz4/v4/internal/lz4block"
)

// testdata/ccompat.golden and ccompathc.golden hold digests of the output of
// lz4 1.10.0's LZ4_compress_fast and LZ4_compress_HC for the cases below,
// showing that CompressorCCompat and CompressorCCompatHC produce the same
// output as the reference implementation. Each digest covers every block's
// compressed size (0 when it did not fit in dst) and data, so it also shows
// that the same blocks fit.
type ccompatGoldenSet struct {
	path       string
	maxInput   int
	blockSizes []int
	params     []int // accelerations or levels
}

// The cases of ccompat.golden: blocks either side of the switch between 16-
// and 32-bit table entries (64kiB+11 bytes), with accelerations 1 and 3.
var ccompatGolden = ccompatGoldenSet{
	path:       "../../testdata/ccompat.golden",
	maxInput:   4 << 20,
	blockSizes: []int{4 << 10, 64<<10 + 10, 64<<10 + 11, 4 << 20},
	params:     []int{1, 3},
}

// The cases of ccompathc.golden: blocks within the 64kiB window and beyond
// it, at every level. Inputs are shorter than for ccompat.golden, as the
// highest levels are slow.
var ccompatHCGolden = ccompatGoldenSet{
	path:       "../../testdata/ccompathc.golden",
	maxInput:   512 << 10,
	blockSizes: []int{4 << 10, 64 << 10, 512 << 10},
	params:     []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
}

// forCases calls f for each golden case: every block of each input, up to
// maxInput bytes, split at each block size, into a dst of CompressBlockBound
// and of the block's own length, with each parameter.
func (g ccompatGoldenSet) forCases(tb testing.TB, f func(name string, blocks [][]byte, dstLen func(int) int, param int)) {
	tb.Helper()
	inputs := []struct {
		name string
		data []byte
	}{{"zeros", make([]byte, min(1<<20, g.maxInput))}}
	for _, name := range []string{"pg1661.txt", "Mark.Twain-Tom.Sawyer.txt", "vmlinux_LZ4_19377", "pg_control.tar", "e.txt", "random.data"} {
		b, err := readGz("../../testdata/" + name + ".gz")
		if err != nil {
			tb.Fatal(err)
		}
		if len(b) > g.maxInput {
			b = b[:g.maxInput]
		}
		inputs = append(inputs, struct {
			name string
			data []byte
		}{name, b})
	}
	dsts := []struct {
		name string
		len  func(int) int
	}{
		{"bound", lz4block.CompressBlockBound},
		{"src", func(n int) int { return n }},
	}
	for _, in := range inputs {
		for _, bs := range g.blockSizes {
			var blocks [][]byte
			for b := in.data; len(b) > 0; {
				n := min(bs, len(b))
				blocks = append(blocks, b[:n])
				b = b[n:]
			}
			for _, d := range dsts {
				for _, p := range g.params {
					f(fmt.Sprintf("%s/%d/%s/%d", in.name, bs, d.name, p), blocks, d.len, p)
				}
			}
		}
	}
}

// ccompatGoldenDigest returns the total compressed size and the digest of the
// sizes and data of compressing blocks with compress.
func ccompatGoldenDigest(blocks [][]byte, dstLen func(int) int, compress func(src, dst []byte) int) (int, string) {
	h := sha256.New()
	total := 0
	for _, blk := range blocks {
		dst := make([]byte, dstLen(len(blk)))
		n := compress(blk, dst)
		total += n
		_ = binary.Write(h, binary.LittleEndian, uint32(n))
		h.Write(dst[:n])
	}
	return total, fmt.Sprintf("%x", h.Sum(nil))
}

// check compares the digests of compress's output with the golden file.
func (g ccompatGoldenSet) check(t *testing.T, compress func(src, dst []byte, param int) int) {
	f, err := os.Open(g.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if fs := strings.SplitN(line, " ", 2); len(fs) == 2 {
			want[fs[0]] = fs[1]
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	n := 0
	g.forCases(t, func(name string, blocks [][]byte, dstLen func(int) int, param int) {
		total, digest := ccompatGoldenDigest(blocks, dstLen, func(src, dst []byte) int {
			return compress(src, dst, param)
		})
		if got := fmt.Sprintf("%d %s", total, digest); got != want[name] {
			t.Errorf("%s: got %s, want %s from lz4 1.10.0", name, got, want[name])
		}
		n++
	})
	if n != len(want) {
		t.Errorf("checked %d cases, %s has %d", n, g.path, len(want))
	}
}

func TestCompressorCCompatGolden(t *testing.T) {
	var c lz4block.CompressorCCompat
	ccompatGolden.check(t, func(src, dst []byte, accel int) int {
		n, _ := c.CompressBlock(src, dst, accel)
		return n
	})
}

func TestCompressorCCompatHCGolden(t *testing.T) {
	var c lz4block.CompressorCCompatHC
	ccompatHCGolden.check(t, func(src, dst []byte, level int) int {
		n, _ := c.CompressBlock(src, dst, level)
		return n
	})
}
