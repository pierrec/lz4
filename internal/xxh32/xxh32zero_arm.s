// +build !noasm

#include "textflag.h"

#define prime1		$2654435761
#define prime2		$2246822519
#define prime3		$3266489917
#define prime4		$668265263
#define prime5		$374761393

#define prime1plus2	$606290984
#define prime1minus	$1640531535

// Register allocation.
#define p	R0
#define n	R1
#define h	R2
#define v1	R2	// Alias for h.
#define v2	R3
#define v3	R4
#define v4	R5
#define x1	R6
#define x2	R7
#define x3	R8
#define x4	R9

// We need the primes in registers. The 16-byte loop only uses prime{1,2}.
#define prime1r	R11
#define prime2r	R12
#define prime3r	R3	// The rest can alias v{2-4}.
#define prime4r	R4
#define prime5r	R5

#define round16				\
	MOVM.IA.W (p), [x1, x2, x3, x4]	\
					\
	MULA x1, prime2r, v1, v1	\
	MULA x2, prime2r, v2, v2	\
	MULA x3, prime2r, v3, v3	\
	MULA x4, prime2r, v4, v4	\
					\
	MOVW v1 @> 19, v1		\
	MOVW v2 @> 19, v2		\
	MOVW v3 @> 19, v3		\
	MOVW v4 @> 19, v4		\
					\
	MUL prime1r, v1			\
	MUL prime1r, v2			\
	MUL prime1r, v3			\
	MUL prime1r, v4			\


// func checksumZeroAsm([]byte) uint32
TEXT ·checksumZeroAsm(SB), NOFRAME|NOSPLIT, $-4-16
	MOVW input_base+0(FP), p
	MOVW input_len+4(FP),  n

	MOVW prime1, prime1r
	MOVW prime2, prime2r

	// Set up h for n < 16. It's tempting to say {ADD prime5, n, h}
	// here, but that's a pseudo-op that generates a load through R11.
	MOVW prime5, prime5r
	ADD  prime5r, n, h
	CMP  $0, n
	BEQ  end

	// We let n go negative so we can do comparisons with SUB.S
	// instead of separate CMP.
	SUB.S $16, n
	BMI   loop16done

	MOVW prime1plus2, v1
	MOVW prime2,      v2
	MOVW $0,          v3
	MOVW prime1minus, v4

loop16:
	SUB.S $16, n
	round16
	BPL   loop16

	MOVW v1 @> 31, h
	ADD  v2 @> 25, h
	ADD  v3 @> 20, h
	ADD  v4 @> 14, h

	// h += len(input) with v2 as temporary.
	MOVW input_len+4(FP), v2
	ADD  v2, h

loop16done:
	ADD $16, n	// Restore number of bytes left.

	SUB.S $4, n
	MOVW  prime3, prime3r
	BMI   loop4done
	MOVW  prime4, prime4r

loop4:
	SUB.S $4, n

	MOVW.P 4(p), x1
	MULA   prime3r, x1, h, h
	MOVW   h @> 15, h
	MUL    prime4r, h

	BPL loop4

loop4done:
	ADD.S $4, n	// Restore number of bytes left.
	BEQ   end

	MOVW prime5, prime5r

loop1:
	SUB.S $1, n

	MOVBU.P 1(p), x1
	MULA    prime5r, x1, h, h
	MOVW    h @> 21, h
	MUL     prime1r, h

	BNE loop1

end:
	MOVW prime3, prime3r
	EOR  h >> 15, h
	MUL  prime2r, h
	EOR  h >> 13, h
	MUL  prime3r, h
	EOR  h >> 16, h

	MOVW h, ret+12(FP)
	RET


// func updateAsm(v *[4]uint64, buf *[16]byte, p []byte)
TEXT ·updateAsm(SB), NOFRAME|NOSPLIT, $-4-20
	MOVW    v_arg+0(FP), p
	MOVM.IA (p), [v1, v2, v3, v4]

	MOVW prime1, prime1r
	MOVW prime2, prime2r

	// Process buf, if not nil.
	MOVW buf_arg+4(FP), p
	CMP  $0, p
	BEQ  noBuffered

	round16

noBuffered:
	MOVW input_ptr+ 8(FP), p
	MOVW input_len+12(FP), n

	SUB.S $16, n
	BMI   end

loop:
	SUB.S $16, n
	round16
	BPL   loop

end:
	MOVW    v_arg+0(FP), p
	MOVM.IA [v1, v2, v3, v4], (p)
	RET
