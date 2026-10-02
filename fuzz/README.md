# Fuzzing corpora

These are the corpora that go-fuzz collected when this directory held a
go-fuzz harness: `corpus` for compressing and round-tripping, and
`uncompress/corpus` for decoding blocks. The harness has been replaced by
native Go fuzzers, which these corpora seed:

- `internal/lz4block/fuzz_test.go`: blocks (`FuzzBlockRoundTrip`,
  `FuzzDecodeBlockDifferential`, `FuzzCompressBlockDst`,
  `FuzzCompressorCCompat`);
- `fuzz_test.go`: frames (`FuzzReader`, `FuzzFrameRoundTrip`);
- `bench/ccompat_test.go`: `FuzzCCompatMatchesC`, which checks that
  `CompressorCCompat` produces the same bytes as the C library's
  `LZ4_compress_fast`. It is in the separate `bench` module because it needs
  cgo and liblz4 (see `bench/README.md`), so CI does not run it.

Run one with, for example:

```sh
go test -run '^$' -fuzz '^FuzzDecodeBlockDifferential$' -fuzztime 10m ./internal/lz4block
```

Inputs that make a fuzzer fail are saved under that package's
`testdata/fuzz` and then run by `go test`. Some tests also read files from
`corpus` directly.
