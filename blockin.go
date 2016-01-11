package lz4

import "encoding/binary"

// CompressInBlock compresses buf if possible, returning whether or not it was compressed.
func CompressInBlock(src *[]byte) (compressed bool) {
	buf := *src
	n, i, j := len(buf), 0, 0
	if n == 0 {
		return false
	}

	//fmt.Printf("init n=%d buf=%s\n", n, buf)
	var (
		// debt represents the encoding overhead if the whole buffer is compressed.
		// to be compressible, that overhead must be negative.
		debt      = n/255 + 16
		hashTable [1 << hashLog]int
		fma       = 1 << skipStrength
		anchor    = 0
	)
	// try to compress but only computing the debt
	// as soon as it becomes negative, it is compressible
	for i < n-minMatch {
		h := binary.LittleEndian.Uint32(buf[i:]) * hasher >> hashShift
		ref := hashTable[h] - 1
		hashTable[h] = i + 1
		// previous match must exist
		// match must not overlap with literal
		// previous match must be within the window
		// previous match must be a true match
		if ref < 0 || i-anchor < minMatch ||
			i-ref > winSize ||
			buf[ref] != buf[i] ||
			buf[ref+1] != buf[i+1] ||
			buf[ref+2] != buf[i+2] ||
			buf[ref+3] != buf[i+3] {
			i += fma >> skipStrength
			fma++
			continue
		}
		// match found
		//fmt.Printf("match found i=%d ref=%d\n", i, ref)
		// find full match
		i += minMatch
		for j := ref + minMatch; i < n && buf[i] == buf[j]; {
			i++
			j++
		}

		// debt -= ((lit len)-15)/255 + (match len-4-15)/255 + 3
		// debt -= (mi-anchor-15)/255 + (i-mi-minMatch-15)/255 + 3
		debt -= 3 + (i-anchor-minMatch-30)/255
		if debt < 0 {
			compressed = true
			break
		}

		fma = 1 << skipStrength
		anchor = i
	}

	//fmt.Printf("compressible=%v\n", compressed)
	if !compressed {
		return false
	}

	//TODO zero the previous table instead?
	var hTable [1 << hashLog]int
	fma = 1 << skipStrength
	// anchor: position of current literals
	// i: position of uncompressed data
	// j: position at the end of compressed data
	anchor, i, j = 0, 0, 0

	for i < n-minMatch {
		// find a valid match
		h := binary.LittleEndian.Uint32(buf[i:]) * hasher >> hashShift
		ref := hTable[h] - 1
		hTable[h] = i + 1
		if ref < 0 || i-anchor < minMatch ||
			i-ref > winSize ||
			buf[ref] != buf[i] ||
			buf[ref+1] != buf[i+1] ||
			buf[ref+2] != buf[i+2] ||
			buf[ref+3] != buf[i+3] {
			i += fma >> skipStrength
			fma++
			continue
		}
		// match found
		fma = 1 << skipStrength
		mi := i
		// offset
		offset := i - ref

		// find full match
		//fmt.Printf("match found i=%d ref=%d\n", i, ref)
		i += minMatch
		for j := ref + minMatch; i < n-minMatch && buf[i] == buf[j]; {
			i++
			j++
		}
		// match length - minMatch
		mn := i - mi - minMatch
		// literals length
		ln := mi - anchor
		//fmt.Printf("mn=%d ln=%d offset=%d\n", mn, ln, offset)

		// encode the literals length
		b, bn := byte(0), 0
		if mn > 15 {
			b = 15
		} else {
			b = byte(mn)
		}
		if ln < 15 {
			b |= byte(ln << 4)
			// move the literals
			copy(buf[j+1:], buf[anchor:anchor+ln])
			buf[j] = b
			j += 1 + ln
		} else {
			b |= byte(15 << 4)
			b := []byte{b}
			l := ln - 15
			for l > 255 {
				b = append(b, 255)
				l -= 255
			}
			b = append(b, byte(l))
			// move the literals
			bn = len(b)
			copy(buf[j+bn:], buf[anchor:anchor+ln])
			// add the encoded literals length
			copy(buf[j:], b)
			j += bn + ln
		}

		// add the offset
		buf[j] = byte(offset)
		j++
		buf[j] = byte(offset >> 8)
		j++

		// encode the match length
		if mn > 15 {
			mn -= 15
			for mn > 255 {
				mn -= 255
				buf[j] = 255
				j++
			}
			buf[j] = byte(mn)
			j++
		}
		//fmt.Printf("copied literals + offset + match length j=%d =[%v]\n", j, buf[:j])

		anchor = i
	}

	// last literals
	ln := n - anchor
	//fmt.Printf("last literals anchor=%d ln=%d j=%d\n", anchor, ln, j)
	if ln < 15 {
		// move the literals
		copy(buf[j+1:], buf[anchor:anchor+ln])
		buf[j] = byte(ln << 4)
		j += 1 + ln
	} else {
		b := []byte{15 << 4}
		l := ln - 15
		for l > 255 {
			b = append(b, 255)
			l -= 255
		}
		b = append(b, byte(l))
		// move the literals
		bn := len(b)
		copy(buf[j+bn:], buf[anchor:anchor+ln])
		// add the encoded literals length
		copy(buf[j:], b)
		j += bn + ln
	}
	//fmt.Printf("copied literals + offset + match length j=%d =[%v]\n", j, buf[:j])

	// resize the buffer to fit with the compressed size
	buf = buf[:j]
	*src = buf

	return true
}
