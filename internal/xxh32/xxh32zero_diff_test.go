package xxh32

import (
	"math/rand"
	"testing"
)

// TestChecksumZeroVsGo compares the arch-specific ChecksumZero/update against
// the portable Go implementation for every length 0..512 at several
// alignments, so an assembly tail- or alignment-handling bug fails on the
// exact (length, alignment) pair. On platforms without assembly both sides
// run the same code and the test is a no-op by construction.
func TestChecksumZeroVsGo(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	backing := make([]byte, 1024)
	rng.Read(backing)

	for align := 0; align < 8; align++ {
		for n := 0; n <= 512; n++ {
			in := backing[align : align+n]
			if got, want := ChecksumZero(in), checksumZeroGo(in); got != want {
				t.Fatalf("align=%d len=%d: asm %08x != go %08x", align, n, got, want)
			}
		}
	}
}

// TestUpdateVsGo feeds identical inputs through the asm and Go update
// functions with varying block splits, exercising the buf-round path (odd
// leading chunk) and the empty-trailing-block path.
func TestUpdateVsGo(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	input := make([]byte, 1024)
	rng.Read(input)

	var buf [16]byte
	rng.Read(buf[:])

	splits := [][2]int{{0, 1024}, {16, 1008}, {160, 864}, {1024, 0}, {0, 0}, {16, 0}}
	for _, s := range splits {
		vAsm := [4]uint32{1, 2, 3, 4}
		vGo := vAsm
		update(&vAsm, &buf, input[:s[0]])
		update(&vAsm, nil, input[s[0]:s[0]+s[1]])
		updateGo(&vGo, &buf, input[:s[0]])
		updateGo(&vGo, nil, input[s[0]:s[0]+s[1]])
		if vAsm != vGo {
			t.Fatalf("split %v: asm %v != go %v", s, vAsm, vGo)
		}
	}
}

func benchSized(b *testing.B, f func([]byte) uint32, n int) {
	buf := make([]byte, n)
	rand.New(rand.NewSource(3)).Read(buf)
	b.SetBytes(int64(n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f(buf)
	}
}

func Benchmark_XXH32_Asm_64K(b *testing.B) { benchSized(b, ChecksumZero, 64<<10) }
func Benchmark_XXH32_Go_64K(b *testing.B)  { benchSized(b, checksumZeroGo, 64<<10) }
func Benchmark_XXH32_Asm_4M(b *testing.B)  { benchSized(b, ChecksumZero, 4<<20) }
func Benchmark_XXH32_Go_4M(b *testing.B)   { benchSized(b, checksumZeroGo, 4<<20) }
