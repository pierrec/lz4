package lz4_test

import (
	"bytes"
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
