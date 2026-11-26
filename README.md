# lz4 : LZ4 compression in pure Go

[![Go Reference](https://pkg.go.dev/badge/github.com/pierrec/lz4/v4.svg)](https://pkg.go.dev/github.com/pierrec/lz4/v4)
[![CI](https://github.com/pierrec/lz4/workflows/ci/badge.svg)](https://github.com/pierrec/lz4/actions)
[![GitHub tag (latest SemVer)](https://img.shields.io/github/tag/pierrec/lz4.svg?style=social)](https://github.com/pierrec/lz4/tags)

## Overview

This package provides a streaming interface to [LZ4 data streams](http://fastcompression.blogspot.fr/2013/04/lz4-streaming-format-final.html) as well as low level compress and uncompress functions for LZ4 data blocks.
The implementation is based on the reference C [one](https://github.com/lz4/lz4).

## Install

Assuming you have the go toolchain installed:

```
go get github.com/pierrec/lz4/v4
```

There is a command line interface tool to compress and decompress LZ4 files.

```
go install github.com/pierrec/lz4/cmd/lz4c@latest
```

Usage

```
Usage of lz4c:
  -version
        print the program version

Subcommands:
Compress the given files or from stdin to stdout.
compress [arguments] [<file name> ...]
  -D string
        compress with the dictionary in this file; uncompress with the same one
  -bc
        enable block checksum
  -ccompat
        -l is a level of the C lz4 tool, 1 to 12, and blocks are compressed as it compresses them
  -l int
        compression level (0=fastest)
  -sc
        enable stream checksum
  -size string
        block max size [64K,256K,1M,4M] (default "4M")

Uncompress the given files or from stdin to stdout.
uncompress [arguments] [<file name> ...]
  -D string
        uncompress with the dictionary in this file

```


## Example

```
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
```

## Dictionaries

A dictionary improves the compression of small inputs that look alike, such
as log lines or messages that share a schema. `CompressBlockWithDict` and
`UncompressBlockWithDict` use one for a block, and `DictionaryOption` for a
`Writer`, `Reader` or `CompressingReader`. Decompression needs the same
dictionary: frames do not record which one was used. The output is
compatible with the C library's dictionary functions.

LZ4 does not define a dictionary format. Any bytes will do, and only the last
64 KiB are used, so put the most common content last. A sample of typical
data works; zstd can build a better one from many samples:

```
zstd --train samples/* -o dict --maxdict=65536
```

## Contributing

Contributions are very welcome for bug fixing, performance improvements...!

- Open an issue with a proper description
- Send a pull request with appropriate test case(s)

## Contributors

Thanks to all [contributors](https://github.com/pierrec/lz4/graphs/contributors)  so far!

Special thanks to [@Zariel](https://github.com/Zariel) for his asm implementation of the decoder.

Special thanks to [@greatroar](https://github.com/greatroar) for his work on the asm implementations of the decoder for amd64 and arm64.

Special thanks to [@klauspost](https://github.com/klauspost) for his work on optimizing the code.

Special thanks to [@lizthegrey](https://github.com/lizthegrey) for work on the arm64 decoder, the compressors, and fuzzing and robustness testing.

## License

This package is under the BSD 3-Clause License in [LICENSE](LICENSE).
`CompressorCCompat` and `CompressorCCompatHC` are ports of the reference
implementation's `lib/lz4.c` and `lib/lz4hc.c`, which are copyright Yann
Collet under the BSD 2-Clause License in
[LICENSE-lz4](LICENSE-lz4). That code is compiled into every program that
imports the package, so programs distributed in binary form should reproduce
both notices.
