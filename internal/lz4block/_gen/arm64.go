package main

import "fmt"

// clamp sets to = max(from-n, 0).
func clamp(n int, from, to string) string {
	return fmt.Sprintf("\tSUBS $%d, %s, %s\n\tCSEL LO, ZR, %[3]s, %[3]s\n", n, from, to)
}

// loadArgs loads dstorig, src's base pointer into srcBase and dict, and
// computes the ends derived from them: dstend, srcend and dictend, and
// dstend16, dstend32 and srcend16 clamped at zero. srcCheck runs with
// srcend holding len(src).
func loadArgs(srcBase, srcCheck string) string {
	return fmt.Sprintf(`
	LDP dst_base+0(FP), (dstorig, dstend)
	ADD dstorig, dstend
	LDP src_base+24(FP), (%[1]s, srcend)
%[2]s	ADD %[1]s, srcend
`, srcBase, srcCheck) +
		clamp(16, "dstend", "dstend16") + clamp(32, "dstend", "dstend32") + clamp(16, "srcend", "srcend16") + `
	LDP dict_base+48(FP), (dict, dictlen)
	ADD dict, dictlen, dictend
`
}

// readLenExt adds the bytes of an extended length (255, ..., 255, <255)
// at src to len.
func readLenExt(a *asm, loop string) {
	a.F(`
%s:
	CMP     src, srcend
	BEQ     shortSrc
	MOVBU.P 1(src), tmp1
	ADDS    tmp1, len
	BVS     shortDst
	CMP     $255, tmp1
	BEQ     %[1]s`, loop)
}

// copyStep copies width (8 or 16) bytes from from to dst through r1 (and
// r2), advancing both.
func copyStep(width int, from, r1, r2 string) string {
	if width == 8 {
		return fmt.Sprintf("\tMOVD.P 8(%s), %s\n\tMOVD.P %[2]s, 8(dst)\n", from, r1)
	}
	return fmt.Sprintf("\tLDP.P 16(%s), (%s, %s)\n\tSTP.P (%[2]s, %[3]s), 16(dst)\n", from, r1, r2)
}

// copyLoop copies width bytes per iteration while n, counted down by
// width, stays non-negative.
func copyLoop(a *asm, loop string, width int, from, n, r1, r2 string) {
	a.F("%s:", loop)
	a.I(copyStep(width, from, r1, r2))
	a.F("\tSUBS $%d, %s\n\tBPL %s\n", width, n, loop)
}

// copyLast16 copies the 16 bytes ending at from+n+disp+16 to the 16 bytes
// ending at dst+adv, and advances dst by adv. clearLen zeroes len before
// the store.
func copyLast16(a *asm, from, n string, disp int, end, adv, r1, r2 string, clearLen bool) {
	ldDisp := ""
	if disp != 0 {
		ldDisp = fmt.Sprint(disp)
	}
	a.F("\tADD %s, %s, %s", from, n, end)
	a.F("\tLDP %s(%s), (%s, %s)", ldDisp, end, r1, r2)
	a.F("\tADD %s, dst", adv)
	if clearLen {
		a.I("\tMOVD $0, len")
	}
	a.F("\tSTP (%s, %s), -16(dst)", r1, r2)
}

// bulk copies n >= width bytes from from to dst, width (8 or 16) at a
// time, finishing with an overlapping copy of the last width bytes;
// lenRem is clobbered.
type bulk struct {
	width       int
	loop        string // loop label
	from, n     string
	r1, r2, end string // scratch registers; end is unused for width 8
	align       string // emitted just before the loop, if set
	clearLen    bool   // zero len as well (n is len)
}

