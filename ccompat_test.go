package lz4_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/pierrec/lz4/v4"
)

func TestCompressorCCompat(t *testing.T) {
	text, err := os.ReadFile("fuzz/corpus/pg1661.txt")
	if err != nil {
		t.Fatal(err)
	}
	var fast lz4.Compressor
	fastBuf := make([]byte, lz4.CompressBlockBound(len(text)))
	fastN, err := fast.CompressBlock(text, fastBuf)
	if err != nil {
		t.Fatal(err)
	}
	for _, accel := range []int{0, 1, 4} {
		c := lz4.CompressorCCompat{Acceleration: accel}
		buf := make([]byte, lz4.CompressBlockBound(len(text)))
		n, err := c.CompressBlock(text, buf)
		if err != nil || n == 0 {
			t.Fatalf("acceleration %d: n=%d err=%v", accel, n, err)
		}
		out := make([]byte, len(text))
		m, err := lz4.UncompressBlock(buf[:n], out)
		if err != nil || !bytes.Equal(out[:m], text) {
			t.Fatalf("acceleration %d: round trip failed: %v", accel, err)
		}
		// Fewer match searches: bigger output than Compressor.
		if n <= fastN {
			t.Errorf("acceleration %d: %d bytes, not more than Compressor's %d", accel, n, fastN)
		}
	}
}

func TestWriterCCompatFast(t *testing.T) {
	text, err := os.ReadFile("fuzz/corpus/pg1661.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, opts := range [][]lz4.Option{
		{lz4.CompressionLevelOption(lz4.CCompatFast)},
		{lz4.CompressionLevelOption(lz4.CCompatFast), lz4.ConcurrencyOption(4), lz4.BlockSizeOption(lz4.Block64Kb)},
		{lz4.CompressionLevelOption(lz4.CCompatFast), lz4.LegacyOption(true)},
		{lz4.CompressionLevelOption(lz4.CCompatLevel2)},
		{lz4.CompressionLevelOption(lz4.CCompatLevel9), lz4.ConcurrencyOption(4), lz4.BlockSizeOption(lz4.Block64Kb)},
		{lz4.CompressionLevelOption(lz4.CCompatLevel12), lz4.LegacyOption(true)},
	} {
		var buf bytes.Buffer
		zw := lz4.NewWriter(&buf)
		if err := zw.Apply(opts...); err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write(text); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(lz4.NewReader(&buf))
		if err != nil || !bytes.Equal(out, text) {
			t.Fatalf("%v: round trip failed: %v", opts, err)
		}
	}

	zc := lz4.NewCompressingReader(io.NopCloser(bytes.NewReader(text)))
	if err := zc.Apply(lz4.CompressionLevelOption(lz4.CCompatFast)); err != nil {
		t.Fatal(err)
	}
	comp, err := io.ReadAll(zc)
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(lz4.NewReader(bytes.NewReader(comp)))
	if err != nil || !bytes.Equal(out, text) {
		t.Fatalf("CompressingReader: round trip failed: %v", err)
	}
	for _, tc := range []struct {
		level lz4.CompressionLevel
		want  string
	}{
		{lz4.CCompatFast, "CCompatFast"},
		{lz4.CCompatLevel2, "CCompatLevel2"},
		{lz4.CCompatLevel12, "CCompatLevel12"},
		{lz4.CompressionLevel(13), "CompressionLevel(13)"},
	} {
		if got := tc.level.String(); got != tc.want {
			t.Errorf("CompressionLevel(%d).String() = %q, want %q", uint32(tc.level), got, tc.want)
		}
	}
}

// CompressionLevelOption accepts the C levels, from 1 to 12, and no others
// below Level1.
func TestCompressionLevelOptionCCompat(t *testing.T) {
	for level := range lz4.Level1 {
		err := lz4.NewWriter(io.Discard).Apply(lz4.CompressionLevelOption(level))
		if valid := level <= lz4.CCompatLevel12; valid != (err == nil) {
			t.Errorf("CompressionLevelOption(%d): %v", level, err)
		}
		if err != nil && !errors.Is(err, lz4.ErrOptionInvalidCompressionLevel) {
			t.Errorf("CompressionLevelOption(%d): %v, want ErrOptionInvalidCompressionLevel", level, err)
		}
	}
}

