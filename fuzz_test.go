package lz4_test

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/pierrec/lz4/v4"
)

// The fuzzers for frames; those for blocks are in internal/lz4block. See
// fuzz/README.md. Run these with, for example:
//
//	go test -run '^$' -fuzz '^FuzzReader$' -fuzztime 10m .
//
// Crashers are saved under testdata/fuzz and then run as part of go test.

// FuzzReader decodes arbitrary input every way the Reader can, which must not
// panic or hang and must agree.
func FuzzReader(f *testing.F) {
	for _, tc := range malformedCases(f) {
		f.Add(tc.in)
	}
	for _, opts := range [][]lz4.Option{
		nil,
		{lz4.BlockChecksumOption(true), lz4.ChecksumOption(true), lz4.SizeOption(1000)},
		{lz4.BlockSizeOption(lz4.Block64Kb), lz4.ChecksumOption(false)},
		{lz4.LegacyOption(true)},
	} {
		f.Add(encode(f, testData(1000, true, 1), opts...))
	}
	f.Add(encode(f, testData(3*int(lz4.Block64Kb)/2, false, 2), lz4.BlockSizeOption(lz4.Block64Kb)))
	// Small golden files from the reference implementation.
	golden, _ := filepath.Glob("testdata/*.lz4")
	for _, name := range golden {
		if b, err := os.ReadFile(name); err == nil && len(b) < 20000 {
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		out, _ := decodeAllModes(t, in)
		// A 4Mb block compresses to about 16kB, so the output is bounded.
		if len(out) > 300*len(in)+int(lz4.Block4Mb) {
			t.Fatalf("%d bytes of output from %d bytes of input", len(out), len(in))
		}
		_, _ = lz4.ValidFrameHeader(in)
	})
}

