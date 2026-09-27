package lz4_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"testing"

	"github.com/pierrec/lz4/v4"
	"github.com/pierrec/lz4/v4/internal/xxh32"
)

// The tests in this file check that the frame reader handles boundary
// conditions and hostile input: it must never panic or hang, must report
// truncated or corrupted frames as errors, and every way of reading a frame
// must give the same result.

// decodeMode is one way of reading a frame.
type decodeMode struct {
	name    string
	conc    int
	bufSize int // 0 means WriteTo
}

var decodeModes = []decodeMode{
	{"Read/small", 1, 7},
	{"Read/direct", 1, 4<<20 + 1}, // at least a block: decompresses straight into the caller's buffer
	{"Read/conc", 4, 7},
	{"WriteTo", 1, 0},
	{"WriteTo/conc", 4, 0},
}

var errNoProgress = errors.New("Read made no progress")

// readBufs recycles the read buffers: a fresh 4MB one per decode dominates
// the run time, particularly with the race detector.
var readBufs sync.Pool

func decodeWith(m decodeMode, in []byte) ([]byte, error) {
	zr := lz4.NewReader(bytes.NewReader(in))
	if err := zr.Apply(lz4.ConcurrencyOption(m.conc)); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if m.bufSize == 0 {
		_, err := zr.WriteTo(&out)
		return out.Bytes(), err
	}
	bp, _ := readBufs.Get().(*[]byte)
	if bp == nil || len(*bp) < m.bufSize {
		b := make([]byte, 4<<20+1)
		bp = &b
	}
	defer readBufs.Put(bp)
	buf := (*bp)[:m.bufSize]
	for stalls := 0; stalls < 100; {
		n, err := zr.Read(buf)
		out.Write(buf[:n])
		if err == io.EOF {
			return out.Bytes(), nil
		}
		if err != nil {
			return out.Bytes(), err
		}
		if n == 0 {
			stalls++
		} else {
			stalls = 0
		}
	}
	return out.Bytes(), errNoProgress
}

// decodeAllModes reads in every way and checks that they agree on success,
// and on output when successful. It returns the result of the first mode.
func decodeAllModes(t testing.TB, in []byte) ([]byte, error) {
	t.Helper()
	var first []byte
	var firstErr error
	for i, m := range decodeModes {
		out, err := decodeWith(m, in)
		if errors.Is(err, errNoProgress) {
			t.Fatalf("%s: %v", m.name, err)
		}
		if i == 0 {
			first, firstErr = out, err
			continue
		}
		if (err == nil) != (firstErr == nil) {
			t.Fatalf("%s: err=%v, but %s: err=%v", m.name, err, decodeModes[0].name, firstErr)
		}
		if err == nil && !bytes.Equal(out, first) {
			t.Fatalf("%s: output differs from %s (%d vs %d bytes)", m.name, decodeModes[0].name, len(out), len(first))
		}
	}
	return first, firstErr
}

func encode(t testing.TB, data []byte, opts ...lz4.Option) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := lz4.NewWriter(&buf)
	if err := zw.Apply(opts...); err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testData returns n bytes that are compressible, or not.
func testData(n int, compressible bool, seed int64) []byte {
	rnd := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	if !compressible {
		rnd.Read(b)
		return b
	}
	words := []string{"lz4 ", "frame ", "block ", "boundary ", "\n", "0123456789", "honeycomb "}
	for i := 0; i < n; {
		i += copy(b[i:], words[rnd.Intn(len(words))])
	}
	return b
}

