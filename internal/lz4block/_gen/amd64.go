package main

// amd64ReadLenExt adds the bytes of an extended length (255, ..., 255,
// <255) at SI to CX.
func amd64ReadLenExt(a *asm, loop string) {
	a.F(`
%s:
	CMPQ    SI, R9
	JAE     err_short_buf
	MOVBLZX (SI), BX
	INCQ    SI
	ADDQ    BX, CX
	CMPB    BX, $0xFF
	JE      %[1]s`, loop)
}

// amd64ReloadArgs rebuilds the registers derived from the arguments after
// a call to runtime·memmove.
func amd64ReloadArgs(a *asm) {
	a.I(`
	// Recompute the registers derived from the arguments.
	MOVQ dst_base+0(FP), R8
	MOVQ R8, R11
	ADDQ dst_len+8(FP), R8
	MOVQ src_base+24(FP), R9
	ADDQ src_len+32(FP), R9
	MOVQ dict_base+48(FP), R14
	MOVQ dict_len+56(FP), R15
	MOVQ R8, R12
	SUBQ $32, R12
	MOVQ R9, R13
	SUBQ $16, R13`)
}

// amd64Memmove calls runtime·memmove(DI, from, n). The call may clobber
// every register, so DI and SI are spilled, advanced by n first if listed
// in advance, along with spill if set (only its low 32 bits if spill32);
// reloadN reloads n from the argument slot afterwards.
type amd64Memmove struct {
	from, n string
	advance []string
	spill   string
	spill32 bool
	reloadN bool
}

func (c amd64Memmove) emit(a *asm) {
	a.F(`
	// memmove(to, from, len)
	MOVQ DI, 0(SP)
	MOVQ %s, 8(SP)
	MOVQ %s, 16(SP)
`, c.from, c.n)
	for _, r := range c.advance {
		a.F("\tADDQ %s, %s", c.n, r)
	}
	a.I("\tMOVQ DI, 24(SP)\n\tMOVQ SI, 32(SP)")
	mov := "MOVQ"
	if c.spill32 {
		mov = "MOVL"
	}
	if c.spill != "" {
		a.F("\t%s %s, 40(SP)", mov, c.spill)
	}
	a.I("\n\tCALL runtime·memmove(SB)\n")
	if c.reloadN {
		a.F("\tMOVQ 16(SP), %s", c.n)
	}
	a.I("\tMOVQ 24(SP), DI\n\tMOVQ 32(SP), SI")
	if c.spill != "" {
		a.F("\t%s 40(SP), %s", mov, c.spill)
	}
	amd64ReloadArgs(a)
}

// amd64ByteLoop copies CX > 0 bytes from BX to DI one at a time through AX.
func amd64ByteLoop(a *asm, loop string) {
	a.F(`
%s:
	MOVB (BX), AX
	MOVB AX, (DI)
	INCQ DI
	INCQ BX
	DECQ CX
	JNZ  %[1]s`, loop)
}

// amd64Splat ends a splat setup that left a 16-byte tile in X0: the tile
// loop stores it every 16 bytes.
func amd64Splat(a *asm, label, setup string) {
	a.F("\n%s:", label)
	a.I(setup)
	a.I("\tMOVQ $16, R10\n\tJMP  copy_match_tile_loop")
}

// amd64TileTail stores the tile in regs (16 bytes each) once more if it
// fits in dst, covering the 0..width-1 bytes left in CX; otherwise the
// bytes are copied one at a time.
func amd64TileTail(a *asm, regs ...string) {
	a.F(`
	TESTQ CX, CX
	JZ    loopcheck
	MOVQ  R8, AX
	SUBQ  DI, AX
	CMPQ  AX, $%d
	JB    copy_match_tail_bytes`, 16*len(regs))
	for i, r := range regs {
		if i == 0 {
			a.F("\tMOVOU %s, (DI)", r)
		} else {
			a.F("\tMOVOU %s, %d(DI)", r, 16*i)
		}
	}
	a.I(`
	ADDQ CX, DI
	XORL CX, CX
	JMP  loopcheck`)
}

