// +build gc
// +build !noasm

#include "go_asm.h"
#include "textflag.h"

// Register allocation.
#define dst	R0
#define dstorig	R1
#define src	R2
#define dstend	R3
#define srcend	R4
#define match	R5	// Match address.
#define dictend	R6
#define token	R7
#define len	R8	// Literal and match lengths.
#define offset	R7	// Match offset; overlaps with token.
#define tmp1	R9
#define tmp2	R11
#define tmp3	R12

// func decodeBlock(dst, src, dict []byte) int
TEXT ·decodeBlock(SB), NOFRAME+NOSPLIT, $-8-80
	MOVD dst_base  +0(FP), dst
	MOVD dst_len   +8(FP), dstend
	MOVD src_base +24(FP), src
	MOVD src_len  +32(FP), srcend

	CMP $0, srcend
	BEQ shortSrc

	ADD dst, dstend
	ADD src, srcend

	MOVD dst, dstorig

loop:
	// Read token. Extract literal length.
	MOVBU.P 1(src), token
	LSR     $4, token, len
	CMP     $15, len
	BNE     readLitlenDone

readLitlenLoop:
	CMP     src, srcend
	BEQ     shortSrc
	MOVBU.P 1(src), tmp1
	ADDS    tmp1, len
	BVS     shortDst
	CMP     $255, tmp1
	BEQ     readLitlenLoop

readLitlenDone:
	CMP $0, len
	BEQ copyLiteralDone

	// Bounds check dst+len and src+len.
	ADDS     dst, len, tmp1
	BCS      shortSrc
	ADDS     src, len, tmp2
	BCS      shortSrc
	CMP      dstend, tmp1
	BHI      shortDst
	CMP      srcend, tmp2
	BHI      shortSrc

	// Copy literal.
	CMP $8, len
	BLO copyLiteralFinish

	// Copy 0-7 bytes until src is aligned.
	TST        $1, src
	BEQ        twos
	MOVBU.P    1(src), tmp1
	MOVB.P     tmp1, 1(dst)
	SUB        $1, len

twos:
	TST        $2, src
	BEQ        fours
	MOVHU.P    2(src), tmp2
	MOVB.P     tmp2, 1(dst)
	LSR        $8, tmp2, tmp1
	MOVB.P     tmp1, 1(dst)
	SUB        $2, len

fours:
	TST        $4, src
	BEQ        copyLiteralLoopCond
	MOVWU.P    4(src), tmp2
	MOVB.P     tmp2, 1(dst)
	LSR        $8, tmp2, tmp1
	MOVB.P     tmp1, 1(dst)
	LSR        $16, tmp2, tmp3
	MOVB.P     tmp3, 1(dst)
	LSR        $24, tmp2, tmp1
	MOVB.P     tmp1, 1(dst)
	SUB        $4, len

	B copyLiteralLoopCond

copyLiteralLoop:
	// Aligned load, unaligned write.
	MOVD.P 8(src), tmp1
	MOVD.P tmp1, 8(dst)
copyLiteralLoopCond:
	// Loop until len-8 < 0.
	SUBS   $8, len
	BPL    copyLiteralLoop

copyLiteralFinish:
	// Copy remaining 0-7 bytes.
	// At this point, len may be < 0, but len&7 is still accurate.
	TST       $1, len
	BEQ       finishTwos
	MOVB.P    1(src), tmp3
	MOVB.P    tmp3, 1(dst)

finishTwos:
	TST       $2, len
	BEQ       finishFours
	MOVB.P    2(src), tmp1
	MOVB.P    tmp1, 2(dst)
	MOVB      -1(src), tmp2
	MOVB      tmp2, -1(dst)

finishFours:
	TST       $4, len
	BEQ       copyLiteralDone
	MOVB.P    4(src), tmp1
	MOVB.P    tmp1, 4(dst)
	MOVB      -1(src), tmp2
	MOVB      tmp2, -1(dst)
	MOVB      -2(src), tmp1
	MOVB      tmp1, -2(dst)
	MOVB      -3(src), tmp2
	MOVB      tmp2, -3(dst)