func TestFrameRoundTripBoundaries(t *testing.T) {
	blockSizes := []lz4.BlockSize{lz4.Block64Kb, lz4.Block256Kb, lz4.Block1Mb, lz4.Block4Mb}
	if !thorough() {
		blockSizes = blockSizes[:2]
	}
	for _, bs := range blockSizes {
		B := int(bs)
		for _, n := range []int{0, 1, 15, 16, 17, B - 1, B, B + 1, 2*B + 7} {
			for _, compressible := range []bool{true, false} {
				data := testData(n, compressible, int64(n))
				configs := map[string][]lz4.Option{
					"default":    nil,
					"checksums":  {lz4.BlockChecksumOption(true), lz4.ChecksumOption(true)},
					"nochecksum": {lz4.ChecksumOption(false)},
					"size":       {lz4.SizeOption(uint64(n))},
					"conc":       {lz4.ConcurrencyOption(4)},
					"legacy":     {lz4.LegacyOption(true)},
				}
				if bs == lz4.Block64Kb {
					configs["hc"] = []lz4.Option{lz4.CompressionLevelOption(lz4.Level1)}
					configs["hc/conc"] = []lz4.Option{lz4.CompressionLevelOption(lz4.Level1), lz4.ConcurrencyOption(4)}
				}
				for cname, opts := range configs {
					t.Run(fmt.Sprintf("%s/%d/compressible=%v/%s", bs, n, compressible, cname), func(t *testing.T) {
						frame := encode(t, data, append([]lz4.Option{lz4.BlockSizeOption(bs)}, opts...)...)
						out, err := decodeAllModes(t, frame)
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(out, data) {
							t.Fatalf("round trip mismatch: got %d bytes, want %d", len(out), len(data))
						}
					})
				}
			}
		}
	}
}

// thorough reports whether to run the slow exhaustive cases. The race
// detector runs are there for the concurrent paths, and are much slower.
func thorough() bool { return !testing.Short() && !raceEnabled }

// truncationCuts returns the prefix lengths to try: all of them for small
// frames, otherwise the start, the end, and a sample in between.
func truncationCuts(n int) []int {
	if n <= 256 || (n <= 2048 && thorough()) {
		cuts := make([]int, n)
		for i := range cuts {
			cuts[i] = i
		}
		return cuts
	}
	edge, stride := 64, 997
	switch {
	case n <= 2048:
		edge, stride = 16, 7
	case !thorough():
		edge, stride = 16, 4999
	}
	var cuts []int
	for i := 0; i < edge; i++ {
		cuts = append(cuts, i, n-1-i)
	}
	for i := edge; i < n-edge; i += stride {
		cuts = append(cuts, i)
	}
	return cuts
}

func TestFrameTruncated(t *testing.T) {
	small := testData(300, true, 1)
	multi := testData(2*int(lz4.Block64Kb)+100, true, 2)
	random := testData(2*int(lz4.Block64Kb)+100, false, 3)
	for _, tc := range []struct {
		name string
		data []byte
		opts []lz4.Option
	}{
		{"default", small, nil},
		{"empty", nil, nil},
		{"checksums", small, []lz4.Option{lz4.BlockChecksumOption(true), lz4.ChecksumOption(true), lz4.SizeOption(uint64(len(small)))}},
		{"nochecksum", small, []lz4.Option{lz4.ChecksumOption(false)}},
		{"multiblock", multi, []lz4.Option{lz4.BlockSizeOption(lz4.Block64Kb), lz4.BlockChecksumOption(true)}},
		{"raw", random, []lz4.Option{lz4.BlockSizeOption(lz4.Block64Kb)}},
		{"concatenated", small, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := encode(t, tc.data, append([]lz4.Option{lz4.BlockSizeOption(lz4.Block64Kb)}, tc.opts...)...)
			want := tc.data
			if tc.name == "concatenated" {
				frame = append(frame, frame...)
				want = append(append([]byte{}, want...), want...)
			}
			for _, cut := range truncationCuts(len(frame)) {
				out, err := decodeAllModes(t, frame[:cut])
				if !bytes.HasPrefix(want, out) {
					t.Fatalf("cut %d/%d: output is not a prefix of the data", cut, len(frame))
				}
				// An empty input is an empty stream, and a cut between
				// concatenated frames is indistinguishable from the end.
				if cut == 0 || (tc.name == "concatenated" && cut == len(frame)/2) {
					if err != nil {
						t.Fatalf("cut %d/%d: %v", cut, len(frame), err)
					}
					continue
				}
				if err == nil {
					t.Fatalf("cut %d/%d: truncated frame decoded without error (%d bytes)", cut, len(frame), len(out))
				}
			}
		})
	}
}

