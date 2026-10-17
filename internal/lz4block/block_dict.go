package lz4block

import (
	"encoding/binary"

	"github.com/pierrec/lz4/v4/internal/lz4errors"
)

// dictData indexes the dictionary for CompressBlockWithDict.
type dictData struct {
	table [htSize]uint16
	inUse [htSize / 32]uint32
}

// initDict sets the dictionary, keeping only its last winSize bytes, and
// indexes it unless it is the dictionary already indexed.
func (c *Compressor) initDict(dict []byte) {
	if len(dict) > winSize {
		dict = dict[len(dict)-winSize:]
	}
	if len(dict) == 0 {
		c.dict = nil
		return
	}
	if len(c.dict) == len(dict) && &c.dict[0] == &dict[0] {
		return
	}
	c.dict = dict
	if c.dictData == nil {
		c.dictData = new(dictData)
	} else {
		c.dictData.inUse = [htSize / 32]uint32{}
	}
	// Index every third position, like LZ4_loadDict.
	for j := 0; j+8 <= len(dict); j += 3 {
		h := blockHash(binary.LittleEndian.Uint64(dict[j:])) & (htSize - 1)
		c.dictData.table[h] = uint16(j)
		c.dictData.inUse[h/32] |= 1 << (h % 32)
	}
}

func CompressBlockWithDict(src, dst, dict []byte) (int, error) {
	c := compressorPool.Get().(*Compressor)
	n, err := c.CompressBlockWithDict(src, dst, dict)
	// Keep dictData, so that later calls reuse its table, but not the
	// caller's dictionary. A Compressor reused with the same dictionary
	// also skips indexing it again.
	c.dict = nil
	compressorPool.Put(c)
	return n, err
}

// CompressBlockWithDict is like CompressBlock, but matches may also refer to
// the last 64 KiB of dict, as for UncompressBlock with the same dict.
//
// It uses its own loop so that CompressBlock, the common case, does not pay
// for the dictionary.
func (c *Compressor) CompressBlockWithDict(src, dst, dict []byte) (int, error) {
	c.initDict(dict)
	if len(c.dict) == 0 {
		return c.CompressBlock(src, dst)
	}
	return c.compressBlockDict(src, dst)
}

// dictPos returns the position in the dictionary of a match for the 4 bytes
// x at si, with h the hash of the 8 bytes there.
func (c *Compressor) dictPos(h uint32, si int, x uint32) (int, bool) {
	h &= htSize - 1
	if c.dictData.inUse[h/32]&(1<<(h%32)) == 0 {
		return 0, false
	}
	p := int(c.dictData.table[h])
	if off := si + len(c.dict) - p; off >= winSize || binary.LittleEndian.Uint32(c.dict[p:]) != x {
		return 0, false
	}
	return p, true
}

// compressBlockDict compresses src with matches in src or in c.dict. It
// follows CompressBlock, with one candidate per position and no unsafe loads.
func (c *Compressor) compressBlockDict(src, dst []byte) (int, error) {
	c.reset()
	isNotCompressible := len(dst) < CompressBlockBound(len(src))
	const adaptSkipLog = 7
	dict := c.dict

	var si, di, anchor int
	var err error
	sn := len(src) - mfLimit
	for si < sn {
		match := binary.LittleEndian.Uint64(src[si:])
		h := blockHash(match)
		ref := c.get(h, si)
		c.put(h, si)

		lLen := si - anchor
		var offset, mLen int
		if off := si - ref; ref >= 0 && off < winSize && uint32(match) == binary.LittleEndian.Uint32(src[ref:]) {
			offset = off
			for lLen > 0 && ref > 0 && src[si-1] == src[ref-1] {
				si--
				ref--
				lLen--
			}
			n := minMatch
			for si+n < sn && src[si+n] == src[ref+n] {
				n++
			}
			si, mLen = si+n, n-minMatch
		} else if p, ok := c.dictPos(h, si, uint32(match)); ok {
			offset = si + len(dict) - p
			for lLen > 0 && p > 0 && src[si-1] == dict[p-1] {
				si--
				p--
				lLen--
			}
			n := minMatch
			for p+n < len(dict) && si+n < sn && src[si+n] == dict[p+n] {
				n++
			}
			if p+n == len(dict) {
				// The match continues into src, offset bytes back.
				for si+n < sn && src[si+n] == src[si+n-offset] {
					n++
				}
			}
			si, mLen = si+n, n-minMatch
		} else {
			si += 1 + lLen>>adaptSkipLog
			continue
		}

		if di, err = writeSequence(dst, di, src[anchor:anchor+lLen], offset, mLen); err != nil {
			return 0, err
		}
		anchor = si
		if si < sn {
			c.put(blockHash(binary.LittleEndian.Uint64(src[si-2:])), si-2)
		}
	}

	if isNotCompressible && anchor == 0 {
		// Incompressible.
		return 0, nil
	}
	// As in CompressBlock, give up before the last literals if the output
	// is no smaller than the input consumed so far.
	if di, err = writeLastLiteralsLen(dst, di, len(src)-anchor); err != nil {
		return 0, err
	}
	if isNotCompressible && di >= anchor {
		// Incompressible.
		return 0, nil
	}
	if di+len(src)-anchor > len(dst) {
		return 0, lz4errors.ErrInvalidSourceShortBuffer
	}
	return di + copy(dst[di:], src[anchor:]), nil
}

// writeSequence writes the literals lit and a match of length mLen+minMatch
// at offset to dst at di, and returns the new position in dst.
func writeSequence(dst []byte, di int, lit []byte, offset, mLen int) (int, error) {
	lLen := len(lit)
	if di >= len(dst) {
		return 0, lz4errors.ErrInvalidSourceShortBuffer
	}
	tok := byte(min(mLen, 0xF))
	if lLen < 0xF {
		dst[di] = tok | byte(lLen<<4)
	} else {
		dst[di] = tok | 0xF0
		di++
		l := lLen - 0xF
		for ; l >= 0xFF && di < len(dst); l -= 0xFF {
			dst[di] = 0xFF
			di++
		}
		if di >= len(dst) {
			return 0, lz4errors.ErrInvalidSourceShortBuffer
		}
		dst[di] = byte(l)
	}
	di++
	if di+lLen+2 > len(dst) {
		return 0, lz4errors.ErrInvalidSourceShortBuffer
	}
	di += copy(dst[di:], lit)
	dst[di], dst[di+1] = byte(offset), byte(offset>>8)
	di += 2
	if mLen >= 0xF {
		for mLen -= 0xF; mLen >= 0xFF && di < len(dst); mLen -= 0xFF {
			dst[di] = 0xFF
			di++
		}
		if di >= len(dst) {
			return 0, lz4errors.ErrInvalidSourceShortBuffer
		}
		dst[di] = byte(mLen)
		di++
	}
	return di, nil
}

// writeLastLiteralsLen writes the token and length of the final lLen
// literals to dst at di, and returns the new position in dst.
func writeLastLiteralsLen(dst []byte, di int, lLen int) (int, error) {
	if di >= len(dst) {
		return 0, lz4errors.ErrInvalidSourceShortBuffer
	}
	if lLen < 0xF {
		dst[di] = byte(lLen << 4)
	} else {
		dst[di] = 0xF0
		di++
		l := lLen - 0xF
		for ; l >= 0xFF && di < len(dst); l -= 0xFF {
			dst[di] = 0xFF
			di++
		}
		if di >= len(dst) {
			return 0, lz4errors.ErrInvalidSourceShortBuffer
		}
		dst[di] = byte(l)
	}
	return di + 1, nil
}
