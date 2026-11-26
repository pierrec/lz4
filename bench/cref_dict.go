package bench

/*
#cgo pkg-config: liblz4
#include <stdlib.h>
#include <string.h>
#include <lz4.h>
#include <lz4hc.h>
#include <lz4frame.h>

static int compressFastDict(const char* src, char* dst, int srcLen, int dstLen,
		const char* dict, int dictLen, int accel) {
	LZ4_stream_t s;
	LZ4_initStream(&s, sizeof(s));
	LZ4_loadDict(&s, dict, dictLen);
	return LZ4_compress_fast_continue(&s, src, dst, srcLen, dstLen, accel);
}

static int compressHCDict(const char* src, char* dst, int srcLen, int dstLen,
		const char* dict, int dictLen, int level) {
	LZ4_streamHC_t* s = LZ4_createStreamHC();
	LZ4_resetStreamHC_fast(s, level);
	LZ4_loadDictHC(s, dict, dictLen);
	int n = LZ4_compress_HC_continue(s, src, dst, srcLen, dstLen);
	LZ4_freeStreamHC(s);
	return n;
}

// compressFrameDict writes an LZ4 frame of src with dict, and returns its
// size, or 0 on error.
static size_t compressFrameDict(const void* src, size_t srcLen, void* dst, size_t dstLen,
		const void* dict, size_t dictLen, int level, int linked, unsigned dictID) {
	LZ4F_preferences_t p;
	memset(&p, 0, sizeof(p));
	p.compressionLevel = level;
	p.frameInfo.blockSizeID = LZ4F_max64KB;
	p.frameInfo.blockMode = linked ? LZ4F_blockLinked : LZ4F_blockIndependent;
	p.frameInfo.contentChecksumFlag = LZ4F_contentChecksumEnabled;
	p.frameInfo.dictID = dictID;
	LZ4F_cctx* cctx;
	if (LZ4F_isError(LZ4F_createCompressionContext(&cctx, LZ4F_VERSION))) return 0;
	LZ4F_CDict* cd = LZ4F_createCDict(dict, dictLen);
	size_t n = LZ4F_compressFrame_usingCDict(cctx, dst, dstLen, src, srcLen, cd, &p);
	LZ4F_freeCDict(cd);
	LZ4F_freeCompressionContext(cctx);
	return LZ4F_isError(n) ? 0 : n;
}

// decompressFrameDict decodes the LZ4 frame src with dict into dst, and
// returns the decoded size, or -1 on error.
static long decompressFrameDict(const void* src, size_t srcLen, void* dst, size_t dstLen,
		const void* dict, size_t dictLen) {
	LZ4F_dctx* d;
	if (LZ4F_isError(LZ4F_createDecompressionContext(&d, LZ4F_VERSION))) return -1;
	size_t in = 0, out = 0;
	for (;;) {
		size_t sn = srcLen - in, dn = dstLen - out;
		size_t r = LZ4F_decompress_usingDict(d, (char*)dst + out, &dn, (const char*)src + in, &sn, dict, dictLen, NULL);
		in += sn;
		out += dn;
		if (LZ4F_isError(r)) { LZ4F_freeDecompressionContext(d); return -1; }
		if (r == 0) break;
		if (sn == 0 && dn == 0) { LZ4F_freeDecompressionContext(d); return -1; }
	}
	LZ4F_freeDecompressionContext(d);
	return (long)out;
}
*/
import "C"

import "unsafe"

func uptr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Pointer(&b[0])
}

// CompressFastDict is LZ4_loadDict followed by LZ4_compress_fast_continue.
func CompressFastDict(src, dst, dict []byte, acceleration int) int {
	return int(C.compressFastDict(ptr(src), ptr(dst), C.int(len(src)), C.int(len(dst)), ptr(dict), C.int(len(dict)), C.int(acceleration)))
}

// CompressHCDict is LZ4_loadDictHC followed by LZ4_compress_HC_continue.
func CompressHCDict(src, dst, dict []byte, level int) int {
	return int(C.compressHCDict(ptr(src), ptr(dst), C.int(len(src)), C.int(len(dst)), ptr(dict), C.int(len(dict)), C.int(level)))
}

// DecompressSafeUsingDict is LZ4_decompress_safe_usingDict.
func DecompressSafeUsingDict(src, dst, dict []byte) int {
	return int(C.LZ4_decompress_safe_usingDict(ptr(src), ptr(dst), C.int(len(src)), C.int(len(dst)), ptr(dict), C.int(len(dict))))
}

// CompressFrameDict compresses src into an LZ4 frame of 64 KiB blocks, with a
// content checksum, using LZ4F_compressFrame_usingCDict with dict. linked
// selects linked blocks; dictID, if not zero, is written in the header. It
// returns nil on error.
func CompressFrameDict(src, dict []byte, level int, linked bool, dictID uint32) []byte {
	dst := make([]byte, int(C.LZ4F_compressFrameBound(C.size_t(len(src)), nil))+64)
	l := 0
	if linked {
		l = 1
	}
	n := C.compressFrameDict(uptr(src), C.size_t(len(src)), uptr(dst), C.size_t(len(dst)),
		uptr(dict), C.size_t(len(dict)), C.int(level), C.int(l), C.unsigned(dictID))
	if n == 0 {
		return nil
	}
	return dst[:n]
}

// DecompressFrameDict decodes the LZ4 frame src with dict using
// LZ4F_decompress_usingDict into a buffer of size maxLen, and returns the
// decoded data, or false on error.
func DecompressFrameDict(src, dict []byte, maxLen int) ([]byte, bool) {
	dst := make([]byte, maxLen)
	n := C.decompressFrameDict(uptr(src), C.size_t(len(src)), uptr(dst), C.size_t(len(dst)), uptr(dict), C.size_t(len(dict)))
	if n < 0 {
		return nil, false
	}
	return dst[:n], true
}
