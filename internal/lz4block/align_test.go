//go:build (amd64 || arm64) && !appengine && gc && !noasm
// +build amd64 arm64
// +build !appengine
// +build gc
// +build !noasm

package lz4block

import "testing"

// TestDecodeBlockAligned checks that decodeBlock starts on a 64-byte
// boundary. Its hot loops are placed relative to that boundary, and when it
// fell to 32 mod 64 on amd64 (after an unrelated function was linked ahead
// of it) Zen 4/5 lost 2-6% on real data. A PCALIGN $64 in the function body
// guarantees the alignment; this test stops it from being dropped.
func TestDecodeBlockAligned(t *testing.T) {
	addr := decodeBlockAddr()
	if addr%64 != 0 {
		t.Fatalf("decodeBlock at %#x is %d bytes past a 64-byte boundary", addr, addr%64)
	}
	t.Logf("decodeBlock at %#x", addr)
}
