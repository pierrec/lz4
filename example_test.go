package lz4_test

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pierrec/lz4/v4"
)

func Example() {
	// Compress and uncompress an input string.
	s := "hello world"
	r := strings.NewReader(s)

	// The pipe will uncompress the data from the writer.
	pr, pw := io.Pipe()
	zw := lz4.NewWriter(pw)
	zr := lz4.NewReader(pr)

	go func() {
		// Compress the input string.
		_, _ = io.Copy(zw, r)
		_ = zw.Close() // Make sure the writer is closed
		_ = pw.Close() // Terminate the pipe
	}()

	_, _ = io.Copy(os.Stdout, zr)

	// Output:
	// hello world
}

func ExampleCompressBlock() {
	s := "hello world"
	data := []byte(strings.Repeat(s, 100))
	buf := make([]byte, lz4.CompressBlockBound(len(data)))

	var c lz4.Compressor
	n, err := c.CompressBlock(data, buf)
	if err != nil {
		fmt.Println(err)
	}
	if n >= len(data) {
		fmt.Printf("`%s` is not compressible", s)
	}
	buf = buf[:n] // compressed data

	// Allocate a very large buffer for decompression.
	out := make([]byte, 10*len(data))
	n, err = lz4.UncompressBlock(buf, out)
	if err != nil {
		fmt.Println(err)
	}
	out = out[:n] // uncompressed data

	fmt.Println(string(out[:len(s)]))

	// Output:
	// hello world
}

func ExampleCompressBlockWithDict() {
	// A small message that shares most of its content with the dictionary.
	dict := []byte(`{"service":"checkout","level":"info","msg":"request served","status":200}`)
	msg := []byte(`{"service":"checkout","level":"info","msg":"request served","status":404}`)

	buf := make([]byte, lz4.CompressBlockBound(len(msg)))
	n, err := lz4.CompressBlockWithDict(msg, buf, dict)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("smaller:", n < len(msg))

	out := make([]byte, len(msg))
	n, err = lz4.UncompressBlockWithDict(buf[:n], out, dict)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(string(out[:n]))

	// Output:
	// smaller: true
	// {"service":"checkout","level":"info","msg":"request served","status":404}
}

func ExampleDictionaryOption() {
	dict := []byte("hello world, hello gopher")

	var buf strings.Builder
	zw := lz4.NewWriter(&buf)
	if err := zw.Apply(lz4.DictionaryOption(dict)); err != nil {
		fmt.Println(err)
	}
	_, _ = io.WriteString(zw, "hello gopher, hello world")
	_ = zw.Close()

	// The Reader needs the same dictionary.
	zr := lz4.NewReader(strings.NewReader(buf.String()))
	if err := zr.Apply(lz4.DictionaryOption(dict)); err != nil {
		fmt.Println(err)
	}
	_, _ = io.Copy(os.Stdout, zr)

	// Output:
	// hello gopher, hello world
}
