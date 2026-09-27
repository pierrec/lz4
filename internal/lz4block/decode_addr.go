//go:build (amd64 || arm64) && !appengine && gc && !noasm
// +build amd64 arm64
// +build !appengine
// +build gc
// +build !noasm

package lz4block

// decodeBlockAddr returns decodeBlock's entry address, for
// TestDecodeBlockAligned. Nothing else calls it, so the linker drops it.
func decodeBlockAddr() uintptr
