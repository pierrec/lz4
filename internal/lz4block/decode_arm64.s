// +build gc
// +build !noasm

// This implementation assumes that strict alignment checking is turned off.
// The Go compiler makes the same assumption.

#include "go_asm.h"
#include "textflag.h"

// Register allocation.
#define dst		R0
#define dstorig		R1
#define src		R2
#define dstend		R3
#define dstend16	R4	// dstend - 16
#define srcend		R5
#define srcend16	R6	// srcend - 16
#define match		R7	// Match address.
#define dict		R8
#define dictlen		R9
#define dictend		R10
#define token		R11
#define len		R12	// Literal and match lengths.
#define lenRem		R13
#define offset		R14	// Match offset.
#define tmp1		R15
#define tmp2		R16
#define tmp3		R17
#define tmp4		R19
#define dstend32	R20	// dstend - 32 (shortcut guard)

// func decodeBlock(dst, src, dict []byte) int
TEXT ·decodeBlock(SB), NOFRAME+NOSPLIT, $0-80
	LDP  dst_base+0(FP), (dst, dstend)
	ADD  dst, dstend
	MOVD dst, dstorig

	LDP src_base+24(FP), (src, srcend)
	CBZ srcend, shortSrc
	ADD src, srcend

	// dstend16 = max(dstend-16, 0) and similarly for dstend32, srcend16.
	SUBS $16, dstend, dstend16
	CSEL LO, ZR, dstend16, dstend16
	SUBS $32, dstend, dstend32
	CSEL LO, ZR, dstend32, dstend32
	SUBS $16, srcend, srcend16
	CSEL LO, ZR, srcend16, srcend16

	LDP dict_base+48(FP), (dict, dictlen)
	ADD dict, dictlen, dictend

loop:
	// Read token. Extract literal length.
	MOVBU.P 1(src), token
	LSR     $4, token, len
	CMP     $15, len
	BEQ     readLitlenLoop        // len == 15: extended read, slow path.

	// Shortcut: literal length is 0..14. If we also have at least 32 bytes
	// of dst and 16 bytes of src remaining, copy 16 literal bytes in one
	// shot, then try to finish the token's match with an 18-byte copy.
	// Falls back to the slow path on any guard failure. Mirrors the
	// "copy shortcut" in decode_amd64.s.
	CMP dstend32, dst
	BHS readLitlenDone            // <32 bytes left in dst: slow path.
	CMP srcend16, src
	BHS readLitlenDone            // <16 bytes left in src: slow path.

	// 16-byte literal copy (bytes past len get overwritten next iter).
	LDP (src), (tmp1, tmp2)
	STP (tmp1, tmp2), (dst)
	ADD len, src
	ADD len, dst

	// Derive initial matchlen from token's low nibble.
	AND $15, token, len

	// Read 2-byte offset (src has >=2 bytes left by the guard above).
	MOVHU (src), offset
	ADD   $2, src
	CBZ   offset, corrupt

	// Fast-match preconditions: matchlen != 15, offset >= 8, match is
	// within the current block (>= dstorig -- not a dict reference).
	CMP $15, len
	BEQ readMatchlen              // extended matchlen: slow path.
	CMP $8, offset
	BLO readMatchlen              // small offset: use existing <8 path.
	SUB offset, dst, match
	CMP dstorig, match
	BLO readMatchlen              // dict reference: use existing dict path.

	// 18-byte match copy, sequenced 8+8+2 so that offset == 8 (common
	// 8-byte RLE) works correctly: each load observes the prior store's
	// effect. An LDP+STP would load both halves before any store retires,
	// corrupting the second half for offsets 8..15. dst-space is
	// guaranteed: dst < dstend-32 and matchlen+minMatch <= 18. Bytes past
	// matchlen+minMatch get overwritten next iter.
	MOVD  (match), tmp1
	MOVD  tmp1, (dst)
	MOVD  8(match), tmp2
	MOVD  tmp2, 8(dst)
	MOVHU 16(match), tmp3
	MOVH  tmp3, 16(dst)
	ADD   $const_minMatch, len
	ADD   len, dst
	B     copyMatchDone

readLitlenLoop:
	CMP     src, srcend
	BEQ     shortSrc
	MOVBU.P 1(src), tmp1
	ADDS    tmp1, len
	BVS     shortDst
	CMP     $255, tmp1
	BEQ     readLitlenLoop

readLitlenDone:
	CBZ len, copyLiteralDone

	// Bounds check dst+len and src+len.
	ADDS dst, len, tmp1
	BCS  shortSrc
	ADDS src, len, tmp2
	BCS  shortSrc
	CMP  dstend, tmp1
	BHI  shortDst
	CMP  srcend, tmp2
	BHI  shortSrc

	// Copy literal.
	SUBS $16, len
	BLO  copyLiteralShort

copyLiteralLoop:
	LDP.P 16(src), (tmp1, tmp2)
	STP.P (tmp1, tmp2), 16(dst)
	SUBS  $16, len
	BPL   copyLiteralLoop

	// Copy (final part of) literal of length 0-15.
	// If we have >=16 bytes left in src and dst, just copy 16 bytes.
copyLiteralShort:
	CMP  dstend16, dst
	CCMP LO, src, srcend16, $0b0010 // 0010 = preserve carry (LO).
	BHS  copyLiteralShortEnd

	AND $15, len

	LDP (src), (tmp1, tmp2)
	ADD len, src
	STP (tmp1, tmp2), (dst)
	ADD len, dst

	B copyLiteralDone

	// Safe but slow copy near the end of src, dst.