// amd64Run16 copies CX >= 16 bytes from BX to DI, 16 at a time, the last
// copy ending exactly at DI+CX, and continues at loopcheck. preload loads
// that last chunk before the loop, which is only right if the source does
// not overlap the destination.
func amd64Run16(a *asm, loop string, preload bool) {
	if preload {
		a.I("\tMOVOU -16(BX)(CX*1), X1 // tail; the match does not overlap, so load it up front")
	}
	a.I("\tLEAQ -16(DI)(CX*1), R10")
	if !preload {
		a.I("\tLEAQ -16(BX)(CX*1), AX")
	}
	a.F(`
%s:
	MOVOU (BX), X0
	MOVOU X0, (DI)
	ADDQ  $16, BX
	ADDQ  $16, DI
	CMPQ  DI, R10
	JB    %[1]s
`, loop)
	if !preload {
		a.I("\tMOVOU (AX), X1")
	}
	a.I(`
	MOVOU X1, (R10)
	LEAQ  16(R10), DI
	XORL  CX, CX
	JMP   loopcheck`)
}

// amd64AVX2Stream copies 64 bytes per iteration with AVX2 while at least
// 64 are left, then continues at copy_match_stream_tail. prefetchW issues
// PREFETCHW 512(DI) each iteration.
func amd64AVX2Stream(a *asm, loop string, prefetchW bool) {
	a.F("\tPCALIGN $64\n\n%s:", loop)
	if prefetchW {
		a.I(`
	// PREFETCHW 512(DI), which the Go assembler does not know. Only
	// reached when hasPrefetchW is set.
	BYTE $0x0f; BYTE $0x0d; BYTE $0x8f
	BYTE $0x00; BYTE $0x02; BYTE $0x00; BYTE $0x00`)
	}
	a.F(`
	VMOVDQU (BX), Y0
	VMOVDQU 32(BX), Y1
	VMOVDQU Y0, (DI)
	VMOVDQU Y1, 32(DI)
	ADDQ    $64, BX
	ADDQ    $64, DI
	SUBQ    $64, CX
	CMPQ    CX, $64
	JAE     %s

	// The tail and the rest of the decoder use legacy SSE encodings.
	VZEROUPPER
	JMP copy_match_stream_tail
`, loop)
}

// amd64TileStep emits tileStep[offset] = (16/offset)*offset, the prefill
// and store step of copy_match_tile, for offsets 0..15.
func amd64TileStep(a *asm) {
	var b [16]byte
	for off := 1; off < 16; off++ {
		b[off] = byte(16 / off * off)
	}
	a.I("\n// tileStep[offset] = (16/offset)*offset for offsets 3, 5, 6, 7, 9..15.")
	for i := 0; i < 16; i += 8 {
		var v uint64
		for j := 7; j >= 0; j-- {
			v = v<<8 | uint64(b[i+j])
		}
		a.F("DATA tileStep<>+%d(SB)/8, $0x%016x", i, v)
	}
	a.I("GLOBL tileStep<>(SB), RODATA|NOPTR, $16")
}

