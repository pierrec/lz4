package lz4

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestFrameDescriptor(t *testing.T) {
	for _, tc := range []struct {
		flags             string
		bsum, csize, csum bool
		size              uint64
		bsize             BlockSize
	}{
		{"\x64\x40\xa7", false, false, true, 0, Block64Kb},
		{"\x64\x50\x08", false, false, true, 0, Block256Kb},
		{"\x64\x60\x85", false, false, true, 0, Block1Mb},
		{"\x64\x70\xb9", false, false, true, 0, Block4Mb},
	} {
		s := tc.flags
		label := fmt.Sprintf("%02x %02x %02x", s[0], s[1], s[2])
		t.Run(label, func(t *testing.T) {
			r := &Reader{src: strings.NewReader(tc.flags)}
			var fd FrameDescriptor
			if err := fd.initR(r); err != nil {
				t.Fatal(err)
			}

			if got, want := fd.Flags.BlockChecksum(), tc.bsum; got != want {
				t.Fatalf("got %v; want %v\n", got, want)
			}
			if got, want := fd.Flags.Size(), tc.csize; got != want {
				t.Fatalf("got %v; want %v\n", got, want)
			}
			if got, want := fd.Flags.ContentChecksum(), tc.csum; got != want {
				t.Fatalf("got %v; want %v\n", got, want)
			}
			if got, want := fd.ContentSize, tc.size; got != want {
				t.Fatalf("got %v; want %v\n", got, want)
			}
			if got, want := fd.Flags.BlockSizeIndex(), tc.bsize.index(); got != want {
				t.Fatalf("got %v; want %v\n", got, want)
			}

			buf := new(bytes.Buffer)
			w := &Writer{src: buf}
			fd.initW(nil)
			fd.Checksum = 0
			if err := fd.write(w); err != nil {
				t.Fatal(err)
			}
			if got, want := buf.String(), tc.flags; got != want {
				t.Fatalf("got %q; want %q\n", got, want)
			}
		})
	}
}

func TestFrameDataBlock(t *testing.T) {
	const sample = "abcd4566878dsvddddddqvq&&&&&((èdvshdvsvdsdh)"
	min := func(a, b int) int {
		if a < b {
			return a
		}
		return b
	}
	for _, tc := range []struct {
		data string
		size BlockSize
	}{
		{"", Block64Kb},
		{sample, Block64Kb},
		{strings.Repeat(sample, 10), Block64Kb},
	} {
		label := fmt.Sprintf("%s (%d)", tc.data[:min(len(tc.data), 10)], len(tc.data))
		t.Run(label, func(t *testing.T) {
			data := tc.data
			size := tc.size
			zbuf := new(bytes.Buffer)
			w := &Writer{src: zbuf, level: Fast}

			block := newFrameDataBlock(size.index())
			block.compress(w, []byte(data), nil)
			if err := block.write(w); err != nil {
				t.Fatal(err)
			}

			buf := make([]byte, size)
			r := &Reader{src: zbuf}
			n, err := block.uncompress(r, buf)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := n, len(data); got != want {
				t.Fatalf("got %d; want %d", got, want)
			}
			if got, want := string(buf[:n]), data; got != want {
				t.Fatalf("got %q; want %q", got, want)
			}
		})
	}
}