func (c bulk) emit(a *asm) {
	a.F("\tAND $%d, %s, lenRem", c.width-1, c.n)
	a.F("\tSUB $%d, %s", c.width, c.n)
	if c.align != "" {
		a.I(c.align)
	}
	copyLoop(a, c.loop, c.width, c.from, c.n, c.r1, c.r2)
	if c.width == 8 {
		a.F("\tMOVD (%s)(%s), %s // %[1]s+%[2]s == %[1]s+lenRem-8.", c.from, c.n, c.r2)
		a.I("\tADD lenRem, dst")
		if c.clearLen {
			a.I("\tMOVD $0, len")
		}
		a.F("\tMOVD %s, -8(dst)", c.r2)
		return
	}
	a.F(`
	// LDP lacks a (base)(index) addressing mode, so compute %[1]s+%[2]s
	// into a scratch register first: %[3]s = %[1]s + lenRem - 16.`, c.from, c.n, c.end)
	copyLast16(a, c.from, c.n, 0, c.end, "lenRem", c.r1, c.r2, c.clearLen)
}

// copyTail15 copies the n&15 bytes at from to dst through r, testing one
// bit of n per power of two.
func copyTail15(a *asm, from, n, r string) {
	loads := [4]string{"MOVD.P", "MOVWU.P", "MOVHU.P", "MOVBU.P"}
	stores := [4]string{"MOVD.P", "MOVW.P", "MOVH.P", "MOVB.P"}
	for i, size := range []int{8, 4, 2, 1} {
		a.F("\tTBZ $%d, %s, 3(PC)", 3-i, n)
		a.F("\t%s %d(%s), %s", loads[i], size, from, r)
		a.F("\t%s %s, %d(dst)", stores[i], r, size)
	}
}

// storeRun stores the 16-byte pattern (r1, r2) at dst until fewer than 16
// bytes of len remain: 64 bytes per iteration while it can, then 16. It
// continues at tail.
func storeRun(a *asm, loop64, loop16, tail, r1, r2 string) {
	a.F("\tCMP $64, len\n\tBLO %s\n", loop16)
	a.F("%s:", loop64)
	for _, off := range []string{"", "16", "32", "48"} {
		a.F("\tSTP (%s, %s), %s(dst)", r1, r2, off)
	}
	a.F(`
	ADD $64, dst
	SUB $64, len
	CMP $64, len
	BHS %s

%s:
	CMP   $16, len
	BLO   %s
	STP.P (%s, %s), 16(dst)
	SUB   $16, len
	B     %[2]s`, loop64, loop16, tail, r1, r2)
}

// tileLoop stores a tile of width bytes at dst (the stores given) every
// stride bytes while at least width bytes of len remain, then continues at
// copyMatchTileTail. doc, if set, follows the label.
func tileLoop(a *asm, loop, doc string, width int, stride, stores string) {
	a.F("%s:", loop)
	if doc != "" {
		a.I(doc)
	}
	a.F(`
	CMP $%d, len
	BLO copyMatchTileTail`, width)
	a.I(stores)
	a.F(`
	ADD %s, dst
	SUB %[1]s, len
	B   %s`, stride, loop)
}

// memmoveCall copies n bytes from from to dst with runtime·memmove, then
// continues at next.
type memmoveCall struct {
	from, n string
	advance []string // pointers to advance by n
	spill   string   // a third register to keep across the call
	after   string   // instructions to run after the reload
	next    string
}

func (c memmoveCall) emit(a *asm) {
	a.F(`
	MOVD dst, 8(RSP)  // memmove arg0: to
	MOVD %s, 16(RSP) // memmove arg1: from
	MOVD %s, 24(RSP) // memmove arg2: n`, c.from, c.n)
	for _, r := range c.advance {
		a.F("\tADD %s, %s", c.n, r)
	}
	a.I("\tMOVD dst, 32(RSP)\n\tMOVD src, 40(RSP)")
	if c.spill != "" {
		a.F("\tMOVD %s, 48(RSP)", c.spill)
	}
	a.I("\tBL runtime·memmove(SB)\n\tMOVD 32(RSP), dst\n\tMOVD 40(RSP), src")
	if c.spill != "" {
		a.F("\tMOVD 48(RSP), %s", c.spill)
	}
	a.I("\tRELOAD_ENDS")
	if c.after != "" {
		a.I(c.after)
	}
	a.F("\tB %s", c.next)
}

