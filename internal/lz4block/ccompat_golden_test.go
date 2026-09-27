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

// testdata/ccompat.golden holds digests of the output of lz4 1.10.0's
// LZ4_compress_fast for the cases below, showing that CompressorCCompat
// produces the same output as the reference implementation. Each digest
// covers every block's compressed size (0 when it did not fit in dst) and
// data, so it also shows that the same blocks fit.
const ccompatGoldenPath = "../../testdata/ccompat.golden"

// forCCompatGoldenCases calls f for each golden case: every block of each
// input, split at sizes either side of the switch between 16- and 32-bit
// table entries (64kiB+11 bytes), into a dst of CompressBlockBound and of
// the block's own length, with accelerations 1 and 3.
func forCCompatGoldenCases(tb testing.TB, f func(name string, blocks [][]byte, dstLen func(int) int, accel int)) {
	tb.Helper()
	inputs := []struct {
		name string
		data []byte
	}{{"zeros", make([]byte, 1<<20)}}
	for _, name := range []string{"pg1661.txt", "Mark.Twain-Tom.Sawyer.txt", "vmlinux_LZ4_19377", "pg_control.tar", "e.txt", "random.data"} {
		b, err := readGz("../../testdata/" + name + ".gz")
		if err != nil {
			tb.Fatal(err)
		}
		if len(b) > 4<<20 {
			b = b[:4<<20]
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
		for _, bs := range []int{4 << 10, 64<<10 + 10, 64<<10 + 11, 4 << 20} {
			var blocks [][]byte
			for b := in.data; len(b) > 0; {
				n := bs
				if n > len(b) {
					n = len(b)
				}
				blocks = append(blocks, b[:n])
				b = b[n:]
			}
			for _, d := range dsts {
				for _, accel := range []int{1, 3} {
					f(fmt.Sprintf("%s/%d/%s/%d", in.name, bs, d.name, accel), blocks, d.len, accel)
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

func TestCompressorCCompatGolden(t *testing.T) {
	f, err := os.Open(ccompatGoldenPath)
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
	var c lz4block.CompressorCCompat
	n := 0
	forCCompatGoldenCases(t, func(name string, blocks [][]byte, dstLen func(int) int, accel int) {
		total, digest := ccompatGoldenDigest(blocks, dstLen, func(src, dst []byte) int {
			n, _ := c.CompressBlock(src, dst, accel)
			return n
		})
		if got := fmt.Sprintf("%d %s", total, digest); got != want[name] {
			t.Errorf("%s: got %s, want %s from lz4 1.10.0", name, got, want[name])
		}
		n++
	})
	if n != len(want) {
		t.Errorf("checked %d cases, %s has %d", n, ccompatGoldenPath, len(want))
	}
}
