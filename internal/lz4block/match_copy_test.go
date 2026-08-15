package lz4block

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// These tests directly exercise the match-copy paths in decodeBlock with
// hand-assembled LZ4 blocks, so they fail loudly if a specific (offset,
// matchlen) combination regresses. They were written after an arm64
// implementation hit a silent CCMP immediate truncation that only corrupted
// output on specific offsets — TestCompressUncompressBlock caught it late
// (one failing byte far into a large input), while TestMatchCopyMatrix
// reproduces the failure on the very first out-of-range combination.

// buildSingleMatchBlock produces an LZ4 block consisting of:
//   - `prefix` bytes of deterministic literal content, and
//   - a single match of length `mlen` at back-reference distance `offset`.
//
// Returns the compressed block and the expected decompressed output.
func buildSingleMatchBlock(prefix, offset, mlen int) (compressed, decoded []byte) {
	if prefix < offset {
		panic("prefix must be >= offset so the match has valid source data")
	}
	if mlen < minMatch {
		panic("match length must be >= minMatch")
	}

	decoded = make([]byte, prefix+mlen)
	for i := 0; i < prefix; i++ {
		decoded[i] = byte(i%253 + 1) // non-zero so errors are obvious
	}
	for i := 0; i < mlen; i++ {
		decoded[prefix+i] = decoded[prefix+i-offset]
	}

	var buf bytes.Buffer
	writeToken(&buf, prefix, mlen-minMatch)
	buf.Write(decoded[:prefix])
	off := make([]byte, 2)
	binary.LittleEndian.PutUint16(off, uint16(offset))
	buf.Write(off)
	if rawM := mlen - minMatch; rawM >= 15 {
		writeExtended(&buf, rawM-15)
	}
	return buf.Bytes(), decoded
}

func writeToken(buf *bytes.Buffer, litlen, rawMatch int) {
	tokLit := litlen
	if tokLit > 15 {
		tokLit = 15
	}
	tokM := rawMatch
	if tokM > 15 {
		tokM = 15
	}
	buf.WriteByte(byte((tokLit << 4) | tokM))
	if litlen >= 15 {
		writeExtended(buf, litlen-15)
	}
}

func writeExtended(buf *bytes.Buffer, rem int) {
	for rem >= 255 {
		buf.WriteByte(0xFF)
		rem -= 255
	}
	buf.WriteByte(byte(rem))
}

// TestMatchCopySingle covers representative single-match cases spanning each
// of the fast paths (4-byte, 8-byte, and 16-byte) and each of the tricky
// cycling boundaries (offset == matchlen, offset == 8, etc.).
func TestMatchCopySingle(t *testing.T) {
	cases := []struct {
		name         string
		offset, mlen int
		prefix       int // defaults to offset when 0
	}{
		{"off1_len8_rle", 1, 8, 64},       // splat, byte RLE
		{"off1_len100_rle", 1, 100, 64},   // splat, long RLE
		{"off2_len16_rle", 2, 16, 64},     // splat halfword
		{"off3_len16_rle", 3, 16, 64},     // tile path, exactly one prefill
		{"off4_len8", 4, 8, 64},           // word-splat path
		{"off8_len8", 8, 8, 64},           // shortcut 8+8+2 boundary
		{"off8_len18", 8, 18, 64},         // shortcut + match-len boundary
		{"off8_len31", 8, 31, 64},         // copyMatchLoop8 at offset=8 (below tile threshold)
		{"off8_len32", 8, 32, 64},         // offset-8 tile: exactly the threshold
		{"off8_len100", 8, 100, 64},       // offset-8 tile with 8B + byte tail
		{"off16_len32", 16, 32, 64},       // offset-16 tile, no tail
		{"off16_len47", 16, 47, 64},       // offset-16 tile with 15-byte tail
		{"off12_len64", 12, 64, 64},       // 9..15 generic tile (prefill 12)
		{"off17_len32", 17, 32, 64},       // 32B tile: prefill 17 = 2x8 + 1, no tile iteration
		{"off24_len96", 24, 96, 64},       // 32B tile: prefill 3x8, three iterations
		{"off31_len4096", 31, 4096, 64},   // 32B tile: long
		{"off16_len16", 16, 16, 64},       // offset >= 16 but < 32
		{"off16_len100", 16, 100, 64},     // offset < 32: must use 8B loop
		{"off32_len16", 32, 16, 64},       // smallest 16B-eligible
		{"off32_len32", 32, 32, 64},       // two 16B iters
		{"off32_len100", 32, 100, 128},    // longer
		{"off64_len4096", 64, 4096, 8192}, // bulk
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prefix := c.prefix
			if prefix == 0 {
				prefix = c.offset
			}
			src, want := buildSingleMatchBlock(prefix, c.offset, c.mlen)
			dst := make([]byte, len(want))
			n := decodeBlock(dst, src, nil)
			if n != len(want) {
				t.Fatalf("decode returned %d, want %d", n, len(want))
			}
			if !bytes.Equal(dst[:n], want) {
				for i := 0; i < n; i++ {
					if dst[i] != want[i] {
						t.Fatalf("first mismatch at byte %d: got 0x%02x want 0x%02x",
							i, dst[i], want[i])
					}
				}
			}
		})
	}
}