// Legacy frames have no end mark, so a cut between blocks cannot be detected,
// but a cut must still never panic or produce anything but a prefix.
func TestFrameTruncatedLegacy(t *testing.T) {
	data := testData(3*int(lz4.Block64Kb), true, 4)
	frame := encode(t, data, lz4.LegacyOption(true))
	for _, cut := range truncationCuts(len(frame)) {
		out, _ := decodeAllModes(t, frame[:cut])
		if !bytes.HasPrefix(data, out) {
			t.Fatalf("cut %d/%d: output is not a prefix of the data", cut, len(frame))
		}
	}
}

// With block and content checksums, any corrupted byte must be detected.
// Without them, corruption must at least be handled consistently.
func TestFrameCorrupted(t *testing.T) {
	small := testData(300, true, 5)
	multi := testData(2*int(lz4.Block64Kb)+100, true, 6)
	random := testData(int(lz4.Block64Kb)+100, false, 7)
	for _, tc := range []struct {
		name     string
		data     []byte
		opts     []lz4.Option
		detected bool
	}{
		{"checksums", small, []lz4.Option{lz4.BlockChecksumOption(true), lz4.ChecksumOption(true)}, true},
		{"checksums/size", small, []lz4.Option{lz4.BlockChecksumOption(true), lz4.ChecksumOption(true), lz4.SizeOption(uint64(len(small)))}, true},
		{"checksums/multiblock", multi, []lz4.Option{lz4.BlockSizeOption(lz4.Block64Kb), lz4.BlockChecksumOption(true), lz4.ChecksumOption(true)}, true},
		{"checksums/raw", random, []lz4.Option{lz4.BlockSizeOption(lz4.Block64Kb), lz4.BlockChecksumOption(true), lz4.ChecksumOption(true)}, true},
		{"nochecksum", small, []lz4.Option{lz4.ChecksumOption(false)}, false},
		{"legacy", small, []lz4.Option{lz4.LegacyOption(true)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := encode(t, tc.data, append([]lz4.Option{lz4.BlockSizeOption(lz4.Block64Kb)}, tc.opts...)...)
			bad := make([]byte, len(frame))
			masks := []byte{0x01, 0x80, 0xFF}
			if !thorough() {
				masks = masks[2:]
			}
			for _, pos := range truncationCuts(len(frame)) {
				for _, mask := range masks {
					copy(bad, frame)
					bad[pos] ^= mask
					out, err := decodeAllModes(t, bad)
					if tc.detected && err == nil {
						t.Fatalf("byte %d ^ %#x: corruption not detected", pos, mask)
					}
					if err == nil && len(out) > 2*len(tc.data)+int(lz4.Block4Mb) {
						t.Fatalf("byte %d ^ %#x: implausible output size %d", pos, mask, len(out))
					}
				}
			}
		})
	}
}

// Helpers to build frames by hand.

const (
	flgV1    = 0x40 // version 01
	flgIndep = 0x20
	flgBlkCk = 0x10
	flgSize  = 0x08
	flgCntCk = 0x04
	flgDict  = 0x01
	bd64K    = 4 << 4
)

func le32(x uint32) []byte { return binary.LittleEndian.AppendUint32(nil, x) }

func le64(x uint64) []byte { return binary.LittleEndian.AppendUint64(nil, x) }

