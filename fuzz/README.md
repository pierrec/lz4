# Fuzzing corpora

These are the corpora that go-fuzz collected when this directory held a
go-fuzz harness: `corpus` for compressing and round-tripping, and
`uncompress/corpus` for decoding blocks. The harness has been replaced by
native Go fuzzers, which these corpora seed:

- `internal/lz4block/fuzz_test.go`: blocks (`FuzzBlockRoundTrip`,
  `FuzzDecodeBlockDifferential`, `FuzzCompressBlockDst`);
- `fuzz_test.go`: frames (`FuzzReader`, `FuzzFrameRoundTrip`).

Run one with, for example:

```sh
go test -run '^$' -fuzz '^FuzzDecodeBlockDifferential$' -fuzztime 10m ./internal/lz4block
```

Inputs that make a fuzzer fail are saved under that package's
`testdata/fuzz` and then run by `go test`. Some tests also read files from
`corpus` directly.
