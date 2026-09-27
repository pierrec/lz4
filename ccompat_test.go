package lz4_test

import (
	"bytes"
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
	if lz4.CCompatFast.String() != "CCompatFast" {
		t.Errorf("CCompatFast.String() = %q", lz4.CCompatFast.String())
	}
}