func decodeAMD64(a *asm) {
	a.I(`
//go:build gc && !noasm && !purego

#include "go_asm.h"
#include "textflag.h"

// Stream copies at least this long prefetch for ownership (see
// copy_match_stream64_avx2).
#define AVX2_PREFETCH_MIN 49152

// AX scratch
// BX scratch, match pointer
// CX literal and match lengths
// DX token, match offset
// R10 scratch (match copy)
// X0, X1 scratch (match copy)
//
// DI &dst
// SI &src
// R8 &dst + len(dst)
// R9 &src + len(src)
// R11 &dst
// R12 short output end
// R13 short input end
// R14 &dict
// R15 len(dict)

// func decodeBlock(dst, src, dict []byte) int
TEXT ·decodeBlock(SB), NOSPLIT, $48-80
	MOVQ dst_base+0(FP), DI
	MOVQ DI, R11
	MOVQ dst_len+8(FP), R8
	ADDQ DI, R8

	MOVQ src_base+24(FP), SI
	MOVQ src_len+32(FP), R9
	CMPQ R9, $0
	JE   err_corrupt
	ADDQ SI, R9

	MOVQ dict_base+48(FP), R14
	MOVQ dict_len+56(FP), R15

	// shortcut ends
	// short output end
	MOVQ R8, R12
	SUBQ $32, R12

	// short input end
	MOVQ R9, R13
	SUBQ $16, R13

	XORL CX, CX

loop:
	// token := uint32(src[si])
	MOVBLZX (SI), DX
	INCQ    SI

	// lit_len = token >> 4
	// if lit_len > 0
	// CX = lit_len
	MOVL DX, CX
	SHRL $4, CX

	// if lit_len != 0xF
	CMPL CX, $0xF
	JEQ  lit_len_loop
	CMPQ DI, R12
	JAE  copy_literal
	CMPQ SI, R13
	JAE  copy_literal

	// copy shortcut

	// A two-stage shortcut for the most common case:
	// 1) If the literal length is 0..14, and there is enough space,
	// enter the shortcut and copy 16 bytes on behalf of the literals
	// (in the fast mode, only 8 bytes can be safely copied this way).
	// 2) Further if the match length is 4..18, copy 18 bytes in a similar
	// manner; but we ensure that there's enough space in the output for
	// those 18 bytes earlier, upon entering the shortcut (in other words,
	// there is a combined check for both stages).

	// copy literal
	MOVOU (SI), X0
	MOVOU X0, (DI)
	ADDQ  CX, DI
	ADDQ  CX, SI

	MOVL DX, CX
	ANDL $0xF, CX

	// The second stage: prepare for match copying, decode full info.
	// If it doesn't work out, the info won't be wasted.
	// offset := uint16(data[:2])
	MOVWLZX (SI), DX
	TESTL   DX, DX
	JE      err_corrupt
	ADDQ    $2, SI
	JC      err_short_buf

	MOVQ DI, AX
	SUBQ DX, AX
	JC   err_corrupt
	CMPQ AX, DI
	JA   err_short_buf

	// if we can't do the second stage then jump straight to read the
	// match length, we already have the offset.
	CMPL CX, $0xF
	JEQ  match_len_loop_pre
	CMPL DX, $8
	JLT  match_len_loop_pre
	CMPQ AX, R11
	JB   match_len_loop_pre

	// memcpy(op + 0, match + 0, 8);
	MOVQ (AX), BX
	MOVQ BX, (DI)

	// memcpy(op + 8, match + 8, 8);
	MOVQ 8(AX), BX
	MOVQ BX, 8(DI)

	// memcpy(op +16, match +16, 2);
	MOVW 16(AX), BX
	MOVW BX, 16(DI)

	LEAQ const_minMatch(DI)(CX*1), DI

	// shortcut complete, load next token
	JMP loopcheck

	// Read the rest of the literal length:
	// do { BX = src[si++]; lit_len += BX } while (BX == 0xFF).`)
	amd64ReadLenExt(a, "lit_len_loop")
	a.I(`
copy_literal:
	// bounds check src and dst
	MOVQ SI, AX
	ADDQ CX, AX
	JC   err_short_buf
	CMPQ AX, R9
	JA   err_short_buf

	MOVQ DI, BX
	ADDQ CX, BX
	JC   err_short_buf
	CMPQ BX, R8
	JA   err_short_buf

	// Copy literals of <=48 bytes through the XMM registers.
	CMPQ CX, $48
	JGT  memmove_lit

	// if len(dst[di:]) < 48
	MOVQ R8, AX
	SUBQ DI, AX
	CMPQ AX, $48
	JLT  memmove_lit

	// if len(src[si:]) < 48
	MOVQ R9, BX
	SUBQ SI, BX
	CMPQ BX, $48
	JLT  memmove_lit

	MOVOU (SI), X0
	MOVOU 16(SI), X1
	MOVOU 32(SI), X2
	MOVOU X0, (DI)
	MOVOU X1, 16(DI)
	MOVOU X2, 32(DI)

	ADDQ CX, SI
	ADDQ CX, DI

	JMP finish_lit_copy

memmove_lit:`)
	amd64Memmove{from: "SI", n: "CX", advance: []string{"DI", "SI"},
		spill: "DX", spill32: true}.emit(a)
	a.I(`
finish_lit_copy:
	// CX := mLen
	// free up DX to use for offset
	MOVL DX, CX
	ANDL $0xF, CX

	CMPQ SI, R9
	JAE  end

	// offset
	// si += 2
	// DX := int(src[si-2]) | int(src[si-1])<<8
	ADDQ    $2, SI
	JC      err_short_buf
	CMPQ    SI, R9
	JA      err_short_buf
	MOVWQZX -2(SI), DX

	// 0 offset is invalid
	TESTL DX, DX
	JEQ   err_corrupt

match_len_loop_pre:
	// if mlen != 0xF
	CMPB CX, $0xF
	JNE  copy_match

	// do { BX = src[si++]; mlen += BX } while (BX == 0xFF).`)
	amd64ReadLenExt(a, "match_len_loop")
	a.I(`
copy_match:
	ADDQ $const_minMatch, CX

	// check we have match_len bytes left in dst
	// di+match_len < len(dst)
	MOVQ DI, AX
	ADDQ CX, AX
	JC   err_short_buf
	CMPQ AX, R8
	JA   err_short_buf

	// DX = offset
	// CX = match_len
	// BX = &dst + (di - offset)
	MOVQ DI, BX
	SUBQ DX, BX

	// check BX is within dst
	// if BX < &dst
	JC   copy_match_from_dict
	CMPQ BX, R11
	JB   copy_match_from_dict

copy_match_dispatch:
	// DX offset, CX match_len > 0, BX match, DI dst. Also entered from
	// copy_match_from_dict with BX = &dst, DX = di.
	CMPQ DX, CX
	JB   copy_match_overlap

	// Non-overlapping match (offset >= match_len).
	CMPQ CX, $16
	JA   copy_match_nonoverlap_long

	// match_len <= 16: one 16-byte copy if there is room for the overrun.
	// if len(dst[di:]) < 16
	MOVQ R8, AX
	SUBQ DI, AX
	CMPQ AX, $16
	JB   copy_match_loop

	MOVOU (BX), X0
	MOVOU X0, (DI)

	ADDQ CX, DI
	XORL CX, CX
	JMP  loopcheck

	// Byte copy: short overlapping matches and tails without room to overrun.`)
	amd64ByteLoop(a, "copy_match_loop")
	a.I(`
	JMP loopcheck

copy_match_nonoverlap_long:
	// 17..255 bytes: inline 16-byte loop, last chunk ends exactly at
	// di+match_len. 256 and up still call memmove.
	CMPQ CX, $256
	JAE  memmove_match
`)
	amd64Run16(a, "copy_match_nonoverlap_loop", true)
	a.I(`
copy_match_overlap:
	// Overlapping: the output is periodic with period offset.
	CMPQ DX, $32
	JAE  copy_match_overlap32
	CMPQ DX, $16
	JA   copy_match_overlap17 // 17..31
	JE   copy_match_splat16

	// offset < 16: byte loop if short, else splat (1, 2, 4, 8) or tile.
	CMPQ CX, $16
	JB   copy_match_loop
	CMPQ DX, $8
	JA   copy_match_tile      // 9..15
	JE   copy_match_splat8
	CMPQ DX, $4
	JA   copy_match_tile      // 5, 6, 7
	JE   copy_match_splat4
	CMPQ DX, $2
	JA   copy_match_tile      // 3
	JE   copy_match_splat2

	// offset == 1: replicate the byte across X0.
	MOVBQZX    (BX), AX
	MOVQ       $0x0101010101010101, R10
	IMULQ      R10, AX
	MOVQ       AX, X0
	PUNPCKLQDQ X0, X0
	MOVQ       $16, R10
	JMP        copy_match_tile_loop`)
	amd64Splat(a, "copy_match_splat2", `
	MOVWQZX    (BX), AX
	MOVQ       $0x0001000100010001, R10
	IMULQ      R10, AX
	MOVQ       AX, X0
	PUNPCKLQDQ X0, X0`)
	amd64Splat(a, "copy_match_splat4", `
	MOVL   (BX), X0
	PSHUFD $0, X0, X0`)
	amd64Splat(a, "copy_match_splat8", `
	MOVQ       (BX), X0
	PUNPCKLQDQ X0, X0`)
	amd64Splat(a, "copy_match_splat16", `
	MOVOU (BX), X0`)
	a.I(`
copy_match_tile:
	// Prefill step = (16/offset)*offset bytes so [match, di) holds a full
	// tile and di is phase-aligned; then store the tile every step bytes.
	LEAQ    tileStep<>(SB), R10
	MOVBQZX (R10)(DX*1), R10
	SUBQ    R10, CX
	LEAQ    (DI)(R10*1), AX     // prefill end

copy_match_tile_prefill:
	MOVB (BX), R10
	MOVB R10, (DI)
	INCQ BX
	INCQ DI
	CMPQ DI, AX
	JB   copy_match_tile_prefill

	LEAQ    tileStep<>(SB), R10
	MOVBQZX (R10)(DX*1), R10
	MOVQ    DI, AX
	SUBQ    R10, AX
	SUBQ    DX, AX              // AX = match: the tile source
	MOVOU   (AX), X0

copy_match_tile_loop:
	// X0 tile, R10 step (16 for splats), CX bytes left. Splats store four
	// tiles per iteration while at least 64 bytes are left: long runs such
	// as zeros are bound by stores, not by loop overhead. (Unrolling the
	// 12..15-byte steps too made them slower on Sapphire Rapids at 4MiB.)
	CMPQ CX, $64
	JB   copy_match_tile_loop1
	CMPQ R10, $16
	JNE  copy_match_tile_loop1

copy_match_tile_loop4:
	MOVOU X0, (DI)
	MOVOU X0, 16(DI)
	MOVOU X0, 32(DI)
	MOVOU X0, 48(DI)
	ADDQ  $64, DI
	SUBQ  $64, CX
	CMPQ  CX, $64
	JAE   copy_match_tile_loop4

copy_match_tile_loop1:
	CMPQ  CX, $16
	JB    copy_match_tile_tail
	MOVOU X0, (DI)
	ADDQ  R10, DI
	SUBQ  R10, CX
	JMP   copy_match_tile_loop1

copy_match_tile_tail:
	// 0..15 left: one more tile if it fits in dst, else bytes.`)
	amd64TileTail(a, "X0")
	a.I(`
copy_match_tail_bytes:
	MOVQ DI, BX
	SUBQ DX, BX
	JMP  copy_match_loop

copy_match_overlap17:
	// 17..31: prefill one period with two 16-byte copies (both inside
	// [di, di+offset)), then store the 32-byte tile at match every offset bytes.
	MOVOU (BX), X0
	MOVOU X0, (DI)
	MOVOU -16(BX)(DX*1), X1
	MOVOU X1, -16(DI)(DX*1)
	ADDQ  DX, DI
	SUBQ  DX, CX
	MOVOU 16(BX), X1

copy_match_overlap17_loop:
	CMPQ  CX, $32
	JB    copy_match_overlap17_tail
	MOVOU X0, (DI)
	MOVOU X1, 16(DI)
	ADDQ  DX, DI
	SUBQ  DX, CX
	JMP   copy_match_overlap17_loop

copy_match_overlap17_tail:
	// 0..31 left: one more tile if it fits in dst, else bytes.`)
	amd64TileTail(a, "X0", "X1")
	a.I(`
copy_match_overlap32:
	// offset >= 32. Long matches take copy_match_far, out of line, unless
	// the offset is so large that its source is out of L1 anyway: there
	// the loop below was as fast (Sapphire Rapids, 1MiB match at 64KiB).
	CMPQ CX, $256
	JB   copy_match_overlap32_short
	CMPQ DX, $16384
	JB   copy_match_far

copy_match_overlap32_short:
	// The last chunk ends exactly at di+match_len and is loaded after the
	// loop; loads never touch the previous iteration's store.`)
	amd64Run16(a, "copy_match_overlap32_loop", false)
	a.I(`
copy_match_from_dict:
	// CX = match_len
	// BX = &dst + (di - offset)

	// AX = offset - di = dict_bytes_available => count of bytes potentially covered by the dictionary
	MOVQ R11, AX
	SUBQ BX, AX

	// BX = len(dict) - dict_bytes_available
	MOVQ R15, BX
	SUBQ AX, BX
	JS   err_short_dict

	ADDQ R14, BX

	// if match_len <= dict_bytes_available, match fits entirely within external dictionary : just copy
	CMPQ CX, AX
	JBE  memmove_match

	// The match stretches over the dictionary and our block
	// 1) copy what comes from the dictionary
	// AX = dict_bytes_available = copy_size
	// BX = &dict_end - copy_size
	// CX = match_len`)
	amd64Memmove{from: "BX", n: "AX", spill: "CX", reloadN: true}.emit(a)
	a.I(`
	// di+=copy_size
	ADDQ AX, DI

	// 2) copy the rest (> 0 bytes) from the start of the current block:
	// a match at offset di from &dst.
	SUBQ AX, CX
	MOVQ R11, BX
	MOVQ DI, DX
	SUBQ R11, DX
	JMP  copy_match_dispatch

memmove_match:`)
	amd64Memmove{from: "BX", n: "CX", advance: []string{"DI"}}.emit(a)
	a.I(`
	XORL CX, CX

	// Every sequence that does not take the shortcut ends here: keep this
	// jump target aligned, so that code added above cannot shift it. Its
	// placement moved short-match decoding by several percent.
	PCALIGN $32

loopcheck:
	// for si < len(src)
	CMPQ SI, R9
	JB   loop

end:
	// Remaining length must be zero.
	TESTQ CX, CX
	JNE   err_corrupt

	SUBQ R11, DI
	MOVQ DI, ret+72(FP)
	RET

err_corrupt:
	MOVQ $-1, ret+72(FP)
	RET

err_short_buf:
	MOVQ $-2, ret+72(FP)
	RET

err_short_dict:
	MOVQ $-3, ret+72(FP)
	RET

	// Out-of-line blocks.
copy_match_far:
	// Overlapping match, 32 <= offset < 16KiB, len >= 256. First grow the copy
	// distance: with P bytes of pattern before DI (P a multiple of the
	// offset), copying [DI-P, DI) to DI doubles it. From P >= 512 on,
	// stream 64 bytes per iteration from DI-P: those loads are of bytes
	// stored long before, so they do not wait on store forwarding as loads
	// from DI-offset do.
	MOVQ DX, AX

copy_match_grow:
	// AX = P >= 32, CX > 0 bytes left.
	CMPQ AX, $512
	JAE  copy_match_stream
	CMPQ CX, AX
	JBE  copy_match_stream
	MOVQ DI, BX
	SUBQ AX, BX
	MOVQ AX, R10

copy_match_grow_loop:
	MOVOU (BX), X0
	MOVOU X0, (DI)
	ADDQ  $16, BX
	ADDQ  $16, DI
	SUBQ  $16, R10
	CMPQ  R10, $16
	JAE   copy_match_grow_loop

	// 0..15 left: the last 16 bytes of the source, which ends at the old DI.
	MOVOU -16(BX)(R10*1), X0
	MOVOU X0, -16(DI)(R10*1)
	ADDQ  R10, DI
	SUBQ  AX, CX
	SHLQ  $1, AX
	JMP   copy_match_grow

copy_match_stream:
	// P >= 512, or CX <= P: all loads are below DI. BX starts at the
	// beginning of the pattern, so the end-aligned last copy below needs
	// 16 bytes behind it: under 16 bytes left at the start, copy bytes.
	MOVQ DI, BX
	SUBQ AX, BX
	CMPQ CX, $16
	JB   copy_match_stream_bytes
	CMPQ CX, $64
	JB   copy_match_stream_tail
	CMPB ·hasAVX2(SB), $0
	JNE  copy_match_stream64_avx2

copy_match_stream64:
	MOVOU (BX), X0
	MOVOU 16(BX), X1
	MOVOU 32(BX), X2
	MOVOU 48(BX), X3
	MOVOU X0, (DI)
	MOVOU X1, 16(DI)
	MOVOU X2, 32(DI)
	MOVOU X3, 48(DI)
	ADDQ  $64, BX
	ADDQ  $64, DI
	SUBQ  $64, CX
	CMPQ  CX, $64
	JAE   copy_match_stream64

copy_match_stream_tail:
	// 0..63 left. The match is at least 256 bytes, so the last 16 bytes
	// can be copied ending exactly at its end.
	CMPQ  CX, $16
	JBE   copy_match_stream_last
	MOVOU (BX), X0
	MOVOU X0, (DI)
	ADDQ  $16, BX
	ADDQ  $16, DI
	SUBQ  $16, CX
	JMP   copy_match_stream_tail

copy_match_stream_last:
	MOVOU -16(BX)(CX*1), X0
	MOVOU X0, -16(DI)(CX*1)
	ADDQ  CX, DI
	XORL  CX, CX
	JMP   loopcheck
`)
	amd64ByteLoop(a, "copy_match_stream_bytes")
	a.I(`
	JMP loopcheck

	// AVX2 variant of copy_match_stream64, out of line so that it moves
	// no other code: two 32-byte loads and stores per 64 bytes.
	//
	// PCALIGN $64 also aligns decodeBlock itself to 64 bytes. Without
	// it the function is only 32-byte aligned, and whether it lands on a
	// 64-byte boundary depends on the code linked before it: the other
	// half moved every hot loop and cost Zen 4/5 2-6% on real data.
	PCALIGN $64

copy_match_stream64_avx2:
	// Long copies store faster than Intel's Golden Cove-class cores
	// (Sapphire and Granite Rapids) prefetch lines for ownership: once the
	// destination leaves L1d, 32-byte stores send about 6x as many demand
	// RFOs to L2 as 16-byte ones, fill the fill and store buffers, and run
	// 4-14% slower than the SSE loop. Prefetching for ownership 512 bytes
	// ahead prevents that; shorter copies stay in L1d and skip it, as do
	// CPUs that do not enumerate PREFETCHW (Haswell).
	CMPQ CX, $AVX2_PREFETCH_MIN
	JB   copy_match_stream64_avx2_loop
	CMPB ·hasPrefetchW(SB), $0
	JNE  copy_match_stream64_avx2_pfw
	JMP  copy_match_stream64_avx2_loop

	// Each loop starts on a 64-byte boundary so that it fits in one 64-byte
	// fetch window: straddling one cost Sapphire Rapids 20% at 4-32K. Every
	// block before a PCALIGN here ends in a jump, so no padding executes.`)
	amd64AVX2Stream(a, "copy_match_stream64_avx2_loop", false)
	amd64AVX2Stream(a, "copy_match_stream64_avx2_pfw", true)
	amd64TileStep(a)
}