// TestMatchCopyMatrix exhaustively sweeps (offset, matchlen) over the ranges
// most likely to expose addressing-mode or threshold bugs. Each combination
// is decoded and diffed against the naive reference built in memory.
func TestMatchCopyMatrix(t *testing.T) {
	// Offsets: cover 1..7 (splat/tile paths), every offset in 8..31 (the
	// 8-byte loop and the offset-8/16/9..15/17..31 tile paths, whose
	// prefill and tail handling depend on offset%8 and on the exact
	// remaining length), and 32+ (16B loop / memmove). Include the exact
	// threshold points plus a scattering above.
	offsets := []int{1, 2, 3, 4, 5, 6, 7}
	for o := 8; o <= 33; o++ {
		offsets = append(offsets, o)
	}
	offsets = append(offsets, 48, 64, 127, 128, 255, 1024)
	// Match lengths: every integer from 4..64 catches off-by-one issues in
	// all the loops; then a few large values exercise the bulk-copy path.
	var mlens []int
	for l := minMatch; l <= 64; l++ {
		mlens = append(mlens, l)
	}
	mlens = append(mlens, 65, 66, 71, 72, 79, 80, 95, 96, 100, 127, 128, 255, 256, 1023, 4096)

	for _, off := range offsets {
		for _, mlen := range mlens {
			prefix := off
			if prefix < 16 {
				prefix = 16
			}
			src, want := buildSingleMatchBlock(prefix, off, mlen)
			dst := make([]byte, len(want))
			n := decodeBlock(dst, src, nil)
			if n != len(want) {
				t.Fatalf("off=%d mlen=%d: decode returned %d, want %d",
					off, mlen, n, len(want))
			}
			if !bytes.Equal(dst[:n], want) {
				for i := 0; i < n; i++ {
					if dst[i] != want[i] {
						t.Fatalf("off=%d mlen=%d: first mismatch at byte %d: got 0x%02x want 0x%02x",
							off, mlen, i, dst[i], want[i])
					}
				}
			}
		}
	}
}

// TestMatchCopyChained runs many matches back-to-back so a bug that leaks
// register state between match-copy invocations shows up as corruption in a
// later match — which is exactly the kind of bug
// TestCompressUncompressBlock only finds incidentally.
func TestMatchCopyChained(t *testing.T) {
	const prefix = 256
	var buf bytes.Buffer

	want := make([]byte, prefix)
	for i := range want {
		want[i] = byte(i%253 + 1)
	}

	// Seq 0: entire prefix as literals + a first large match.
	{
		off := 64
		mlen := 200
		writeToken(&buf, prefix, mlen-minMatch)
		buf.Write(want[:prefix])
		off2 := make([]byte, 2)
		binary.LittleEndian.PutUint16(off2, uint16(off))
		buf.Write(off2)
		if rawM := mlen - minMatch; rawM >= 15 {
			writeExtended(&buf, rawM-15)
		}
		start := len(want)
		for i := 0; i < mlen; i++ {
			want = append(want, want[start-off+i])
		}
	}

	// Many follow-on matches with no literals between them, varying offset
	// and match length across thresholds.
	for i := 0; i < 40; i++ {
		off := 8 + (i*3)%120 // sweep small + medium + large offsets
		mlen := 16 + (i*7)%200
		writeToken(&buf, 0, mlen-minMatch)
		off2 := make([]byte, 2)
		binary.LittleEndian.PutUint16(off2, uint16(off))
		buf.Write(off2)
		if rawM := mlen - minMatch; rawM >= 15 {
			writeExtended(&buf, rawM-15)
		}
		start := len(want)
		for j := 0; j < mlen; j++ {
			want = append(want, want[start-off+j])
		}
	}

	// Trailing literal-only sequence so the block ends correctly.
	tail := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	writeToken(&buf, len(tail), 0)
	buf.Write(tail)
	want = append(want, tail...)

	dst := make([]byte, len(want))
	n := decodeBlock(dst, buf.Bytes(), nil)
	if n != len(want) {
		t.Fatalf("decode returned %d, want %d", n, len(want))
	}
	if !bytes.Equal(dst[:n], want) {
		for i := 0; i < n; i++ {
			if dst[i] != want[i] {
				t.Fatalf("first mismatch at byte %d: got 0x%02x want 0x%02x", i, dst[i], want[i])
			}
		}
	}
}