// CompressorCCompatHC takes the C levels, with 0 as the default, and refuses
// the others, such as Level9, which would otherwise mean the slowest level.
func TestCompressorCCompatHCLevel(t *testing.T) {
	src := bytes.Repeat([]byte("hello, world. "), 100)
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	for _, tc := range []struct {
		level lz4.CompressionLevel
		valid bool
	}{
		{0, true},
		{lz4.CCompatFast, true},
		{lz4.CCompatLevel2, true},
		{lz4.CCompatLevel12, true},
		{lz4.CCompatLevel12 + 1, false},
		{lz4.Level1, false},
		{lz4.Level9, false},
	} {
		c := lz4.CompressorCCompatHC{Level: tc.level}
		n, err := c.CompressBlock(src, dst)
		if tc.valid != (err == nil) || tc.valid != (n > 0) {
			t.Errorf("Level %d: n=%d, err=%v", uint32(tc.level), n, err)
		}
		if err != nil && !errors.Is(err, lz4.ErrOptionInvalidCompressionLevel) {
			t.Errorf("Level %d: %v, want ErrOptionInvalidCompressionLevel", uint32(tc.level), err)
		}
	}
}

// A Writer at CCompatFast or a CCompatLevel writes a block that compresses to
// exactly its size raw, as the lz4 1.10.0 command does at levels 1, 9 and 12:
// the frame below is its output for this input with -BI.
func TestWriterCCompatExactSizeBlock(t *testing.T) {
	// A 4-byte match costs as much as it saves: 13 bytes for the first
	// sequence and 13 for the last literals.
	src := []byte("ABCDEFGHIJABCDklmnopqrstuv")
	want := []byte("\x04\x22\x4d\x18\x64\x40\xa7\x1a\x00\x00\x80" + string(src) + "\x00\x00\x00\x00\xc2\xa8\xd2\x6c")
	var c lz4.CompressorCCompat
	if n, _ := c.CompressBlock(src, make([]byte, len(src))); n != len(src) {
		t.Fatalf("the input compresses to %d bytes, want %d", n, len(src))
	}
	for _, level := range []lz4.CompressionLevel{lz4.CCompatFast, lz4.CCompatLevel2, lz4.CCompatLevel9, lz4.CCompatLevel12} {
		var buf bytes.Buffer
		zw := lz4.NewWriter(&buf)
		if err := zw.Apply(lz4.CompressionLevelOption(level), lz4.BlockSizeOption(lz4.Block64Kb)); err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write(src); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), want) {
			t.Errorf("%v: frame\n%x, want\n%x", level, buf.Bytes(), want)
		}
	}
}

// A Writer at CCompatLevelN writes the blocks CompressorCCompatHC writes at
// level N into one byte less than the block's size, raw where they do not
// fit, as the C library does.
func TestWriterCCompatHCBlocks(t *testing.T) {
	text, err := os.ReadFile("fuzz/corpus/pg1661.txt")
	if err != nil {
		t.Fatal(err)
	}
	text = text[:200<<10]
	for level := lz4.CCompatLevel2; level <= lz4.CCompatLevel12; level++ {
		var buf bytes.Buffer
		zw := lz4.NewWriter(&buf)
		if err := zw.Apply(lz4.CompressionLevelOption(level), lz4.BlockSizeOption(lz4.Block64Kb), lz4.ChecksumOption(false)); err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write(text); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		c := lz4.CompressorCCompatHC{Level: level}
		var want []byte
		for b := text; len(b) > 0; {
			chunk := b[:min(64<<10, len(b))]
			b = b[len(chunk):]
			dst := make([]byte, len(chunk)-1)
			n, _ := c.CompressBlock(chunk, dst)
			if n == 0 {
				want = binary.LittleEndian.AppendUint32(want, uint32(len(chunk))|1<<31)
				want = append(want, chunk...)
			} else {
				want = binary.LittleEndian.AppendUint32(want, uint32(n))
				want = append(want, dst[:n]...)
			}
		}
		// The frame: a 7-byte header, the blocks, and the end mark.
		got := buf.Bytes()
		if len(got) < 11 || !bytes.Equal(got[7:len(got)-4], want) {
			t.Errorf("%v: blocks differ from CompressorCCompatHC's", level)
		}
	}
}