// FuzzFrameRoundTrip compresses and decompresses arbitrary data with options
// and write sizes picked by the fuzzer.
func FuzzFrameRoundTrip(f *testing.F) {
	for i, n := range []int{0, 1, 17, 1000, int(lz4.Block64Kb) + 1} {
		f.Add(testData(n, true, int64(i)), uint16(i*0x1111), uint16(n/3))
		f.Add(testData(n, false, int64(i)), uint16(0xFFFF-i), uint16(0))
	}
	// Frames of several blocks, raw and compressed, fed through ReadFrom.
	for i, bs := range []uint16{0, 1, 3} {
		f.Add(testData(1000, i%2 == 0, int64(i)), 1<<14|uint16(4+i)<<11|bs, uint16(0))
		f.Add(testData(100, true, int64(i)), 1<<14|1<<6|7<<11|bs, uint16(0))
		// The same frames written and flushed in chunks.
		f.Add(testData(1000, true, int64(i)), 1<<15|1<<6|uint16(2+i)<<11|bs, uint16(300))
	}
	// Inputs go-fuzz collected for the frame round trip.
	entries, err := os.ReadDir("fuzz/corpus")
	if err != nil {
		f.Fatal(err)
	}
	for i, e := range entries {
		b, err := os.ReadFile(filepath.Join("fuzz/corpus", e.Name()))
		if err != nil {
			f.Fatal(err)
		}
		if len(b) <= 64<<10 {
			f.Add(b, uint16(i*0x0101), uint16(len(b)/3))
		}
	}
	blockSizes := []lz4.BlockSize{lz4.Block64Kb, lz4.Block256Kb, lz4.Block1Mb, lz4.Block4Mb}
	levels := []lz4.CompressionLevel{lz4.Fast, lz4.Level1, lz4.Level5, lz4.Level9}
	f.Fuzz(func(t *testing.T, data []byte, flags uint16, chunk uint16) {
		legacy := flags&(1<<9) != 0
		level := levels[flags>>2&3]
		if flags&(1<<10) != 0 {
			level = lz4.CCompatFast
		}
		opts := []lz4.Option{
			lz4.BlockSizeOption(blockSizes[flags&3]),
			lz4.CompressionLevelOption(level),
			lz4.BlockChecksumOption(flags&(1<<4) != 0),
			lz4.ChecksumOption(flags&(1<<5) != 0),
			lz4.ConcurrencyOption(1 + int(flags>>6&1)*3),
			lz4.LegacyOption(legacy),
		}
		// OnBlockDone must report each block once, by Close.
		var reports, reported atomic.Int64
		opts = append(opts, lz4.OnBlockDoneOption(func(size int) {
			reports.Add(1)
			reported.Add(int64(size))
		}))
		if k := int(flags >> 11 & 7); k > 0 {
			data = growData(data, k*int(blockSizes[flags&3]), 2<<20)
		}
		// Write in chunks of step bytes (all at once if 0), but few of them
		// for large inputs: a flush per tiny chunk of megabytes hangs.
		step := int(chunk)
		if step > 0 && len(data) > 64<<10 {
			step = max(step, len(data)/16)
		}
		if flags&(1<<7) != 0 {
			opts = append(opts, lz4.SizeOption(uint64(len(data))))
		}
		var frame bytes.Buffer
		if flags&(1<<8) != 0 && !legacy {
			// CompressingReader supports neither concurrency nor legacy frames.
			zc := lz4.NewCompressingReader(io.NopCloser(bytes.NewReader(data)))
			// All but the ConcurrencyOption and LegacyOption at opts[4:6].
			if err := zc.Apply(append(opts[:4:4], opts[6:]...)...); err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(&frame, zc); err != nil {
				t.Fatal(err)
			}
		} else {
			zw := lz4.NewWriter(&frame)
			if err := zw.Apply(opts...); err != nil {
				t.Fatal(err)
			}
			if flags&(1<<14) != 0 {
				// io.Copy from a reader without WriteTo, as from a file or pipe.
				if _, err := zw.ReadFrom(bytes.NewReader(data)); err != nil {
					t.Fatal(err)
				}
			}
			for rest := data; len(rest) > 0 && flags&(1<<14) == 0; {
				n := len(rest)
				if step > 0 && step < n {
					n = step
				}
				if _, err := zw.Write(rest[:n]); err != nil {
					t.Fatal(err)
				}
				rest = rest[n:]
				if flags&(1<<15) != 0 {
					if err := zw.Flush(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
		}
		n, sum := frameBlocks(t, frame.Bytes(), legacy)
		if r, s := reports.Load(), reported.Load(); r != n || s != sum {
			t.Fatalf("OnBlockDone reported %d blocks of %d bytes; the frame has %d of %d", r, s, n, sum)
		}
		out, err := decodeAllModes(t, frame.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, data) {
			t.Fatalf("round trip mismatch: got %d bytes, want %d", len(out), len(data))
		}
		if r := reports.Load(); r != n {
			t.Fatalf("OnBlockDone reported %d blocks after Close; the frame has %d", r-n, n)
		}
	})
}

// frameBlocks returns the number of data blocks in frame and the sum of
// their sizes, as written in their headers.
func frameBlocks(t *testing.T, frame []byte, legacy bool) (n, sum int64) {
	t.Helper()
	if len(frame) < 4 {
		t.Fatalf("frame of %d bytes", len(frame))
	}
	b := frame[4:]
	blockChecksum := false
	if !legacy {
		if len(b) < 3 {
			t.Fatalf("truncated frame descriptor: %x", b)
		}
		flg := b[0]
		blockChecksum = flg&(1<<4) != 0
		b = b[2:] // FLG and BD
		if flg&(1<<3) != 0 {
			b = b[8:] // content size
		}
		if flg&1 != 0 {
			b = b[4:] // dictionary ID
		}
		b = b[1:] // header checksum
	}
	for len(b) >= 4 {
		size := int64(binary.LittleEndian.Uint32(b) &^ (1 << 31))
		b = b[4:]
		if size == 0 && !legacy {
			return n, sum // end mark
		}
		if blockChecksum {
			size += 4
		}
		if size > int64(len(b)) {
			t.Fatalf("block of %d bytes overruns the frame", size)
		}
		b = b[size:]
		if blockChecksum {
			size -= 4
		}
		n++
		sum += size
	}
	if !legacy || len(b) > 0 {
		t.Fatalf("frame ends without an end mark, %d bytes left", len(b))
	}
	return n, sum
}

// growData returns seed grown to n bytes (at most limit, and never shorter
// than seed): stretches of random bytes alternate with copies of seed, so
// that a frame of it holds both stored and compressed blocks. The growth is
// a function of seed, so a crasher reproduces.
func growData(seed []byte, n, limit int) []byte {
	n = max(min(n, limit), len(seed))
	h := fnv.New64a()
	h.Write(seed)
	rnd := rand.New(rand.NewSource(int64(h.Sum64())))
	out := make([]byte, len(seed), n)
	copy(out, seed)
	for len(out) < n {
		l := min(1+rnd.Intn(96<<10), n-len(out))
		if len(seed) == 0 || rnd.Intn(2) == 0 {
			start := len(out)
			out = out[:start+l]
			rnd.Read(out[start:])
			continue
		}
		for l > 0 {
			c := min(l, len(seed))
			out = append(out, seed[:c]...)
			l -= c
		}
	}
	return out
}
