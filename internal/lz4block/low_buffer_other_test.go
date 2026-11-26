//go:build !linux || !amd64

package lz4block

import "testing"

func lowBuffer(t testing.TB, n int) []byte {
	t.Helper()
	t.Skip("low-address fixture requires Linux/amd64")
	return nil
}
