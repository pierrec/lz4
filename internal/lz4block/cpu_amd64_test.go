//go:build !appengine && gc && !noasm
// +build !appengine,gc,!noasm

package lz4block

import "testing"

// TestMatchCopyMatrixFallbacks reruns the match-copy matrix with CPU features
// cleared, so that hardware that has them also tests the fallback loops.
func TestMatchCopyMatrixFallbacks(t *testing.T) {
	avx2, pfw := hasAVX2, hasPrefetchW
	defer func() { hasAVX2, hasPrefetchW = avx2, pfw }()
	for _, tc := range []struct {
		name      string
		avx2, pfw bool
	}{
		{"SSE", false, false},
		{"AVX2 without PREFETCHW", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.avx2 && !avx2 {
				t.Skip("no AVX2")
			}
			if tc.avx2 == avx2 && tc.pfw == pfw {
				t.Skip("TestMatchCopyMatrix already covers this")
			}
			hasAVX2, hasPrefetchW = tc.avx2, tc.pfw
			testMatchCopyMatrix(t)
		})
	}
}
