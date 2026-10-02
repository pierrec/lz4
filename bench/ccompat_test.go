package bench

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/pierrec/lz4/v4"
	"github.com/pierrec/lz4/v4/internal/benchdata"
)

// checkMatchesC compresses src into dstLen bytes with lz4.CompressorCCompat
// and with LZ4_compress_fast, and requires the same result: the same length
// (0 when the output does not fit) and the same bytes.
func checkMatchesC(t testing.TB, src []byte, dstLen, accel int) {
	t.Helper()
	goDst := make([]byte, dstLen)
	cDst := make([]byte, dstLen)
	c := lz4.CompressorCCompat{Acceleration: accel}
	gn, err := c.CompressBlock(src, goDst)
	if err != nil {
		t.Fatalf("len(src)=%d dst=%d accel=%d: %v", len(src), dstLen, accel, err)
	}
	cn := CompressFast(src, cDst, accel)
	if gn != cn {
		t.Fatalf("len(src)=%d dst=%d accel=%d: Go wrote %d bytes, C %d", len(src), dstLen, accel, gn, cn)
	}
	if !bytes.Equal(goDst[:gn], cDst[:cn]) {
		i := 0
		for i < gn && goDst[i] == cDst[i] {
			i++
		}
		t.Fatalf("len(src)=%d dst=%d accel=%d: output differs from C at byte %d of %d", len(src), dstLen, accel, i, gn)
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

// TestFrameCCompatBlocksMatchC checks the frame path: every block a Writer
// writes at CCompatFast holds exactly what LZ4_compress_fast produces for
// that chunk of input into a destination of the chunk's size, or the raw
// chunk where that does not fit, for block sizes that use each of C's
// hash tables, with and without concurrency.
func TestFrameCCompatBlocksMatchC(t *testing.T) {
	for _, in := range benchCorpus(t) {
		data := in.Data
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}
		for _, bs := range []lz4.BlockSize{lz4.Block64Kb, lz4.Block256Kb} {
			for _, conc := range []int{1, 4} {
				var frame bytes.Buffer
				zw := lz4.NewWriter(&frame)
				if err := zw.Apply(lz4.CompressionLevelOption(lz4.CCompatFast), lz4.BlockSizeOption(bs),
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
					t.Fatalf("%s/%v/c%d: %d blocks, want %d", in.Name, bs, conc, len(blocks), len(chunks))
				}
				for i, chunk := range chunks {
					cDst := make([]byte, len(chunk))
					n := CompressFast(chunk, cDst, 1)
					b := blocks[i]
					switch {
					case n == 0 && (!b.raw || !bytes.Equal(b.data, chunk)):
						t.Fatalf("%s/%v/c%d block %d: C does not fit, want the raw chunk", in.Name, bs, conc, i)
					case n > 0 && (b.raw || !bytes.Equal(b.data, cDst[:n])):
						t.Fatalf("%s/%v/c%d block %d: payload differs from LZ4_compress_fast", in.Name, bs, conc, i)
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

// FuzzCCompatMatchesC checks CompressorCCompat against LZ4_compress_fast on
// arbitrary input, destination size and acceleration. It needs liblz4, so it
// runs outside CI; see README.md.
func FuzzCCompatMatchesC(f *testing.F) {
	f.Add([]byte(nil), uint32(0), int32(1))
	f.Add([]byte("a"), uint32(1), int32(1))
	f.Add(bytes.Repeat([]byte("a"), 13), uint32(3), int32(0))
	f.Add(bytes.Repeat([]byte("ab"), 40000), uint32(1000), int32(2)) // byU32 table
	for _, in := range benchCorpus(f) {
		blk := in.Data
		if len(blk) > 96<<10 {
			blk = blk[:96<<10]
		}
		f.Add(blk, uint32(lz4.CompressBlockBound(len(blk))), int32(1))
		f.Add(blk[:len(blk)/8], uint32(len(blk)/16), int32(9))
	}
	f.Fuzz(func(t *testing.T, src []byte, dstLen uint32, accel int32) {
		if bound := lz4.CompressBlockBound(len(src)); int(dstLen) > bound+16 {
			dstLen = uint32(bound + 16)
		}
		checkMatchesC(t, src, int(dstLen), int(accel))
	})
}
