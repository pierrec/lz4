// +build !noasm

package xxh32

import "golang.org/x/sys/cpu"

// ChecksumZero returns the 32-bit hash of input.
var (
	ChecksumZero = checksumZeroGo
	update       = updateGo
)

//go:noescape
func checksumZeroAsm([]byte) uint32

//go:noescape
func updateAsm(*[4]uint32, *[16]byte, []byte)

func init() {
	// We use unaligned loads and stores in the assembly code. ARMv7 allows
	// that; ARMv5 doesn't. sys/cpu doesn't tell us the architecture version
	// directly, but the presence of NEON instructions implies ARMv7.
	if cpu.ARM.HasNEON {
		ChecksumZero = checksumZeroAsm
		update = updateAsm
	}
}
