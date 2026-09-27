# Benchmarks against the C implementation

This module benchmarks the reference C implementation of LZ4 (liblz4) on the
same corpus (`internal/benchdata`), and with the same benchmark names, as the
`lz4` package, so that the two can be compared with
[benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat). It is a
separate module so that `github.com/pierrec/lz4/v4` does not use cgo.

It needs liblz4 and its headers, found with pkg-config: for example
`apt install liblz4-dev` or `brew install lz4`. The C results depend on the
liblz4 version, which the benchmark logs.

## Comparing Go with C

Run both on an otherwise idle machine, pinned to one CPU if possible, and
anchor the benchmark pattern (`-bench 'BenchmarkCompressBlock'` would also
match any benchmark whose name merely starts with it):

```sh
# From the repository root.
go test -run '^$' -bench '^BenchmarkCompressBlock$/fast' -cpu 1 -count 8 . > go.txt
(cd bench && go test -run '^$' -bench '^BenchmarkCompressBlock$/fast' -cpu 1 -count 8 .) > c.txt
benchstat go.txt c.txt
```

Each benchmark is named `compressor/input/blocksize`, compresses the input as
independent blocks of that size, and reports the throughput and the
compressed size as a percentage of the input (`%size`). `fast` is
`Compressor` in Go and `LZ4_compress_default` in C. The HC levels of the two
implementations are not equivalent, so compare their throughput together with
their `%size`.

## Comparing decompression

`BenchmarkUncompressBlock` decodes the same compressed blocks with both
implementations, so that neither benefits from decoding output shaped by its
own compressor. It is named `decoder/source/input/blocksize`: the decoder is
`go` (`lz4.UncompressBlock`) or `c` (`LZ4_decompress_safe`), and the source is
the compressor that produced the blocks (`gofast`, `cfast`, `gohc2`, `chc9`).

```sh
(cd bench && go test -run '^$' -bench '^BenchmarkUncompressBlock$' -cpu 1 -count 8 .) > decode.txt
```

Compare the `go/…` and `c/…` rows for the same source, input and block size.

## Checking that output is unchanged

`TestCompressGolden` in the `lz4` package checks the compressed output of
`Compressor` and `CompressorHC` against digests in `testdata/compress.golden`,
so a change that claims to keep the output identical can show it by passing.
When a change alters the output on purpose, regenerate the file with
`go test -run TestCompressGolden -update .`: the sizes it records show the
effect on compression.
