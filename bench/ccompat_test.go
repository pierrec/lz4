package bench

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"

	"github.com/pierrec/lz4/v4"
	"github.com/pierrec/lz4/v4/internal/benchdata"
)

// checkMatchesC compresses src into dstLen bytes with lz4.CompressorCCompat
// and with LZ4_compress_fast, and requires the same result: the same length
// (0 when the output does not fit) and the same bytes.
func checkMatchesC(t testing.TB, src []byte, dstLen, accel int) {
	t.Helper()
	c := lz4.CompressorCCompat{Acceleration: accel}
	checkSameAsC(t, fmt.Sprintf("accel=%d", accel), src, dstLen, c.CompressBlock,
		func(src, dst []byte) int { return CompressFast(src, dst, accel) })
}

// checkHCMatchesC is checkMatchesC for c and LZ4_compress_HC at c.Level.
func checkHCMatchesC(t testing.TB, c *lz4.CompressorCCompatHC, src []byte, dstLen int) {
	t.Helper()
	checkSameAsC(t, fmt.Sprintf("level=%d", c.Level), src, dstLen, c.CompressBlock,
		func(src, dst []byte) int { return CompressHC(src, dst, int(c.Level)) })
}

func checkSameAsC(t testing.TB, name string, src []byte, dstLen int, goCompress func(src, dst []byte) (int, error), cCompress func(src, dst []byte) int) {
	t.Helper()
	goDst := make([]byte, dstLen)
	cDst := make([]byte, dstLen)
	gn, err := goCompress(src, goDst)
	if err != nil {
		t.Fatalf("len(src)=%d dst=%d %s: %v", len(src), dstLen, name, err)
	}
	cn := cCompress(src, cDst)
	if gn != cn {
		t.Fatalf("len(src)=%d dst=%d %s: Go wrote %d bytes, C %d", len(src), dstLen, name, gn, cn)
	}
	if !bytes.Equal(goDst[:gn], cDst[:cn]) {
		i := 0
		for i < gn && goDst[i] == cDst[i] {
			i++
		}
		t.Fatalf("len(src)=%d dst=%d %s: output differs from C at byte %d of %d", len(src), dstLen, name, i, gn)
	}
}

// TestCCompatMatchesC checks that CompressorCCompat produces the same bytes as
// LZ4_compress_fast on the corpus, for accelerations inside and outside the
// range C clamps to, and for destinations of CompressBlockBound, exactly the
// compressed size, and one byte less (where the output no longer fits).
func TestCCompatMatchesC(t *testing.T) {
	t.Logf("liblz4 %s", Version())
	accels := []int{-5, 0, 1, 2, 3, 8, 17, 100, 65537, 1 << 20}
	for _, in := range benchCorpus(t) {
		for _, bs := range benchdata.BlockSizes {
			for _, blk := range benchdata.Blocks(in.Data, bs)[:1] {
				bound := lz4.CompressBlockBound(len(blk))
				for _, a := range accels {
					checkMatchesC(t, blk, bound, a)
					n := CompressFast(blk, make([]byte, bound), a)
					checkMatchesC(t, blk, n, a)
					checkMatchesC(t, blk, n-1, a)
				}
			}
		}
	}
}

// TestCCompatHCMatchesC checks that CompressorCCompatHC produces the same
// bytes as LZ4_compress_HC on the corpus at every level, including those C
// maps to its default and its maximum, for destinations of
// CompressBlockBound, exactly the compressed size, and one byte less. One
// CompressorCCompatHC compresses everything, so that its tables are reused.
func TestCCompatHCMatchesC(t *testing.T) {
	// Runs of random bytes between copies of earlier ones, where how far
	// level 2 skips ahead decides which matches it finds.
	rnd := rand.New(rand.NewSource(1))
	mixed := make([]byte, 1<<20)
	for i := 0; i < len(mixed); {
		n := min(256+rnd.Intn(4096), len(mixed)-i)
		if i == 0 || rnd.Intn(2) == 0 {
			rnd.Read(mixed[i : i+n])
		} else {
			copy(mixed[i:i+n], mixed[rnd.Intn(i):i])
		}
		i += n
	}
	inputs := append(benchCorpus(t), benchdata.Input{Name: "mixed", Data: mixed})
	var c lz4.CompressorCCompatHC
	for level := range 13 {
		c.Level = lz4.CompressionLevel(level)
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			for _, in := range inputs {
				for _, bs := range benchdata.BlockSizes {
					if testing.Short() && level >= 10 && bs > 64<<10 {
						continue
					}
					blk := benchdata.Blocks(in.Data, bs)[0]
					bound := lz4.CompressBlockBound(len(blk))
					checkHCMatchesC(t, &c, blk, bound)
					n := CompressHC(blk, make([]byte, bound), level)
					checkHCMatchesC(t, &c, blk, n)
					checkHCMatchesC(t, &c, blk, n-1)
				}
				// Destinations that end where each output check is
				// decided: small blocks, short of the output by up to
				// the bytes the checks reserve and some.
				for _, blk := range benchdata.Blocks(in.Data, 1<<10)[:8] {
					n := CompressHC(blk, make([]byte, lz4.CompressBlockBound(len(blk))), level)
					for d := max(n-24, 0); d < n; d++ {
						checkHCMatchesC(t, &c, blk, d)
					}
				}
			}
		})
	}
}