copyLiteralShortEnd:
	TBZ     $3, len, 3(PC)
	MOVD.P  8(src), tmp1
	MOVD.P  tmp1, 8(dst)
	TBZ     $2, len, 3(PC)
	MOVW.P  4(src), tmp2
	MOVW.P  tmp2, 4(dst)
	TBZ     $1, len, 3(PC)
	MOVH.P  2(src), tmp3
	MOVH.P  tmp3, 2(dst)
	TBZ     $0, len, 3(PC)
	MOVBU.P 1(src), tmp4
	MOVB.P  tmp4, 1(dst)

copyLiteralDone:
	// Initial part of match length.
	AND $15, token, len

	CMP src, srcend
	BEQ end

	// Read offset.
	ADDS  $2, src
	BCS   shortSrc
	CMP   srcend, src
	BHI   shortSrc
	MOVHU -2(src), offset
	CBZ   offset, corrupt

readMatchlen:
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
	ADD $const_minMatch, len

	// Bounds check dst+len.
	ADDS dst, len, tmp2
	BCS  shortDst
	CMP  dstend, tmp2
	BHI  shortDst

	SUB offset, dst, match
	CMP dstorig, match
	BHS copyMatchTry8

	// match < dstorig means the match starts in the dictionary,
	// at len(dict) - offset + (dst - dstorig).
	SUB  dstorig, dst, tmp1
	SUB  offset, dictlen, tmp2
	ADDS tmp2, tmp1
	BMI  shortDict
	ADD  dict, tmp1, match

copyDict:
	MOVBU.P 1(match), tmp3
	MOVB.P  tmp3, 1(dst)
	SUBS    $1, len
	CCMP    NE, dictend, match, $0b0100 // 0100 sets the Z (EQ) flag.
	BNE     copyDict

	CBZ len, copyMatchDone

	// If the match extends beyond the dictionary, the rest is at dstorig.
	// Recompute the offset for the next check.
	MOVD dstorig, match
	SUB  dstorig, dst, offset

copyMatchTry8:
	// Copy doublewords if both len and offset are at least eight.
	// A 16-at-a-time loop doesn't provide a further speedup.
	CMP  $8, len
	CCMP HS, offset, $8, $0
	BLO  copyMatchTry4

	AND    $7, len, lenRem
	SUB    $8, len
copyMatchLoop8:
	MOVD.P 8(match), tmp1
	MOVD.P tmp1, 8(dst)
	SUBS   $8, len
	BPL    copyMatchLoop8

	MOVD (match)(len), tmp2 // match+len == match+lenRem-8.
	ADD  lenRem, dst
	MOVD $0, len
	MOVD tmp2, -8(dst)
	B    copyMatchDone

copyMatchTry4:
	// Copy words if both len and offset are at least four.
	CMP  $4, len
	CCMP HS, offset, $4, $0
	BLO  copyMatchLoop1

	MOVWU.P 4(match), tmp2
	MOVWU.P tmp2, 4(dst)
	SUBS    $4, len
	BEQ     copyMatchDone

copyMatchLoop1:
	// For offset == 1 or 2 and len >= 8, splat the 1- or 2-byte pattern
	// into tmp3 and store 8 bytes at a time. The remaining 1..7 bytes,
	// plus offset == 3 and the len < 8 cases, fall through to the
	// byte-by-byte loop below. match is not advanced during the splat;
	// the byte loop's post-index read correctly observes the pattern
	// because match[k] for k >= offset sees the bytes we just splatted.
	CMP $8, len
	BLO copyMatchByteLoop         // len < 8: byte loop is shorter.
	CMP $2, offset
	BHI copyMatchByteLoop         // offset == 3: period doesn't tile 8B.
	BEQ copyMatchSplat2           // offset == 2

	// offset == 1: splat a single byte to all 8 bytes of tmp3.
	MOVBU (match), tmp3
	ORR   tmp3<<8, tmp3, tmp3
	ORR   tmp3<<16, tmp3, tmp3
	B     copyMatchSplatTile32

copyMatchSplat2:
	// offset == 2: splat a halfword.
	MOVHU (match), tmp3
	ORR   tmp3<<16, tmp3, tmp3

copyMatchSplatTile32:
	ORR tmp3<<32, tmp3, tmp3

copyMatchSplatLoop:
	MOVD.P tmp3, 8(dst)
	SUB    $8, len
	CMP    $8, len
	BHS    copyMatchSplatLoop

	CBZ len, copyMatchDone
	// fall through with 1..7 bytes remaining.

copyMatchByteLoop:
	// Byte-at-a-time copy for small offsets <= 3.
	MOVBU.P 1(match), tmp2
	MOVB.P  tmp2, 1(dst)
	SUBS    $1, len
	BNE     copyMatchByteLoop

copyMatchDone:
	CMP src, srcend
	BNE loop

end:
	CBNZ len, corrupt
	SUB  dstorig, dst, tmp1
	MOVD tmp1, ret+72(FP)
	RET

	// The error cases have distinct labels so we can put different
	// return codes here when debugging, or if the error returns need to
	// be changed.
shortDict:
shortDst:
shortSrc:
corrupt:
	MOVD $-1, tmp1
	MOVD tmp1, ret+72(FP)
	RET
