// Package bench benchmarks the reference C implementation of LZ4 on the same
// corpus and with the same benchmark names as the lz4 package, so that the
// two can be compared with benchstat. It is a separate module, so that the
// lz4 module does not depend on cgo. See README.md.
package bench

/*
#cgo pkg-config: liblz4
#include <lz4.h>
#include <lz4hc.h>
*/
import "C"

import "unsafe"

// Version returns the version of the linked liblz4.
func Version() string { return C.GoString(C.LZ4_versionString()) }

func ptr(b []byte) *C.char {
	if len(b) == 0 {
		return nil
	}
	return (*C.char)(unsafe.Pointer(&b[0]))
}

// CompressFast is LZ4_compress_fast.
func CompressFast(src, dst []byte, acceleration int) int {
	return int(C.LZ4_compress_fast(ptr(src), ptr(dst), C.int(len(src)), C.int(len(dst)), C.int(acceleration)))
}

// CompressHC is LZ4_compress_HC.
func CompressHC(src, dst []byte, level int) int {
	return int(C.LZ4_compress_HC(ptr(src), ptr(dst), C.int(len(src)), C.int(len(dst)), C.int(level)))
}

// DecompressSafe is LZ4_decompress_safe.
func DecompressSafe(src, dst []byte) int {
	return int(C.LZ4_decompress_safe(ptr(src), ptr(dst), C.int(len(src)), C.int(len(dst))))
}
