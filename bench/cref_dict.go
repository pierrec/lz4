package bench

/*
#cgo pkg-config: liblz4
#include <lz4.h>
#include <lz4hc.h>

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
*/
import "C"

// CompressFastDict is LZ4_loadDict followed by LZ4_compress_fast_continue.
// C's streaming functions look at addresses: src must not overlap dict.
func CompressFastDict(src, dst, dict []byte, acceleration int) int {
	return int(C.compressFastDict(ptr(src), ptr(dst), C.int(len(src)), C.int(len(dst)), ptr(dict), C.int(len(dict)), C.int(acceleration)))
}

// CompressHCDict is LZ4_loadDictHC followed by LZ4_compress_HC_continue.
// src must not overlap dict.
func CompressHCDict(src, dst, dict []byte, level int) int {
	return int(C.compressHCDict(ptr(src), ptr(dst), C.int(len(src)), C.int(len(dst)), ptr(dict), C.int(len(dict)), C.int(level)))
}

// DecompressSafeUsingDict is LZ4_decompress_safe_usingDict.
func DecompressSafeUsingDict(src, dst, dict []byte) int {
	return int(C.LZ4_decompress_safe_usingDict(ptr(src), ptr(dst), C.int(len(src)), C.int(len(dst)), ptr(dict), C.int(len(dict))))
}
