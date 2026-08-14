//go:build !race

package xxh32_test

import (
	"testing"

	"github.com/pierrec/lz4/v4/internal/xxh32"
)

// TestZeroLargeInput covers inputs whose length does not fit in 32 bits.
// Sum32 adds the length modulo 2^32, but it must choose between the
// accumulator and the short-input path on the true length: 4 GiB is not a
// short input even though its truncated length is 0.
//
// The second case leaves a non-empty tail buffer, and because the remainder is
// written first, every subsequent write carries buffered bytes across the call
// boundary.
//
// The sums are the reference C implementation's frame content checksum and can
// be regenerated with:
//
//	head -c 4294967296 /dev/zero | lz4 -c | tail -c 4 | xxd -p  # 4139b935 -> 0x35b93941
//	head -c 4294967301 /dev/zero | lz4 -c | tail -c 4 | xxd -p  # 21cba38e -> 0x8ea3cb21
//
// It is built only without -race: hashing several GiB is roughly 60 times
// slower under the race detector, and there is no concurrency here to check.
func TestZeroLargeInput(t *testing.T) {
	if testing.Short() {
		t.Skip("hashes several GiB")
	}
	const fourGiB = int64(1) << 32
	block := make([]byte, 1<<20)
	for _, td := range []struct {
		sum uint32
		n   int64
	}{
		{0x35b93941, fourGiB},
		{0x8ea3cb21, fourGiB + 5},
	} {
		var xxh xxh32.XXHZero
		rem := td.n % int64(len(block))
		_, _ = xxh.Write(block[:rem])
		for written := rem; written < td.n; written += int64(len(block)) {
			_, _ = xxh.Write(block)
		}
		if got, want := xxh.Sum32(), td.sum; got != want {
			t.Errorf("%d bytes: got %x; want %x", td.n, got, want)
		}
	}
}
