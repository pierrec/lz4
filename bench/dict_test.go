package bench

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pierrec/lz4/v4"
)

// dictCases splits each corpus input into a dictionary and the data after
// it: dictionaries shorter than, equal to and longer than the 64 KiB window.
func dictCases(tb testing.TB) []struct {
	name       string
	dict, data []byte
} {
	var cases []struct {
		name       string
		dict, data []byte
	}
	for _, in := range benchCorpus(tb) {
		for _, dl := range []int{100, 64 << 10, 100 << 10} {
			if dl >= len(in.Data) {
				continue
			}
			data := in.Data[dl:]
			if len(data) > 256<<10 {
				data = data[:256<<10]
			}
			cases = append(cases, struct {
				name       string
				dict, data []byte
			}{in.Name, in.Data[:dl], data})
		}
	}
	return cases
}

// checkDictBlockInterop compresses src with dict in Go and C, and checks that
// each decompresses the other's block.
func checkDictBlockInterop(t testing.TB, src, dict []byte) {
	t.Helper()
	// The fuzzer may pass overlapping slices. C's streaming functions look at
	// addresses and drop the part of a dictionary that src overlaps.
	src, dict = bytes.Clone(src), bytes.Clone(dict)
	bound := lz4.CompressBlockBound(len(src))
	out := make([]byte, len(src))

	goBlk := make([]byte, bound)
	n, err := lz4.CompressBlockWithDict(src, goBlk, dict)
	if err != nil {
		t.Fatalf("len(src)=%d len(dict)=%d: CompressBlockWithDict: %v", len(src), len(dict), err)
	}
	if n > 0 { // n == 0 only for empty src
		if m := DecompressSafeUsingDict(goBlk[:n], out, dict); m != len(src) || !bytes.Equal(out[:m], src) {
			t.Fatalf("len(src)=%d len(dict)=%d: LZ4_decompress_safe_usingDict of Go's block returned %d", len(src), len(dict), m)
		}
	}

	for _, c := range []struct {
		name     string
		compress func(src, dst, dict []byte) int
	}{
		{"fast", func(src, dst, dict []byte) int { return CompressFastDict(src, dst, dict, 1) }},
		{"hc2", func(src, dst, dict []byte) int { return CompressHCDict(src, dst, dict, 2) }},
		{"hc9", func(src, dst, dict []byte) int { return CompressHCDict(src, dst, dict, 9) }},
	} {
		cBlk := make([]byte, bound)
		cn := c.compress(src, cBlk, dict)
		if cn <= 0 {
			t.Fatalf("%s len(src)=%d len(dict)=%d: C compression returned %d", c.name, len(src), len(dict), cn)
		}
		m, err := lz4.UncompressBlockWithDict(cBlk[:cn], out, dict)
		if err != nil || m != len(src) || !bytes.Equal(out[:m], src) {
			t.Fatalf("%s len(src)=%d len(dict)=%d: UncompressBlockWithDict of C's block: %d, %v", c.name, len(src), len(dict), m, err)
		}
	}
}

// TestDictBlockMatchesC checks that blocks compressed with a dictionary by
// CompressBlockWithDict decode with LZ4_decompress_safe_usingDict, and that
// UncompressBlockWithDict decodes blocks from LZ4_compress_fast_continue and
// LZ4_compress_HC_continue after LZ4_loadDict and LZ4_loadDictHC.
func TestDictBlockMatchesC(t *testing.T) {
	t.Logf("liblz4 %s", Version())
	for _, c := range dictCases(t) {
		for _, bs := range []int{1 << 10, 16 << 10, 64 << 10, 256 << 10} {
			src := c.data[:min(bs, len(c.data))]
			checkDictBlockInterop(t, src, c.dict)
		}
	}
}

// TestDictFrameMatchesC checks that Reader with DictionaryOption decodes
// frames that LZ4F_compressFrame_usingCDict writes, with linked and
// independent blocks, and that LZ4F_decompress_usingDict decodes frames that
// Writer with DictionaryOption writes.
func TestDictFrameMatchesC(t *testing.T) {
	for _, c := range dictCases(t) {
		for _, level := range []int{1, 9} {
			for _, linked := range []bool{false, true} {
				frame := CompressFrameDict(c.data, c.dict, level, linked, 0)
				if frame == nil {
					t.Fatalf("%s level %d linked %v: C frame compression failed", c.name, level, linked)
				}
				zr := lz4.NewReader(bytes.NewReader(frame))
				if err := zr.Apply(lz4.DictionaryOption(c.dict)); err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(zr)
				if err != nil || !bytes.Equal(got, c.data) {
					t.Fatalf("%s len(dict)=%d level %d linked %v: Reader: %d bytes, %v", c.name, len(c.dict), level, linked, len(got), err)
				}
			}
		}

		var frame bytes.Buffer
		zw := lz4.NewWriter(&frame)
		if err := zw.Apply(lz4.BlockSizeOption(lz4.Block64Kb), lz4.DictionaryOption(c.dict)); err != nil {
			t.Fatal(err)
		}
		if _, err := zw.Write(c.data); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		got, ok := DecompressFrameDict(frame.Bytes(), c.dict, len(c.data))
		if !ok || !bytes.Equal(got, c.data) {
			t.Fatalf("%s len(dict)=%d: LZ4F_decompress_usingDict of Writer's frame: %d bytes, ok %v", c.name, len(c.dict), len(got), ok)
		}
	}
}

// FuzzDictMatchesC runs checkDictBlockInterop on arbitrary input and
// dictionaries. It needs liblz4, so it runs outside CI; see README.md.
func FuzzDictMatchesC(f *testing.F) {
	f.Add([]byte("hello world"), []byte("hello"))
	f.Add(bytes.Repeat([]byte("abcd"), 100), bytes.Repeat([]byte("abcd"), 20000))
	for _, c := range dictCases(f) {
		f.Add(c.data[:min(len(c.data), 8<<10)], c.dict)
	}
	f.Fuzz(func(t *testing.T, src, dict []byte) {
		if len(src) == 0 {
			return
		}
		checkDictBlockInterop(t, src, dict)
	})
}

var updateDictFixtures = flag.Bool("update", false, "rewrite the dictionary frames in ../testdata")

// TestDictFixtures writes the frames that TestReaderDictCFrames in the lz4
// package decodes, so that CI, which does not build this module, checks that
// Reader decodes C frames compressed with a dictionary. They hold the 80 KiB
// of pg1661.txt that follow its first 64 KiB, the dictionary. Run it with
// -update to rewrite them.
func TestDictFixtures(t *testing.T) {
	if !*updateDictFixtures {
		t.Skip("run with -update to rewrite the fixtures")
	}
	var pg []byte
	for _, in := range benchCorpus(t) {
		if in.Name == "pg1661" {
			pg = in.Data
		}
	}
	dict, data := pg[:64<<10], pg[64<<10:144<<10]
	for _, f := range []struct {
		name   string
		level  int
		linked bool
	}{
		{"pg1661_dict_independent.lz4", 1, false},
		{"pg1661_dict_linked_hc.lz4", 9, true},
	} {
		frame := CompressFrameDict(data, dict, f.level, f.linked, 0)
		if frame == nil {
			t.Fatalf("%s: C frame compression failed", f.name)
		}
		if err := os.WriteFile(filepath.Join("..", "testdata", f.name), frame, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d bytes", f.name, len(frame))
	}
}
