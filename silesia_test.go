package lz4_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"testing"

	"github.com/pierrec/lz4/v4"
)

// The Silesia corpus is too big to commit. testdata/fetch_silesia.sh fetches
// it as testdata/silesia.tar, the name klauspost/compress also uses, and CI
// runs it; the tests below skip without it. CI also writes C-CLI encodings
// of it next to it (see silesiaCFrames).
const silesiaPath = "testdata/silesia.tar"

func silesia(tb testing.TB) []byte {
	tb.Helper()
	b, err := os.ReadFile(silesiaPath)
	if errors.Is(err, fs.ErrNotExist) {
		tb.Skipf("%s is missing: run testdata/fetch_silesia.sh", silesiaPath)
	}
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func firstDiff(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// TestSilesiaFrames round-trips the corpus through Writer and Reader.
func TestSilesiaFrames(t *testing.T) {
	data := silesia(t)
	for _, tc := range []struct {
		name string
		slow bool
		opts []lz4.Option
	}{
		{"fast/4M", false, nil},
		{"fast/64K", false, []lz4.Option{lz4.BlockSizeOption(lz4.Block64Kb)}},
		{"fast/256K/checksums", false, []lz4.Option{lz4.BlockSizeOption(lz4.Block256Kb), lz4.BlockChecksumOption(true), lz4.ChecksumOption(true)}},
		{"fast/4M/concurrent", false, []lz4.Option{lz4.ConcurrencyOption(4)}},
		{"ccompat/1M", false, []lz4.Option{lz4.BlockSizeOption(lz4.Block1Mb), lz4.CompressionLevelOption(lz4.CCompatFast)}},
		{"level2/64K", false, []lz4.Option{lz4.BlockSizeOption(lz4.Block64Kb), lz4.CompressionLevelOption(lz4.Level2)}},
		{"level9/4M", true, []lz4.Option{lz4.CompressionLevelOption(lz4.Level9)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.slow && testing.Short() {
				t.Skip("slow")
			}
			comp := writeFrame(t, data, tc.opts...)
			got, err := io.ReadAll(lz4.NewReader(bytes.NewReader(comp)))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("round trip differs at byte %d of %d (got %d bytes)", firstDiff(got, data), len(data), len(got))
			}
			t.Logf("%d -> %d bytes (%.2f%%)", len(data), len(comp), 100*float64(len(comp))/float64(len(data)))
		})
	}
}

// TestSilesiaBlocks round-trips the corpus in 64 KiB blocks through each
// block compressor and UncompressBlock.
func TestSilesiaBlocks(t *testing.T) {
	data := silesia(t)
	for _, tc := range []struct {
		name     string
		compress func(src, dst []byte) (int, error)
	}{
		{"fast", new(lz4.Compressor).CompressBlock},
		{"ccompat", new(lz4.CompressorCCompat).CompressBlock},
		{"level2", (&lz4.CompressorHC{Level: lz4.Level2}).CompressBlock},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const bs = 64 << 10
			comp := make([]byte, lz4.CompressBlockBound(bs))
			dec := make([]byte, bs)
			total := 0
			for off := 0; off < len(data); off += bs {
				src := data[off:min(off+bs, len(data))]
				n, err := tc.compress(src, comp)
				if err != nil {
					t.Fatalf("block at %d: compress: %v", off, err)
				}
				total += n
				m, err := lz4.UncompressBlock(comp[:n], dec)
				if err != nil {
					t.Fatalf("block at %d: uncompress: %v", off, err)
				}
				if !bytes.Equal(dec[:m], src) {
					t.Fatalf("block at %d differs at byte %d", off, firstDiff(dec[:m], src))
				}
			}
			t.Logf("%d -> %d bytes (%.2f%%)", len(data), total, 100*float64(total)/float64(len(data)))
		})
	}
}

// silesiaCFrames are frames of the corpus made by the C CLI, which CI
// writes next to it: lz4 -BD -B4 and -BD -B7 (linked 64 KiB and 4 MiB
// blocks). Decoding them exercises the linked-block paths that the Writer
// does not produce.
var silesiaCFrames = []struct{ name, suffix string }{
	{"cLinked64K", ".B4D.lz4"},
	{"cLinked4M", ".B7D.lz4"},
}

func readSilesiaCFrame(tb testing.TB, suffix string) []byte {
	tb.Helper()
	return readOptional(tb, silesiaPath+suffix)
}

// TestSilesiaCFrames decodes the C CLI's frames of the corpus.
func TestSilesiaCFrames(t *testing.T) {
	data := silesia(t)
	for _, f := range silesiaCFrames {
		t.Run(f.name, func(t *testing.T) {
			comp := readSilesiaCFrame(t, f.suffix)
			got, err := io.ReadAll(lz4.NewReader(bytes.NewReader(comp)))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("decode differs at byte %d of %d (got %d bytes)", firstDiff(got, data), len(data), len(got))
			}
		})
	}
}

// BenchmarkSilesia decodes the corpus as frames from the Writer and the C
// CLI, like klauspost/compress's BenchmarkDecoderSilesia.
func BenchmarkSilesia(b *testing.B) {
	data := silesia(b)
	frames := []struct {
		name string
		comp func(b *testing.B) []byte
	}{
		{"go4M", func(b *testing.B) []byte { return writeFrame(b, data) }},
		{"go64K", func(b *testing.B) []byte { return writeFrame(b, data, lz4.BlockSizeOption(lz4.Block64Kb)) }},
	}
	for _, f := range silesiaCFrames {
		frames = append(frames, struct {
			name string
			comp func(b *testing.B) []byte
		}{f.name, func(b *testing.B) []byte { return readSilesiaCFrame(b, f.suffix) }})
	}
	for _, f := range frames {
		b.Run(f.name, func(b *testing.B) {
			comp := f.comp(b)
			r := bytes.NewReader(comp)
			zr := lz4.NewReader(r)
			b.SetBytes(int64(len(data)))
			b.ReportMetric(float64(len(comp))/float64(len(data)), "ratio")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r.Reset(comp)
				zr.Reset(r)
				if n, err := io.Copy(io.Discard, zr); err != nil || n != int64(len(data)) {
					b.Fatal(fmt.Errorf("decoded %d of %d bytes: %v", n, len(data), err))
				}
			}
		})
	}
}

func writeFrame(tb testing.TB, data []byte, opts ...lz4.Option) []byte {
	tb.Helper()
	var buf bytes.Buffer
	w := lz4.NewWriter(&buf)
	if err := w.Apply(opts...); err != nil {
		tb.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		tb.Fatal(err)
	}
	if err := w.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}
