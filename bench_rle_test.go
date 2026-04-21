package lz4_test

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/pierrec/lz4/v4"
)

// buildRLE returns a byte slice of n bytes consisting of 32 random-ish bytes
// followed by long runs that LZ4 will encode as tokens with small offsets
// (offset=1 for the zero padding, offset=2 for AB ABAB... patterns).
func buildRLE(n int, periodBytes int) []byte {
	out := make([]byte, n)
	pattern := make([]byte, periodBytes)
	for i := range pattern {
		pattern[i] = byte('A' + i)
	}
	// Seed a small amount of preceding data so the compressor sees a real
	// match source, then fill the rest with repetitions of the pattern.
	for i := range out {
		out[i] = pattern[i%periodBytes]
	}
	return out
}

func benchRLE(b *testing.B, period int) {
	const n = 1 << 20 // 1 MiB
	raw := buildRLE(n, period)
	compressed := make([]byte, lz4.CompressBlockBound(n))
	var c lz4.Compressor
	m, err := c.CompressBlock(raw, compressed)
	if err != nil {
		b.Fatalf("compress: %v", err)
	}
	compressed = compressed[:m]

	decoded := make([]byte, n)
	b.SetBytes(int64(n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := lz4.UncompressBlock(compressed, decoded)
		if err != nil {
			b.Fatalf("decompress: %v", err)
		}
		if got != n {
			b.Fatalf("short decode: %d != %d", got, n)
		}
	}
	b.StopTimer()
	if !bytes.Equal(decoded, raw) {
		b.Fatalf("round-trip mismatch")
	}
	// Report compression ratio for visibility.
	b.ReportMetric(float64(len(compressed))/float64(n), "compress_ratio")
}

func BenchmarkUncompressRLE1(b *testing.B) { benchRLE(b, 1) }
func BenchmarkUncompressRLE2(b *testing.B) { benchRLE(b, 2) }
func BenchmarkUncompressRLE3(b *testing.B) { benchRLE(b, 3) }
func BenchmarkUncompressRLE4(b *testing.B) { benchRLE(b, 4) }

// buildColumnar builds a synthetic input that mimics the match-length and
// match-offset distribution typical of columnar / record-oriented compressed
// storage. Columnar data tends to have two distinct populations: many short
// matches (length 4..18, within the "shortcut" 18-byte copy range) that
// together cover a minority of bytes, and a small number of long matches
// (hundreds of bytes) that cover the majority. The input is a sequence of
// fixed-size "records" each drawn from a small pool of random patterns, so
// LZ4 emits long matches at medium-to-large offsets (back-references to
// earlier record instances) -- this is the workload that copyMatchLoop8 /
// copyMatchLoop16 actually exercise on such data, vs. the English-text
// benchmarks above where short matches dominate.
//
// recordSize controls match length ceiling (and thus how many bytes per long
// match). poolSize × recordSize controls the typical offset (records repeat
// within a sliding window of that size). totalSize is the full buffer.
func buildColumnar(totalSize, recordSize, poolSize int) []byte {
	rng := rand.New(rand.NewSource(42))
	pool := make([][]byte, poolSize)
	for i := range pool {
		pool[i] = make([]byte, recordSize)
		rng.Read(pool[i])
	}
	out := make([]byte, 0, totalSize)
	for len(out) < totalSize {
		out = append(out, pool[rng.Intn(poolSize)]...)
	}
	return out[:totalSize]
}

func benchColumnar(b *testing.B, totalSize, recordSize, poolSize int) {
	raw := buildColumnar(totalSize, recordSize, poolSize)
	compressed := make([]byte, lz4.CompressBlockBound(len(raw)))
	var c lz4.Compressor
	m, err := c.CompressBlock(raw, compressed)
	if err != nil {
		b.Fatalf("compress: %v", err)
	}
	compressed = compressed[:m]

	decoded := make([]byte, len(raw))
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := lz4.UncompressBlock(compressed, decoded)
		if err != nil {
			b.Fatalf("decompress: %v", err)
		}
		if got != len(raw) {
			b.Fatalf("short decode: %d != %d", got, len(raw))
		}
	}
	b.StopTimer()
	if !bytes.Equal(decoded, raw) {
		b.Fatalf("round-trip mismatch")
	}
	b.ReportMetric(float64(len(compressed))/float64(len(raw)), "compress_ratio")
}

// 1 MiB of 512-byte records drawn from a pool of 64 -- yields a mix of long
// matches (usually 512-byte whole-record copies) at offsets in roughly the
// 512..32768 range.
func BenchmarkUncompressColumnarMed(b *testing.B) {
	benchColumnar(b, 1<<20, 512, 64)
}

// Larger records -- pushes typical match length up toward 4 KiB so the bulk
// copy loops dominate decode time.
func BenchmarkUncompressColumnarLong(b *testing.B) {
	benchColumnar(b, 1<<20, 4096, 16)
}

// Smaller records -- pushes typical match length down toward 64 bytes, so
// the shortcut's 18-byte copy fires more often than the long-match loop.
func BenchmarkUncompressColumnarShort(b *testing.B) {
	benchColumnar(b, 1<<20, 64, 256)
}
