//go:build !appengine && gc && !noasm
// +build !appengine,gc,!noasm

package lz4block

// hasAVX2 selects decodeBlock's AVX2 loops. Tests clear it to exercise the
// SSE fallbacks on AVX2 hardware.
var hasAVX2 = cpuHasAVX2()

// cpuHasAVX2 reports whether the CPU supports AVX2 and the OS saves the
// YMM registers.
//
//go:noescape
func cpuHasAVX2() bool
