//go:build !appengine && gc && !noasm
// +build !appengine,gc,!noasm

package lz4block

import "testing"

// TestMatchCopyMatrixSSE reruns the match-copy matrix with the AVX2 loops
// disabled, so that AVX2 hardware also tests the SSE fallbacks.
func TestMatchCopyMatrixSSE(t *testing.T) {
	if !hasAVX2 {
		t.Skip("no AVX2: TestMatchCopyMatrix already covers the SSE paths")
	}
	hasAVX2 = false
	defer func() { hasAVX2 = true }()
	testMatchCopyMatrix(t)
}