func cat(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

var (
	magic       = le32(0x184D2204)
	magicLegacy = le32(0x184C2102)
	endMark     = le32(0)
)

func skippable(n byte, data []byte) []byte {
	return cat(le32(0x184D2A50+uint32(n)), le32(uint32(len(data))), data)
}

// desc returns a frame descriptor with a valid checksum.
func desc(flg, bd byte, extra ...byte) []byte {
	b := append([]byte{flg, bd}, extra...)
	return append(b, byte(xxh32.ChecksumZero(b)>>8))
}

func rawBlock(data []byte) []byte { return cat(le32(uint32(len(data))|1<<31), data) }

func compBlock(comp []byte) []byte { return cat(le32(uint32(len(comp))), comp) }

func xxh(b []byte) []byte { return le32(xxh32.ChecksumZero(b)) }

func compressBlock(t testing.TB, data []byte) []byte {
	t.Helper()
	comp := make([]byte, lz4.CompressBlockBound(len(data)))
	var c lz4.Compressor
	n, err := c.CompressBlock(data, comp)
	if err != nil || n == 0 {
		t.Fatalf("CompressBlock: n=%d err=%v", n, err)
	}
	return comp[:n]
}

type malformedCase struct {
	name    string
	in      []byte
	want    []byte // output, or for errors, what the output must be a prefix of
	wantErr error
}

func malformedCases(t testing.TB) []malformedCase {
	hello := []byte("hello, hello, hello, hello, hello, hello!")
	comp := compressBlock(t, hello)
	hello2 := append(append([]byte{}, hello...), hello...)
	F := byte(flgV1 | flgIndep)
	hdr := cat(magic, desc(F, bd64K))
	valid := cat(hdr, compBlock(comp), endMark)
	allCk := cat(magic, desc(F|flgBlkCk|flgCntCk, bd64K))
	max64K := testData(int(lz4.Block64Kb), false, 8)
	over64K := compressBlock(t, make([]byte, int(lz4.Block64Kb)+1))
	comp8 := compressBlock(t, []byte("01234567")) // literals only: 9 bytes
	comp7 := compressBlock(t, []byte("89abcde"))  // literals only: 8 bytes
	if len(comp8) != 9 || len(comp7) != 8 {
		t.Fatalf("literal blocks are %d and %d bytes", len(comp8), len(comp7))
	}
	withSize := func(n uint64) []byte { return cat(magic, desc(F|flgSize, bd64K, le64(n)...)) }

	cases := []malformedCase{
		{"empty", nil, nil, nil},
		{"valid/raw", cat(hdr, rawBlock(hello), endMark), hello, nil},
		{"valid/compressed", valid, hello, nil},
		{"valid/checksums", cat(allCk, compBlock(comp), xxh(comp), endMark, xxh(hello)), hello, nil},
		{"valid/dependent", cat(magic, desc(flgV1, bd64K), compBlock(comp), rawBlock(hello), endMark), hello2, nil},
		{"valid/emptyRawBlock", cat(hdr, rawBlock(nil), endMark), nil, nil},
		{"valid/emptyCompressedBlock", cat(hdr, compBlock([]byte{0}), endMark), nil, nil},
		{"valid/max64KRawBlock", cat(hdr, rawBlock(max64K), endMark), max64K, nil},
		{"contentSize/ok", cat(withSize(uint64(len(hello))), compBlock(comp), endMark), hello, nil},
		{"contentSize/zero", cat(withSize(0), endMark), nil, nil},
		{"contentSize/tooBig", cat(withSize(uint64(len(hello))+1), compBlock(comp), endMark), hello, lz4.ErrInvalidContentSize},
		{"contentSize/tooSmall", cat(withSize(uint64(len(hello))-1), compBlock(comp), endMark), hello, lz4.ErrInvalidContentSize},
		{"contentSize/huge", cat(withSize(1<<63), compBlock(comp), endMark), hello, lz4.ErrInvalidContentSize},
		{"contentSize/secondFrame", cat(valid, withSize(1), endMark), hello, lz4.ErrInvalidContentSize},

		{"magic/bad", le32(0x184D2205), nil, lz4.ErrInvalidFrame},
		{"magic/partial", magic[:2], nil, io.ErrUnexpectedEOF},
		{"magic/only", magic, nil, io.ErrUnexpectedEOF},
		{"descriptor/partial", cat(magic, []byte{F}), nil, io.ErrUnexpectedEOF},
		{"descriptor/noChecksum", cat(magic, []byte{F, bd64K}), nil, io.ErrUnexpectedEOF},
		{"descriptor/partialContentSize", cat(magic, []byte{F | flgSize, bd64K, 1, 2, 3}), nil, io.ErrUnexpectedEOF},
		{"descriptor/partialDictID", cat(magic, []byte{F | flgDict, bd64K, 1, 2}), nil, io.ErrUnexpectedEOF},
		{"descriptor/badChecksum", cat(magic, []byte{F, bd64K, desc(F, bd64K)[2] ^ 1}, endMark), nil, lz4.ErrInvalidHeaderChecksum},
		{"descriptor/version0", cat(magic, desc(flgIndep, bd64K), endMark), nil, lz4.ErrInvalidFrameDescriptor},
		{"descriptor/version2", cat(magic, desc(0x80|flgIndep, bd64K), endMark), nil, lz4.ErrInvalidFrameDescriptor},
		{"descriptor/version3", cat(magic, desc(0xC0|flgIndep, bd64K), endMark), nil, lz4.ErrInvalidFrameDescriptor},
		{"descriptor/reservedFLG", cat(magic, desc(F|0x02, bd64K), endMark), nil, lz4.ErrInvalidFrameDescriptor},
		{"descriptor/reservedBDLow", cat(magic, desc(F, bd64K|0x01), endMark), nil, lz4.ErrInvalidFrameDescriptor},
		{"descriptor/reservedBDMid", cat(magic, desc(F, bd64K|0x08), endMark), nil, lz4.ErrInvalidFrameDescriptor},
		{"descriptor/reservedBDHigh", cat(magic, desc(F, bd64K|0x80), endMark), nil, lz4.ErrInvalidFrameDescriptor},
		{"descriptor/dictID", cat(magic, desc(F|flgDict, bd64K, 1, 2, 3, 4), endMark), nil, lz4.ErrInvalidFrameDescriptor},
		{"descriptor/dictIDAndSize", cat(magic, desc(F|flgDict|flgSize, bd64K, cat(le64(0), le32(7))...), endMark), nil, lz4.ErrInvalidFrameDescriptor},

		{"block/tooBigRaw", cat(hdr, le32(uint32(lz4.Block64Kb)+1|1<<31)), nil, lz4.ErrOptionInvalidBlockSize},
		{"block/tooBigCompressed", cat(hdr, le32(uint32(lz4.Block64Kb)+1)), nil, lz4.ErrOptionInvalidBlockSize},
		{"block/maxSizeField", cat(hdr, le32(0xFFFFFFFF)), nil, lz4.ErrOptionInvalidBlockSize},
		{"block/decodesTooLarge", cat(hdr, compBlock(over64K), endMark), nil, lz4.ErrInvalidSourceShortBuffer},
		{"block/garbage", cat(hdr, compBlock([]byte{0xF0, 0xFF, 0xFF}), endMark), nil, lz4.ErrInvalidSourceShortBuffer},
		{"block/badChecksum", cat(allCk, compBlock(comp), le32(xxh32.ChecksumZero(comp)^1), endMark, xxh(hello)), nil, lz4.ErrInvalidBlockChecksum},
		{"block/partialSize", cat(hdr, []byte{1, 0}), nil, io.ErrUnexpectedEOF},
		{"block/partialData", cat(hdr, le32(uint32(len(comp))), comp[:len(comp)/2]), nil, io.ErrUnexpectedEOF},
		{"block/noData", cat(hdr, le32(uint32(len(comp)))), nil, io.ErrUnexpectedEOF},
		{"block/missingChecksum", cat(allCk, compBlock(comp)), nil, io.ErrUnexpectedEOF},
		{"block/partialChecksum", cat(allCk, compBlock(comp), []byte{1, 2}), nil, io.ErrUnexpectedEOF},

		{"end/missing", cat(hdr, compBlock(comp)), hello, io.ErrUnexpectedEOF},
		{"end/missingNoBlocks", hdr, nil, io.ErrUnexpectedEOF},
		{"end/partial", cat(hdr, compBlock(comp), []byte{0, 0}), hello, io.ErrUnexpectedEOF},
		{"end/missingContentChecksum", cat(allCk, compBlock(comp), xxh(comp), endMark), hello, io.ErrUnexpectedEOF},
		{"end/partialContentChecksum", cat(allCk, compBlock(comp), xxh(comp), endMark, []byte{1}), hello, io.ErrUnexpectedEOF},
		{"end/badContentChecksum", cat(allCk, compBlock(comp), xxh(comp), endMark, le32(xxh32.ChecksumZero(hello)^1)), hello, lz4.ErrInvalidFrameChecksum},

		{"skippable/thenFrame", cat(skippable(0, []byte("abc")), valid), hello, nil},
		{"skippable/afterFrame", cat(valid, skippable(3, nil)), hello, nil},
		{"skippable/between", cat(valid, skippable(7, []byte("x")), valid), hello2, nil},
		{"skippable/partialSize", cat(le32(0x184D2A50), []byte{1, 0}), nil, io.ErrUnexpectedEOF},
		{"skippable/partialData", cat(le32(0x184D2A50), le32(10), []byte("abc")), nil, io.ErrUnexpectedEOF},
		{"skippable/hugeSize", cat(le32(0x184D2A5F), le32(0xFFFFFFFF), []byte("abc")), nil, io.ErrUnexpectedEOF},

		{"concatenated", cat(valid, valid), hello2, nil},
		{"trailing/zeros", cat(valid, endMark), hello, lz4.ErrInvalidFrame},
		{"trailing/partialMagic", cat(valid, magic[:3]), hello, io.ErrUnexpectedEOF},

		{"legacy/empty", magicLegacy, nil, nil},
		{"legacy/block", cat(magicLegacy, compBlock(comp)), hello, nil},
		{"legacy/concatenated", cat(magicLegacy, compBlock(comp), magicLegacy, compBlock(comp)), hello2, nil},
		{"legacy/kernelSizeTrailer", cat(magicLegacy, compBlock(comp), le32(uint32(len(hello)))), hello, nil},
		// The second block's compressed size equals the size decoded so far,
		// which is what the Linux kernel size trailer looks like.
		{"legacy/blockSizeEqualsTotal", cat(magicLegacy, compBlock(comp8), compBlock(comp7)), []byte("0123456789abcde"), nil},
		{"legacy/sizeTrailerMismatch", cat(magicLegacy, compBlock(comp), le32(uint32(len(hello))+1)), hello, io.ErrUnexpectedEOF},
		{"legacy/partialSize", cat(magicLegacy, []byte{1, 0}), nil, io.ErrUnexpectedEOF},
		{"legacy/partialBlock", cat(magicLegacy, le32(uint32(len(comp))), comp[:3]), nil, io.ErrUnexpectedEOF},
		{"legacy/blockTooBig", cat(magicLegacy, le32(8<<20+1)), nil, lz4.ErrOptionInvalidBlockSize},
	}
	for i := byte(0); i < 16; i++ {
		cases = append(cases, malformedCase{fmt.Sprintf("skippable/only%d", i), skippable(i, []byte("skip me")), nil, nil})
	}
	for idx := byte(0); idx < 8; idx++ {
		c := malformedCase{fmt.Sprintf("blockSizeIndex%d", idx), cat(magic, desc(F, idx<<4), compBlock(comp), endMark), hello, nil}
		if idx < 4 {
			c.want, c.wantErr = nil, lz4.ErrOptionInvalidBlockSize
		}
		cases = append(cases, c)
	}
	return cases
}

func TestFrameMalformed(t *testing.T) {
	for _, tc := range malformedCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for _, m := range decodeModes {
				out, err := decodeWith(m, tc.in)
				if !bytes.HasPrefix(tc.want, out) {
					t.Errorf("%s: output %q is not a prefix of %q", m.name, out, tc.want)
				}
				switch {
				case tc.wantErr == nil && err != nil:
					t.Errorf("%s: unexpected error %v", m.name, err)
				case tc.wantErr == nil && !bytes.Equal(out, tc.want):
					t.Errorf("%s: got %q, want %q", m.name, out, tc.want)
				case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
					t.Errorf("%s: got error %v, want %v", m.name, err, tc.wantErr)
				}
			}
		})
	}
}