copyLiteralDone:
	CMP src, srcend
	BEQ end

	// Initial part of match length.
	// This frees up the token register for reuse as offset.
	AND $15, token, len

	// Read offset.
	ADDS  $2, src
	BCS   shortSrc
	CMP   srcend, src
	BHI   shortSrc
	MOVBU -2(src), offset
	MOVBU -1(src), tmp1
	ORR   tmp1 << 8, offset
	CMP   $0, offset
	BEQ   corrupt

	// Read rest of match length.
	CMP $15, len
	BNE readMatchlenDone

readMatchlenLoop:
	CMP     src, srcend
	BEQ     shortSrc
	MOVBU.P 1(src), tmp1
	ADDS    tmp1, len
	BVS     shortDst
	CMP     $255, tmp1
	BEQ     readMatchlenLoop

readMatchlenDone:
	// Bounds check dst+len+minMatch.
	ADDS     dst, len, tmp1
	ADDS     $const_minMatch, tmp1
	BCS      shortDst
	CMP      dstend, tmp1
	BHI      shortDst

	SUB offset, dst, match
	CMP dstorig, match
	BGE copyMatch4

	// match < dstorig means the match starts in the dictionary,
	// at len(dict) - offset + (dst - dstorig).
	MOVD dict_base+48(FP), match
	MOVD dict_len +56(FP), dictend

	ADD $const_minMatch, len

	SUB   dstorig, dst, tmp1
	SUB   offset, dictend, tmp2
	ADDS  tmp2, tmp1
	BMI   shortDict
	ADD   match, dictend
	ADD   tmp1, match

copyDict:
	MOVBU.P 1(match), tmp1
	MOVB.P  tmp1, 1(dst)
	SUBS    $1, len
	BEQ     extends
	CMP     match, dictend
	BNE     copyDict

extends:
	// If the match extends beyond the dictionary, the rest is at dstorig.
	CMP  $0, len
	BEQ  copyMatchDone
	MOVD dstorig, match
	B    copyMatch

	// Copy a regular match.
	// Since len+minMatch is at least four, we can do a 4× unrolled
	// byte copy loop. Using MOVW instead of four byte loads is faster,
	// but to remain portable we'd have to align match first, which is
	// too expensive. By alternating loads and stores, we also handle
	// the case offset < 4.
copyMatch4:
	SUBS    $4, len
	MOVBU.P 4(match), tmp1
	MOVB.P  tmp1, 4(dst)
	MOVBU   -3(match), tmp2
	MOVB    tmp2, -3(dst)
	MOVBU   -2(match), tmp3
	MOVB    tmp3, -2(dst)
	MOVBU   -1(match), tmp1
	MOVB    tmp1, -1(dst)
	BPL     copyMatch4

	// Restore len, which is now negative.
	ADDS  $4, len
	BEQ   copyMatchDone

copyMatch:
	// Finish with a byte-at-a-time copy.
	SUBS    $1, len
	MOVBU.P 1(match), tmp2
	MOVB.P  tmp2, 1(dst)
	BNE     copyMatch

copyMatchDone:
	CMP src, srcend
	BNE loop

end:
	SUB  dstorig, dst, tmp1
	MOVD tmp1, ret+72(FP)
	RET

	// The error cases have distinct labels so we can put different
	// return codes here when debugging, or if the error returns need to
	// be changed.
shortDict:
	MOVD $-4, tmp1
	MOVD tmp1, ret+72(FP)
	RET
shortDst:
	MOVD $-3, tmp1
	MOVD tmp1, ret+72(FP)
	RET
shortSrc:
	MOVD $-2, tmp1
	MOVD tmp1, ret+72(FP)
	RET
corrupt:
	MOVD $-1, tmp1
	MOVD tmp1, ret+72(FP)
	RET
