// +build !noasm

package xxh32

// ChecksumZero returns the 32-bit hash of input.
//
//go:noescape
func ChecksumZero([]byte) uint32

//go:noescape
func update(*[4]uint32, *[16]byte, []byte)