// Once a Reader fails, it must keep failing until Reset, and then work.
func TestReaderErrorIsSticky(t *testing.T) {
	valid := encode(t, []byte("some valid data"))
	for _, tc := range malformedCases(t) {
		if tc.wantErr == nil {
			continue
		}
		for _, conc := range []int{1, 4} {
			t.Run(fmt.Sprintf("%s/conc%d", tc.name, conc), func(t *testing.T) {
				zr := lz4.NewReader(bytes.NewReader(tc.in))
				if err := zr.Apply(lz4.ConcurrencyOption(conc)); err != nil {
					t.Fatal(err)
				}
				if _, err := io.ReadAll(zr); err == nil {
					t.Fatal("no error")
				}
				buf := make([]byte, 16)
				for i := 0; i < 3; i++ {
					if _, err := zr.Read(buf); err == nil || err == io.EOF {
						t.Fatalf("Read %d after failure: err=%v", i, err)
					}
				}
				if _, err := zr.WriteTo(io.Discard); err == nil {
					t.Fatal("WriteTo after failure: no error")
				}
				zr.Reset(bytes.NewReader(valid))
				out, err := io.ReadAll(zr)
				if err != nil || string(out) != "some valid data" {
					t.Fatalf("after Reset: %q, %v", out, err)
				}
			})
		}
	}
}

func TestOptionErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  lz4.Option
		want error
	}{
		{"blockSize0", lz4.BlockSizeOption(0), lz4.ErrOptionInvalidBlockSize},
		{"blockSize123", lz4.BlockSizeOption(123), lz4.ErrOptionInvalidBlockSize},
		{"level3", lz4.CompressionLevelOption(3), lz4.ErrOptionInvalidCompressionLevel},
		{"level1<<18", lz4.CompressionLevelOption(1 << 18), lz4.ErrOptionInvalidCompressionLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zw := lz4.NewWriter(io.Discard)
			if err := zw.Apply(tc.opt); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	// 8Mb blocks are only valid in legacy frames. Options can be applied in
	// any order, so the Writer checks when it starts.
	t.Run("blockSize8Mb", func(t *testing.T) {
		zw := lz4.NewWriter(io.Discard)
		if err := zw.Apply(lz4.BlockSizeOption(8 << 20)); err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write([]byte("x")); !errors.Is(err, lz4.ErrOptionInvalidBlockSize) {
			t.Fatalf("Write: got %v", err)
		}
		if err := zw.Close(); !errors.Is(err, lz4.ErrOptionInvalidBlockSize) {
			t.Fatalf("Close: got %v", err)
		}
		var buf bytes.Buffer
		zw = lz4.NewWriter(&buf)
		if err := zw.Apply(lz4.BlockSizeOption(8<<20), lz4.LegacyOption(true)); err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if out, err := decodeAllModes(t, buf.Bytes()); err != nil || string(out) != "x" {
			t.Fatalf("legacy: %q, %v", out, err)
		}
		zc := lz4.NewCompressingReader(io.NopCloser(bytes.NewReader(nil)))
		if err := zc.Apply(lz4.BlockSizeOption(8 << 20)); !errors.Is(err, lz4.ErrOptionInvalidBlockSize) {
			t.Fatalf("CompressingReader: got %v", err)
		}
	})
	t.Run("readerNotApplicable", func(t *testing.T) {
		zr := lz4.NewReader(bytes.NewReader(nil))
		if err := zr.Apply(lz4.BlockSizeOption(lz4.Block64Kb)); !errors.Is(err, lz4.ErrOptionNotApplicable) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("writerAfterWrite", func(t *testing.T) {
		zw := lz4.NewWriter(io.Discard)
		if _, err := zw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := zw.Apply(lz4.ChecksumOption(false)); !errors.Is(err, lz4.ErrOptionClosedOrError) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("readerAfterRead", func(t *testing.T) {
		zr := lz4.NewReader(bytes.NewReader(encode(t, []byte("abc"))))
		if _, err := zr.Read(make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		if err := zr.Apply(lz4.ConcurrencyOption(2)); !errors.Is(err, lz4.ErrOptionClosedOrError) {
			t.Fatalf("got %v", err)
		}
	})
}

// Zero length writes, and a Close with nothing written, must produce valid
// empty frames.
func TestWriterEmpty(t *testing.T) {
	for _, conc := range []int{1, 4} {
		for _, legacy := range []bool{false, true} {
			var buf bytes.Buffer
			zw := lz4.NewWriter(&buf)
			if err := zw.Apply(lz4.ConcurrencyOption(conc), lz4.LegacyOption(legacy)); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				if n, err := zw.Write(nil); n != 0 || err != nil {
					t.Fatalf("Write(nil) = %d, %v", n, err)
				}
			}
			if err := zw.Flush(); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			out, err := decodeAllModes(t, buf.Bytes())
			if err != nil || len(out) != 0 {
				t.Fatalf("conc=%d legacy=%v: %q, %v", conc, legacy, out, err)
			}
		}
	}
}

// A frame whose data differs from SizeOption cannot be read back, so writing
// one must fail too.
func TestWriterSizeOption(t *testing.T) {
	data := testData(100000, true, 9)
	withWriter := func(opts ...lz4.Option) func([]byte, uint64, bool) ([]byte, error) {
		return func(data []byte, size uint64, readFrom bool) ([]byte, error) {
			var buf bytes.Buffer
			zw := lz4.NewWriter(&buf)
			if err := zw.Apply(append(opts, lz4.SizeOption(size), lz4.BlockSizeOption(lz4.Block64Kb))...); err != nil {
				return nil, err
			}
			var err error
			if readFrom {
				_, err = zw.ReadFrom(bytes.NewReader(data))
			} else {
				_, err = zw.Write(data)
			}
			if err != nil {
				return nil, err
			}
			err = zw.Close()
			return buf.Bytes(), err
		}
	}
	writers := []struct {
		name     string
		write    func([]byte, uint64, bool) ([]byte, error)
		readFrom bool
	}{
		{"Write", withWriter(), false},
		{"Write/conc", withWriter(lz4.ConcurrencyOption(4)), false},
		{"ReadFrom", withWriter(), true},
		{"ReadFrom/conc", withWriter(lz4.ConcurrencyOption(4)), true},
		{"CompressingReader", func(data []byte, size uint64, _ bool) ([]byte, error) {
			zc := lz4.NewCompressingReader(io.NopCloser(bytes.NewReader(data)))
			if err := zc.Apply(lz4.SizeOption(size), lz4.BlockSizeOption(lz4.Block64Kb)); err != nil {
				return nil, err
			}
			return io.ReadAll(zc)
		}, false},
	}
	for _, w := range writers {
		for _, tc := range []struct {
			name    string
			n       int
			size    uint64
			wantErr error
		}{
			{"exact", len(data), uint64(len(data)), nil},
			{"exactOneByte", 1, 1, nil},
			{"short", len(data) - 1, uint64(len(data)), lz4.ErrInvalidContentSize},
			{"long", len(data), uint64(len(data)) - 1, lz4.ErrInvalidContentSize},
			{"empty", 0, 1, lz4.ErrInvalidContentSize},
			{"zeroMeansUnset", len(data), 0, nil},
		} {
			t.Run(w.name+"/"+tc.name, func(t *testing.T) {
				frame, err := w.write(data[:tc.n], tc.size, w.readFrom)
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got error %v, want %v", err, tc.wantErr)
				}
				if err != nil {
					return
				}
				out, err := decodeAllModes(t, frame)
				if err != nil || !bytes.Equal(out, data[:tc.n]) {
					t.Fatalf("read back %d bytes, err %v", len(out), err)
				}
			})
		}
	}
}
