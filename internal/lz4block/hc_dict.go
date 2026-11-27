package lz4block

import (
	"encoding/binary"
	"math"

	"github.com/pierrec/lz4/v4/internal/lz4errors"
)

// hcDict holds a dictionary and the HC tables with its positions inserted,
// copied into the working tables for each block.
type hcDict struct {
	dict   []byte
	tables hcTables[int32]
	buf    []byte // the dictionary followed by the block being compressed
}

// initDict indexes the last winSize bytes of dict, unless it is the
// dictionary already indexed, and returns them.
func (c *CompressorHC) initDict(dict []byte) []byte {
	if len(dict) > winSize {
		dict = dict[len(dict)-winSize:]
	}
	if len(dict) == 0 {
		return nil
	}
	d := c.dict
	if d == nil {
		d = new(hcDict)
		c.dict = d
	} else if len(d.dict) == len(dict) && &d.dict[0] == &dict[0] {
		return dict
	}
	d.dict = dict
	// Insert every position, like LZ4_loadDictHC. The chain entries of
	// later positions are written when they are inserted.
	t := &d.tables
	t.hashTable = [htSize]int32{}
	for j := 0; j+minMatch <= len(dict); j++ {
		h := blockHashHC(binary.LittleEndian.Uint32(dict[j:]))
		t.chainTable[j&winMask] = t.hashTable[h]
		t.hashTable[h] = int32(j)
	}
	return dict
}

// CompressBlockWithDict is like CompressBlock, but matches may also refer to
// the last 64 KiB of dict, as for UncompressBlock with the same dict.
// Calling it again with the same dict slice reuses the dictionary's index.
func (c *CompressorHC) CompressBlockWithDict(src, dst, dict []byte, depth CompressionLevel) (int, error) {
	dict = c.initDict(dict)
	if len(dict) == 0 {
		return c.CompressBlock(src, dst, depth)
	}
	if int64(len(src)) > math.MaxInt32-winSize {
		return 0, lz4errors.ErrInvalidSourceShortBuffer
	}
	if c.large == nil {
		c.large = new(hcTables[int32])
	}
	// The primed tables replace the reset that CompressBlock does.
	*c.large = c.dict.tables
	c.largeDirty = true
	d := c.dict
	d.buf = append(append(d.buf[:0], dict...), src...)
	c.large.start = len(dict)
	n, err := compressBlockHC(c.large, d.buf, dst, depth)
	if len(d.buf) > 2*winSize {
		// Keep it only for 64 KiB blocks: a Writer with concurrency keeps a
		// DictCompressor per goroutine, which would otherwise each hold a
		// block of up to 4 MiB.
		d.buf = nil
	}
	return n, err
}

// DictCompressor compresses blocks with a dictionary at Fast or HC levels,
// keeping each level's index of the dictionary.
type DictCompressor struct {
	fast Compressor
	hc   *CompressorHC // allocated on first use
}

// CompressBlock compresses src with dict at level, which must not be
// CCompatFast.
func (c *DictCompressor) CompressBlock(src, dst, dict []byte, level CompressionLevel) (int, error) {
	if level == Fast {
		return c.fast.CompressBlockWithDict(src, dst, dict)
	}
	if c.hc == nil {
		c.hc = new(CompressorHC)
	}
	return c.hc.CompressBlockWithDict(src, dst, dict, level)
}
