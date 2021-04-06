package lz4

import (
	"bytes"
	"fmt"
	"io"

	"github.com/pierrec/lz4/v4"
)

// Fuzz function for the Reader and Writer.
func Fuzz(data []byte) int {
	var (
		r      = bytes.NewReader(data)
		w      = new(bytes.Buffer)
		pr, pw = io.Pipe()
		zr     = lz4.NewReader(pr)
		zw     = lz4.NewWriter(pw)
	)
	// Compress.
	go func() {
		_, err := io.Copy(zw, r)
		if err != nil {
			panic(err)
		}
		err = zw.Close()
		if err != nil {
			panic(err)
		}
		err = pw.Close()
		if err != nil {
			panic(err)
		}
	}()
	// Decompress.
	_, err := io.Copy(w, zr)
	if err != nil {
		panic(err)
	}
	// Check that the data is valid.
	if !bytes.Equal(data, w.Bytes()) {
		panic("not equal")
	}
	return 1
}

// CompressBlock with various destination sizes.
// Shares its corpus with Fuzz.
//
// go-fuzz-build && go-fuzz -func=FuzzCompressBlock
func FuzzCompressBlock(data []byte) int {
	var (
		bound = lz4.CompressBlockBound(len(data))
		c     lz4.Compressor
		comp  = make([]byte, lz4.CompressBlockBound(len(data)))
		keep  = 0
	)

	for _, b := range []int{bound, len(data), len(data) - len(data)>>1} {
		n, err := c.CompressBlock(data, comp[:b:b])

		switch {
		case err != nil && b == bound: // Should always work.
			panic(err)
		case n > b:
			panic(fmt.Sprintf("n = %d > b = %d", n, b))
		}
	}

	return keep
}

// Fuzzer for UncompressBlock: tries to decompress into a block the same size
// as the input.
//
// go-fuzz-build && go-fuzz -func=FuzzUncompressBlock -workdir=uncompress
func FuzzUncompressBlock(data []byte) int {
	decomp := make([]byte, len(data)+16-len(data)%8)
	for i := range decomp {
		decomp[i] = byte(i)
	}
	decomp = decomp[:len(data)]

	n, err := lz4.UncompressBlock(data, decomp, nil)
	if n > len(decomp) {
		panic("uncompressed length greater than buffer")
	}

	decomp = decomp[:cap(decomp)]
	for i := len(data); i < len(decomp); i++ {
		if decomp[i] != byte(i) {
			panic("UncompressBlock wrote out of bounds")
		}
	}

	if err != nil {
		return 0
	}
	return 1
}