func decodeARM64(a *asm) {
	a.I(`
//go:build gc && !noasm

// This implementation assumes that strict alignment checking is turned off.
// The Go compiler makes the same assumption.

#include "go_asm.h"
#include "textflag.h"

// Register allocation.
#define dst		R0
#define dstorig		R1
#define src		R2
#define dstend		R3
#define dstend16	R4 // dstend - 16
#define srcend		R5
#define srcend16	R6 // srcend - 16
#define match		R7 // Match address.
#define dict		R8
#define dictlen		R9
#define dictend		R10
#define token		R11
#define len		R12 // Literal and match lengths.
#define lenRem		R13
#define offset		R14 // Match offset.
#define tmp1		R15
#define tmp2		R16
#define tmp3		R17
#define tmp4		R19
#define dstend32	R20 // dstend - 32 (shortcut guard)

// Reload the registers derived from the arguments after a call to
// runtime·memmove, which may clobber all of them.`)
	a.macro("RELOAD_ENDS", loadArgs("tmp1", ""))

	a.I(`
// func decodeBlock(dst, src, dict []byte) int
//
// Frame: 56 bytes for calling runtime·memmove for long literals, dictionary
// copies and non-overlapping matches (arg0..arg2 at 8/16/24(RSP), spills at
// 32..48(RSP)). NOSPLIT is preserved -- memmove's own stack use is well under
// the nosplit margin.
TEXT ·decodeBlock(SB), NOSPLIT, $56-80`)
	a.I(loadArgs("src", "\tCBZ srcend, shortSrc\n"))
	a.I(`
	MOVD dstorig, dst

loop:
	// Read token; >= 0xF0 means literal length 15, slow path.
	MOVBU.P 1(src), token
	CMP     $0xF0, token
	BHS     readLitlenExt

	// Shortcut: literal length is 0..14. If we also have at least 32 bytes
	// of dst and 16 bytes of src remaining, copy 16 literal bytes in one
	// shot, then try to finish the token's match with an 18-byte copy.
	// Falls back to the slow path on any guard failure. Mirrors the
	// "copy shortcut" in decode_amd64.s.
	CMP  dstend32, dst
	CCMP LO, src, srcend16, $0b0010 // dst >= dstend-32: 0010 sets C (HS).
	BHS  readLitlenShort            // <32 bytes left in dst or <16 in src: slow path.

	// 16-byte literal copy (bytes past the literal get overwritten next iter).
	LDP (src), (tmp1, tmp2)
	STP (tmp1, tmp2), (dst)
	ADD token>>4, src, src
	ADD token>>4, dst, dst

	// Initial matchlen from the token's low nibble, then the 2-byte
	// offset (src has >= 2 bytes left by the guard above).
	AND     $15, token, len
	MOVHU.P 2(src), offset

	// Fast-match preconditions: matchlen != 15, offset >= 8, match is
	// within the current block (>= dstorig -- not a dict reference).
	// Branches, not a CCMP chain: matchlen == 15 data must leave at the
	// first test. offset == 0 fails offset >= 8 and is rejected at
	// readMatchlenChk.
	CMP $15, len
	BEQ readMatchlenChk
	CMP $8, offset
	BLO readMatchlenChk
	SUB offset, dst, match
	CMP dstorig, match
	BLO readMatchlenChk

	// 18-byte match copy as sequenced 8+8+2 (like the C decoder).
	// dst-space is guaranteed: dst < dstend-32 and matchlen+minMatch
	// <= 18; bytes past the match get overwritten next iter. 8-byte
	// loads are more likely than an LDP to be forwarded from the
	// in-flight stores of the previous sequences (Neoverse), and the
	// sequencing keeps offset == 8 cycling correct.
	MOVD  (match), tmp1
	MOVD  tmp1, (dst)
	MOVD  8(match), tmp2
	MOVD  tmp2, 8(dst)
	MOVHU 16(match), tmp3
	MOVH  tmp3, 16(dst)
	ADD   $const_minMatch, len
	ADD   len, dst

	// src < srcend: the guard left >= 17 bytes, the shortcut used <= 16.
	B loop

readLitlenShort:
	LSR $4, token, len
	B   readLitlenDone

readLitlenExt:
	MOVD $15, len
`)
	readLenExt(a, "readLitlenLoop")
	a.I(`
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

	// Copy literal. Long ones go to memmove, which copies 64 bytes per
	// iteration.
	CMP  $256, len
	BHS  copyLiteralMemmove
	SUBS $16, len
	BLO  copyLiteralShort

`)
	copyLoop(a, "copyLiteralLoop", 16, "src", "len", "tmp1", "tmp2")
	a.I(`

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
copyLiteralShortEnd:`)
	copyTail15(a, "src", "len", "tmp1")
	a.I(`
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

readMatchlenChk:
	CBZ offset, corrupt

readMatchlen:
	// Read rest of match length.
	CMP $15, len
	BNE readMatchlenDone
`)
	readLenExt(a, "readMatchlenLoop")
	a.I(`
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
	B    copyDict

copyDictDone:
	CBZ len, copyMatchDone

	// If the match extends beyond the dictionary, the rest is at dstorig.
	// Recompute the offset for the next check.
	MOVD dstorig, match
	SUB  dstorig, dst, offset

copyMatchTry8:
	// Non-overlapping bulk copy: len >= 256 and offset >= len means the
	// whole match can be served by runtime.memmove (scalar 64B/iter
	// LDP/STP with source alignment), which outruns the 16B/iter loop
	// below for long copies. Below 256 bytes the call-and-spill
	// overhead dominates, so the inline loops stay.
	CMP $256, len
	BLO copyMatchTry8_inline
	CMP len, offset
	BLO copyMatchTry8_inline // offset < len -> match cycles, can't memmove.
	B   copyMatchViaMemmove

copyMatchTry8_inline:
	// Copy quadwords (16 bytes/iter via LDP/STP) if len and offset are both
	// large enough. offset >= 32 guarantees there is no store-to-load
	// forwarding dependency between iterations: iter N+1's LDP reads bytes
	// at (match+16) which, since match = dst - offset, sits at
	// dst - (offset-16). For offset >= 32 that is pre-dst, untouched by
	// iter N's STP. In columnar and record-oriented data, most long matches
	// have offsets of 32 or more and carry most of the match-copy bytes, so
	// this is the dominant path there.
	// CCMP immediate is 5-bit unsigned (0..31); we can't encode $32 directly,
	// so compare offset against $31 with BLS (lower or same) to match
	// "offset < 32" exactly. The first-CMP "len < 16" case falls into BLS
	// via NZCV=$0 (C=0 => LS).
	CMP  $16, len
	CCMP HS, offset, $31, $0
	BLS  copyMatchTry8Narrow

	// Long overlapping match: see copyMatchFar.
	CMP $256, len
	BLO copyMatchLoop16Setup
	CMP $(64<<10), len
	BLS copyMatchFar

copyMatchLoop16Setup:`)
	bulk{
		width: 16, loop: "copyMatchLoop16", from: "match", n: "len",
		r1: "tmp1", r2: "tmp2", end: "tmp3",
		align: `
	// Keep this loop aligned so that code added above cannot shift it: its
	// placement alone moved N1 and Graviton 4 by 7-25% on long matches.
	PCALIGN $32`,
		clearLen: true,
	}.emit(a)
	a.I(`
	B copyMatchDone

copyMatchTry8Narrow:
	// Offsets in [8, 32) (or any offset >= 8 via the dict remainder
	// path). Long matches take the load-free tiles; short ones the
	// 8-byte loop.
	CMP  $8, len
	CCMP HS, offset, $8, $0
	BLO  copyMatchTry4

	CMP $32, len
	BLO copyMatchLoop8Setup // short match: 8-byte loop.

	// offset is 8..31 here (entered with len < 16 or offset <= 31).
	CMP $8, offset
	BEQ copyMatchTile8
	CMP $16, offset
	BEQ copyMatchTile16
	BLO copyMatchTile       // 9..15: generic 16-byte tile (prefill = offset).
	CMP $24, offset
	BEQ copyMatchTile24
	B   copyMatchTile32     // 17..23, 25..31: 32-byte tile.

copyMatchLoop8Setup:
	// 8-byte loop, store-to-load-forwarding bound; fine for short matches.`)
	bulk{
		width: 8, loop: "copyMatchLoop8", from: "match", n: "len",
		r1: "tmp1", r2: "tmp2", clearLen: true,
	}.emit(a)
	a.I(`
	B copyMatchDone

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
	// For offset <= 2 and len >= 8, splat the 1- or 2-byte pattern
	// and store 8-16 bytes at a time. Offsets 3..7 with enough length
	// go to their own splat/tile paths (reachable only with offset < 8:
	// len >= 8 at this point implies the offset >= 8 cases went through
	// copyMatchTry8Narrow). Everything else falls to the byte loop.
	CMP $8, len
	BLO copyMatchByteLoop  // len < 8: byte loop is shorter.
	CMP $2, offset
	BHI copyMatchMidPeriod // offsets 3..7.
	BEQ copyMatchSplat2    // offset == 2

	// offset == 1: splat a single byte to all 8 bytes of tmp3.
	MOVBU (match), tmp3
	ORR   tmp3<<8, tmp3, tmp3
	ORR   tmp3<<16, tmp3, tmp3
	B     copyMatchSplatTile32

copyMatchMidPeriod:
	// offsets 3..7 with len >= 8.
	CMP $4, offset
	BEQ copyMatchSplat4
	CMP $16, len
	BHS copyMatchTile
	B   copyMatchByteLoop

copyMatchSplat4:
	// offset == 4: splat a word to all 8 bytes of tmp3, then reuse the
	// 16-byte splat store loop below.
	MOVWU (match), tmp3
	B     copyMatchSplatTile32

copyMatchTile:
	// offsets 3, 5, 6, 7 with len >= 16. The output is periodic with
	// period == offset. Prefill step = (16/offset)*offset bytes (12..15,
	// the largest multiple of offset <= 16) byte-by-byte, which makes
	// [match0, dst) hold >= 16 pattern bytes and leaves dst phase-aligned
	// to the pattern start. Then load one 16-byte tile from match0 and
	// store it repeatedly, advancing dst by step so the phase is
	// preserved. The loop has no loads, so unlike an overlapped-copy
	// loop it incurs no store-to-load-forwarding stalls.
	MOVD $16, tmp1
	UDIV offset, tmp1, tmp2
	MUL  offset, tmp2, tmp4 // tmp4 = step
	MOVD match, lenRem      // lenRem = match0, the tile source.
	MOVD tmp4, tmp1         // tmp1 = prefill byte count.

copyMatchTilePrefill:
	MOVBU.P 1(match), tmp3
	MOVB.P  tmp3, 1(dst)
	SUBS    $1, tmp1
	BNE     copyMatchTilePrefill

	SUB tmp4, len
	LDP (lenRem), (tmp1, tmp2) // 16-byte pattern tile.

copyMatchTileLoop:
	// While at least 64 bytes remain, store four tiles per iteration
	// (4*step <= 64 bytes; the last store ends at most 3*step+16 <= 61
	// bytes on): long runs are bound by stores, not by loop overhead.
	CMP $64, len
	BLO copyMatchTileLoop1

copyMatchTileLoop4:
	STP (tmp1, tmp2), (dst)
	ADD tmp4, dst, tmp3
	STP (tmp1, tmp2), (tmp3)
	ADD tmp4, tmp3
	STP (tmp1, tmp2), (tmp3)
	ADD tmp4, tmp3
	STP (tmp1, tmp2), (tmp3)
	ADD tmp4, tmp3, dst
	SUB tmp4<<2, len
	CMP $64, len
	BHS copyMatchTileLoop4
`)
	tileLoop(a, "copyMatchTileLoop1", `
	// Store 16 bytes while at least 16 remain, consuming step bytes per
	// iteration; the 16-step overlap is rewritten by the next store (or
	// the tail loop) with identical values.`, 16, "tmp4", "\tSTP (tmp1, tmp2), (dst)")
	a.I(`
copyMatchTileTail:
	CBZ len, copyMatchDone
	SUB offset, dst, match // Re-derive match for the tail copy.
	CMP $8, len
	BLO copyMatchByteLoop
	B   copyMatchTry8Narrow

	// Load-free tiles for offsets 8..31 with len >= 32 (constant or
	// short-period columns): load the pattern once, then only store.
copyMatchTile8:
	// The splat loop consumes multiples of 8, so dst stays phase-aligned
	// and its byte-loop tail (reading from the un-advanced match) is right.
	MOVD (match), tmp3
	B    copyMatchSplatLoop

copyMatchTile16:
	LDP (match), (tmp1, tmp2)`)
	storeRun(a, "copyMatchTile16Loop64", "copyMatchTile16Loop", "copyMatchTileTail", "tmp1", "tmp2")
	a.I(`
copyMatchTile24:
	// Dedicated tile: the generic 32-byte tile would overlap its stores
	// by 8 bytes at this stride, which is markedly slower.
	LDP  (match), (tmp1, tmp2)
	MOVD 16(match), tmp3`)
	tileLoop(a, "copyMatchTile24Loop", "", 24, "$24", "\tSTP (tmp1, tmp2), (dst)\n\tMOVD tmp3, 16(dst)")
	a.I(`
copyMatchTile32:
	// 17..23, 25..31: prefill one period with two 16-byte copies (bytes
	// 0..15 from match, offset-16..offset-1 from before dst), then store
	// the 32-byte tile at match every offset bytes.
	LDP (match), (tmp1, tmp2)
	LDP -16(dst), (tmp3, tmp4)
	STP (tmp1, tmp2), (dst)
	SUB $16, offset, lenRem
	ADD lenRem, dst, lenRem    // dst + offset - 16
	STP (tmp3, tmp4), (lenRem)
	ADD offset, dst
	SUB offset, len
	LDP 16(match), (tmp3, tmp4)`)
	tileLoop(a, "copyMatchTile32Loop", "", 32, "offset", "\tSTP (tmp1, tmp2), (dst)\n\tSTP (tmp3, tmp4), 16(dst)")
	a.I(`
copyMatchSplat2:
	// offset == 2: splat a halfword.
	MOVHU (match), tmp3
	ORR   tmp3<<16, tmp3, tmp3

copyMatchSplatTile32:
	ORR tmp3<<32, tmp3, tmp3

	// 16-byte store-pair loop for the bulk. G2 onwards (Neoverse N1 and
	// every later core, and Apple M-series) can retire an STP of two
	// X-registers as a single 16-byte store; doubling the store width
	// halves the iteration count and the per-iteration loop overhead.
	//
	// PCALIGN bumps decodeBlock's own alignment to 64B, fixing a 2-3% M4
	// regression the tile paths above caused in offset 1-7 code below by
	// shifting it off-alignment.
	PCALIGN $64

copyMatchSplatLoop:
	// 64 bytes per iteration while at least 64 remain (runs of zeros).`)
	storeRun(a, "copyMatchSplatLoop64", "copyMatchSplatLoop16", "copyMatchSplatTail", "tmp3", "tmp3")
	a.I(`
copyMatchSplatTail:
	// 0..15 bytes remain. If >= 8, emit one more 8-byte store.
	CBZ    len, copyMatchDone
	CMP    $8, len
	BLO    copyMatchByteLoop
	MOVD.P tmp3, 8(dst)
	SUBS   $8, len
	BEQ    copyMatchDone

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

	B end

copyMatchFar:
	// Overlapping match with offset >= 32 and 256 <= len <= 64KiB. Loads
	// from dst-offset would wait on the stores of the previous iterations,
	// so first grow the copy distance: with P bytes of pattern before dst
	// (P a multiple of the offset), copying [dst-P, dst) to dst doubles it.
	// From P >= 512 on, stream 64 bytes per iteration from dst-P, bytes
	// stored long before. Longer matches, which stream to memory, keep the
	// 16-byte loop: Neoverse cores stop caching lines written in a store
	// stream, so reading back bytes stored kilobytes earlier goes to DRAM,
	// while the loop's loads from dst-offset hit the store buffer.
	MOVD offset, tmp4

copyMatchGrow:
	// tmp4 = P >= 32, len > 0.
	CMP  $512, tmp4
	BHS  copyMatchStream
	CMP  tmp4, len
	BLS  copyMatchStream
	SUB  tmp4, dst, match
	MOVD tmp4, tmp3

copyMatchGrowLoop:`)
	a.I(copyStep(16, "match", "tmp1", "tmp2"))
	a.I(`
	SUB $16, tmp3
	CMP $16, tmp3
	BHS copyMatchGrowLoop

	// 0..15 left: the last 16 bytes of the source, which ends at the old dst.`)
	copyLast16(a, "match", "tmp3", -16, "lenRem", "tmp3", "tmp1", "tmp2", false)
	a.I(`
	SUB tmp4, len
	LSL $1, tmp4
	B   copyMatchGrow

copyMatchStream:
	// P >= 512, or len <= P: all loads are below dst. match starts at the
	// beginning of the pattern, so the end-aligned last copy below needs
	// 16 bytes behind it: under 16 bytes left at the start, copy bytes.
	SUB tmp4, dst, match
	CMP $16, len
	BLO copyMatchByteLoop
	CMP $64, len
	BLO copyMatchStreamTail

copyMatchStream64:
	// 64 bytes per iteration through two Q-register pairs.
	FLDPQ (match), (F0, F1)
	FLDPQ 32(match), (F2, F3)
	FSTPQ (F0, F1), (dst)
	FSTPQ (F2, F3), 32(dst)
	ADD   $64, match
	ADD   $64, dst
	SUB   $64, len
	CMP   $64, len
	BHS   copyMatchStream64

copyMatchStreamTail:
	CMP $16, len
	BLS copyMatchStreamLast`)
	a.I(copyStep(16, "match", "tmp1", "tmp2"))
	a.I(`
	SUB $16, len
	B   copyMatchStreamTail

copyMatchStreamLast:`)
	copyLast16(a, "match", "len", -16, "tmp3", "len", "tmp1", "tmp2", true)
	a.I(`
	B copyMatchDone

copyMatchViaMemmove:
	// runtime·memmove(dst, match, len). Caller must guarantee offset >= len
	// (non-overlapping).
	//
	// Go's arm64 ABI0 places a callee's args at caller_SP + 8 (the 0
	// offset is reserved for the callee's LR save slot), so arg0..arg2 go
	// at 8/16/24(RSP) from our POV. Unlike the C ABI, Go's has no
	// callee-saved registers: memmove (reached through an ABI wrapper) may
	// clobber every register but RSP, g and the frame pointer. So every
	// call site spills dst, src and at most one more register to 32..48(RSP)
	// and RELOAD_ENDS rebuilds the registers derived from the arguments.
	// This code never writes g (R28), R18 or REGTMP (R27).`)
	memmoveCall{from: "match", n: "len", advance: []string{"dst"},
		after: "\tMOVD $0, len", next: "copyMatchDone"}.emit(a)
	a.I(`
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

	// Out-of-line blocks.
copyLiteralMemmove:
	// runtime·memmove(dst, src, len); see copyMatchViaMemmove.`)
	memmoveCall{from: "src", n: "len", advance: []string{"dst", "src"},
		spill: "token", next: "copyLiteralDone"}.emit(a)
	a.I(`
copyDict:
	// Copy tmp1 = min(len, dictend-match) >= 1 bytes from the dictionary,
	// which does not overlap dst. Loads stay within the dictionary.
	SUB  match, dictend, tmp1
	CMP  tmp1, len
	CSEL LO, len, tmp1, tmp1
	SUB  tmp1, len
	CMP  $256, tmp1
	BHS  copyDictMemmove
	CMP  $16, tmp1
	BLO  copyDictShort

`)
	bulk{
		width: 16, loop: "copyDictLoop16", from: "match", n: "tmp1",
		r1: "tmp2", r2: "tmp3", end: "tmp4",
	}.emit(a)
	a.I(`
	B copyDictDone

copyDictShort:`)
	copyTail15(a, "match", "tmp1", "tmp2")
	a.I(`

	B copyDictDone

copyDictMemmove:
	// runtime·memmove(dst, match, tmp1); see copyMatchViaMemmove.`)
	memmoveCall{from: "match", n: "tmp1", advance: []string{"dst"},
		spill: "len", next: "copyDictDone"}.emit(a)
}
