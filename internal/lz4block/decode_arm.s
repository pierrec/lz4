// +build gc
// +build !noasm

#include "textflag.h"

// Register allocation.
#define dst	R0
#define dstorig	R1
#define src	R2
#define dstend	R3
#define srcend	R4
#define match	R5	// Match address.
#define token	R6
#define len	R7	// Literal and match lengths.
#define offset	R5	// Match offset; overlaps with match.
#define tmp1	R8
#define tmp2	R9
#define tmp3	R12

#define minMatch	$4

// func decodeBlock(dst, src []byte) int
TEXT ·decodeBlock(SB), NOFRAME|NOSPLIT, $-4-28
	MOVW dst_ptr+0(FP),  dst
	MOVW dst_len+4(FP),  dstend
	MOVW src_ptr+12(FP), src
	MOVW src_len+16(FP), srcend

	CMP $0, srcend
	BEQ shortSrc

	ADD dst, dstend
	ADD src, srcend

	MOVW dst, dstorig

loop:
	CMP src, srcend
	BEQ end

	// Read token. Extract literal length.
	MOVBU.P 1(src), token
	MOVW    token >> 4, len
	CMP     $15, len
	BNE     readLitlenDone

readLitlenLoop:
	CMP     src, srcend
	BEQ     shortSrc
	MOVBU.P 1(src), tmp1
	ADD     tmp1, len
	CMP     $255, tmp1
	BEQ     readLitlenLoop

readLitlenDone:
	CMP $0, len
	BEQ copyLiteralDone

	// Bounds check dst+len and src+len.
	ADD dst, len, tmp1
	ADD src, len, tmp2
	CMP dstend, tmp1
	BHI shortDst
	CMP srcend, tmp2
	BHI shortSrc

	// Copy literal.
	CMP $4, len
	BLO copyLiteralFinish

	// Copy 0-3 bytes until src is aligned.
	TST        $1, src
	MOVBU.NE.P 1(src), tmp1
	MOVB.NE.P  tmp1, 1(dst)
	SUB.NE     $1, len

	TST        $2, src
	MOVHU.NE.P 2(src), tmp2
	MOVH.NE.P  tmp2, 2(dst)
	SUB.NE     $2, len

	CMP $4, len
	BLO copyLiteralFinish

copyLiteralLoop:
	// Aligned load, unaligned write.
	SUB   $4, len
	MOVW.P 4(src), tmp1
	MOVW   tmp1 >>  8, tmp2
	MOVB   tmp2, 1(dst)
	MOVW   tmp1 >> 16, tmp3
	MOVB   tmp3, 2(dst)
	MOVW   tmp1 >> 24, tmp2
	MOVB   tmp2, 3(dst)
	MOVB.P tmp1, 4(dst)
	CMP    $4, len
	BHS    copyLiteralLoop

copyLiteralFinish:
	// Copy remaining 0-3 bytes.
	TST        $2, len
	MOVHU.NE.P 2(src), tmp2
	MOVHU.NE.P tmp2, 2(dst)
	TST        $1, len
	MOVBU.NE.P 1(src), tmp1
	MOVBU.NE.P tmp1, 1(dst)

copyLiteralDone:
	CMP src, srcend
	BEQ end

	// Read offset.
	ADD   $2, src
	CMP   srcend, src
	BHI   shortSrc
	MOVHU -2(src), offset
	CMP   $0, offset
	BEQ   corrupt

	// Read match length.
	AND $15, token, len
	CMP $15, len
	BNE readMatchlenDone

readMatchlenLoop:
	CMP     src, srcend
	BEQ     shortSrc
	MOVBU.P 1(src), tmp1
	ADD     tmp1, len
	CMP     $255, tmp1
	BEQ     readMatchlenLoop

readMatchlenDone:
	ADD minMatch, len

	ADD dst, len, tmp1
	CMP dstend, tmp1
	BHI shortDst

	SUB offset, dst, match
	CMP dstorig, match
	BLO corrupt

copyMatch:
	// Simple byte-at-a-time copy.
	SUB.S   $1, len
	MOVBU.P 1(match), tmp2
	MOVB.P  tmp2, 1(dst)
	BNE     copyMatch

	B loop

	// The three error cases have distinct labels so we can put different
	// return codes here when debugging, or if the error returns need to
	// be changed.
shortDst:
shortSrc:
corrupt:
	MOVW $-1, tmp1
	MOVW tmp1, ret+24(FP)
	RET

end:
	SUB  dstorig, dst, tmp1
	MOVW tmp1, ret+24(FP)
	RET