// TestFrameCCompatBlocksMatchC checks the frame path: every block a Writer
// writes at CCompatFast or a CCompatLevel holds exactly what
// LZ4_compress_fast or LZ4_compress_HC at that level produces for that chunk
// of input into a destination one byte smaller than the chunk, as
// LZ4F_makeBlock does, or the raw chunk where that does not fit, for block
// sizes that use each of C's fast hash tables, with and without concurrency.
func TestFrameCCompatBlocksMatchC(t *testing.T) {
	levels := []struct {
		level    lz4.CompressionLevel
		compress func(src, dst []byte) int
	}{
		{lz4.CCompatFast, func(src, dst []byte) int { return CompressFast(src, dst, 1) }},
		{lz4.CCompatLevel2, func(src, dst []byte) int { return CompressHC(src, dst, 2) }},
		{lz4.CCompatLevel9, func(src, dst []byte) int { return CompressHC(src, dst, 9) }},
		{lz4.CCompatLevel11, func(src, dst []byte) int { return CompressHC(src, dst, 11) }},
	}
	for _, lvl := range levels {
		for _, in := range benchCorpus(t) {
			data := in.Data
			if len(data) > 1<<20 {
				data = data[:1<<20]
			}
			for _, bs := range []lz4.BlockSize{lz4.Block64Kb, lz4.Block256Kb} {
				for _, conc := range []int{1, 4} {
					name := fmt.Sprintf("%v/%s/%v/c%d", lvl.level, in.Name, bs, conc)
					var frame bytes.Buffer
					zw := lz4.NewWriter(&frame)
					if err := zw.Apply(lz4.CompressionLevelOption(lvl.level), lz4.BlockSizeOption(bs),
						lz4.ChecksumOption(false), lz4.ConcurrencyOption(conc)); err != nil {
						t.Fatal(err)
					}
					if _, err := zw.Write(data); err != nil {
						t.Fatal(err)
					}
					if err := zw.Close(); err != nil {
						t.Fatal(err)
					}
					blocks := frameBlocks(t, frame.Bytes())
					chunks := benchdata.Blocks(data, int(bs))
					if len(blocks) != len(chunks) {
						t.Fatalf("%s: %d blocks, want %d", name, len(blocks), len(chunks))
					}
					for i, chunk := range chunks {
						cDst := make([]byte, len(chunk)-1)
						n := lvl.compress(chunk, cDst)
						b := blocks[i]
						switch {
						case n == 0 && (!b.raw || !bytes.Equal(b.data, chunk)):
							t.Fatalf("%s block %d: C does not fit, want the raw chunk", name, i)
						case n > 0 && (b.raw || !bytes.Equal(b.data, cDst[:n])):
							t.Fatalf("%s block %d: payload differs from C's", name, i)
						}
					}
				}
			}
		}
	}
}

type frameBlock struct {
	raw  bool
	data []byte
}

// frameBlocks splits an LZ4 frame without block checksums into its blocks.
func frameBlocks(t *testing.T, b []byte) []frameBlock {
	t.Helper()
	if len(b) < 7 || binary.LittleEndian.Uint32(b) != 0x184D2204 {
		t.Fatal("not an LZ4 frame")
	}
	flg := b[4]
	if flg&0x10 != 0 {
		t.Fatal("frameBlocks does not handle block checksums")
	}
	p := 6
	if flg&0x08 != 0 {
		p += 8 // content size
	}
	if flg&0x01 != 0 {
		p += 4 // dictionary ID
	}
	p++ // header checksum
	var blocks []frameBlock
	for {
		size := binary.LittleEndian.Uint32(b[p:])
		p += 4
		if size == 0 {
			return blocks
		}
		n := int(size &^ (1 << 31))
		blocks = append(blocks, frameBlock{raw: size>>31 == 1, data: b[p : p+n]})
		p += n
	}
}

// FuzzCCompatMatchesC checks CompressorCCompat against LZ4_compress_fast, and
// CompressorCCompatHC against LZ4_compress_HC, on arbitrary input,
// destination size and acceleration or level: a level of 0 selects the fast
// compressor, and the others HC at that level. It needs liblz4, so it runs
// outside CI; see README.md.
func FuzzCCompatMatchesC(f *testing.F) {
	f.Add([]byte(nil), uint32(0), int32(1), int8(0))
	f.Add([]byte("a"), uint32(1), int32(1), int8(0))
	f.Add(bytes.Repeat([]byte("a"), 13), uint32(3), int32(0), int8(0))
	f.Add(bytes.Repeat([]byte("ab"), 40000), uint32(1000), int32(2), int8(0)) // byU32 table
	f.Add(bytes.Repeat([]byte("abcd"), 300), uint32(100), int32(0), int8(9))  // repeated pattern
	f.Add([]byte(nil), uint32(100), int32(0), int8(12))                       // see CompressHC
	for _, in := range benchCorpus(f) {
		blk := in.Data
		if len(blk) > 96<<10 {
			blk = blk[:96<<10]
		}
		for _, level := range []int8{0, 2, 9, 12} {
			f.Add(blk, uint32(lz4.CompressBlockBound(len(blk))), int32(1), level)
			f.Add(blk[:len(blk)/8], uint32(len(blk)/16), int32(9), level)
		}
	}
	var hc lz4.CompressorCCompatHC
	f.Fuzz(func(t *testing.T, src []byte, dstLen uint32, accel int32, level int8) {
		if bound := lz4.CompressBlockBound(len(src)); int(dstLen) > bound+16 {
			dstLen = uint32(bound + 16)
		}
		if level == 0 {
			checkMatchesC(t, src, int(dstLen), int(accel))
			return
		}
		// C clamps levels to 1..12 and reads those below 1 as 9, as
		// CompressorCCompatHC reads 0; it refuses levels above 12.
		hc.Level = lz4.CompressionLevel(min(max(level, 0), 12))
		checkHCMatchesC(t, &hc, src, int(dstLen))
	})
}
