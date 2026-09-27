package lz4_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
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
		opts := []lz4.Option{
			lz4.BlockSizeOption(blockSizes[flags&3]),
			lz4.CompressionLevelOption(levels[flags>>2&3]),
			lz4.BlockChecksumOption(flags&(1<<4) != 0),
			lz4.ChecksumOption(flags&(1<<5) != 0),
			lz4.ConcurrencyOption(1 + int(flags>>6&1)*3),
			lz4.LegacyOption(legacy),
		}
		if flags&(1<<7) != 0 {
			opts = append(opts, lz4.SizeOption(uint64(len(data))))
		}
		var frame bytes.Buffer
		if flags&(1<<8) != 0 && !legacy {
			// CompressingReader supports neither concurrency nor legacy frames.
			zc := lz4.NewCompressingReader(io.NopCloser(bytes.NewReader(data)))
			if err := zc.Apply(opts[:4]...); err != nil {
				t.Fatal(err)
			}
			if flags&(1<<7) != 0 {
				if err := zc.Apply(lz4.SizeOption(uint64(len(data)))); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := io.Copy(&frame, zc); err != nil {
				t.Fatal(err)
			}
		} else {
			zw := lz4.NewWriter(&frame)
			if err := zw.Apply(opts...); err != nil {
				t.Fatal(err)
			}
			for rest := data; len(rest) > 0; {
				n := len(rest)
				if chunk > 0 && int(chunk) < n {
					n = int(chunk)
				}
				if _, err := zw.Write(rest[:n]); err != nil {
					t.Fatal(err)
				}
				rest = rest[n:]
				if flags&(1<<10) != 0 {
					if err := zw.Flush(); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
		}
		out, err := decodeAllModes(t, frame.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, data) {
			t.Fatalf("round trip mismatch: got %d bytes, want %d", len(out), len(data))
		}
	})
}
