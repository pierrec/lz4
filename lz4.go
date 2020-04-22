package lz4

import (
	"github.com/pierrec/lz4/internal/lz4block"
)

func _() {
	// Safety checks for duplicated elements.
	var x [1]struct{}
	_ = x[lz4block.CompressionLevel(Fast)-lz4block.Fast]
	_ = x[Block64Kb-BlockSize(lz4block.Block64Kb)]
	_ = x[Block256Kb-BlockSize(lz4block.Block256Kb)]
	_ = x[Block1Mb-BlockSize(lz4block.Block1Mb)]
	_ = x[Block4Mb-BlockSize(lz4block.Block4Mb)]
}

// CompressBlockBound returns the maximum size of a given buffer of size n, when not compressible.
func CompressBlockBound(n int) int {
	return lz4block.CompressBlockBound(n)
}

// UncompressBlock uncompresses the source buffer into the destination one,
// and returns the uncompressed size.
//
// The destination buffer must be sized appropriately.
//
// An error is returned if the source data is invalid or the destination buffer is too small.
func UncompressBlock(src, dst []byte) (int, error) {
	return lz4block.UncompressBlock(src, dst)
}

// CompressBlock compresses the source buffer into the destination one.
// This is the fast version of LZ4 compression and also the default one.
//
// The argument hashTable is scratch space for a hash table used by the
// compressor. If provided, it should have length at least 1<<16. If it is
// shorter (or nil), CompressBlock allocates its own hash table.
//
// The size of the compressed data is returned.
//
// If the destination buffer size is lower than CompressBlockBound and
// the compressed size is 0 and no error, then the data is incompressible.
//
// An error is returned if the destination buffer is too small.
func CompressBlock(src, dst []byte, hashTable []int) (_ int, err error) {
	return lz4block.CompressBlock(src, dst, hashTable)
}

// CompressBlockHC compresses the source buffer src into the destination dst
// with max search depth (use 0 or negative value for no max).
//
// CompressBlockHC compression ratio is better than CompressBlock but it is also slower.
//
// The size of the compressed data is returned.
//
// If the destination buffer size is lower than CompressBlockBound and
// the compressed size is 0 and no error, then the data is incompressible.
//
// An error is returned if the destination buffer is too small.
func CompressBlockHC(src, dst []byte, depth CompressionLevel, hashTable []int) (_ int, err error) {
	return lz4block.CompressBlockHC(src, dst, lz4block.CompressionLevel(depth), hashTable)
}
