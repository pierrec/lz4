//go:build !linux && !darwin
// +build !linux,!darwin

package lz4block

import "testing"

// guardedTail returns an n-byte slice. On Linux and macOS, reads past its
// end fault.
func guardedTail(t testing.TB, n int) []byte {
	return make([]byte, n)
}
