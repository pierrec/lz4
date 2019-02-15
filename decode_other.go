// +build !amd64 appengine !gc noasm

package lz4

func decodeBlock(dst, src []byte) (ret int) {
	defer func() {
		// It is now faster to let the runtime panic and recover on out of bound slice access
		// than checking indices as we go along.
		if recover() != nil {
			ret = -2
		}
	}()

	var si, di int
	for {
		// Literals and match lengths (token).
		b := int(src[si])
		si++

		// Literals.
		if lLen := b >> 4; lLen > 0 {
			if lLen == 0xF {
				for src[si] == 0xFF {
					lLen += 0xFF
					si++
				}
				lLen += int(src[si])
				si++
			}
			i := si
			si += lLen
			di += copy(dst[di:di+si-i], src[i:si])

			if si >= len(src) {
				return di
			}
		}

		si++
		_ = src[si] // Bound check elimination.
		offset := int(src[si-1]) | int(src[si])<<8
		si++

		// Match.
		mLen := b & 0xF
		if mLen == 0xF {
			for src[si] == 0xFF {
				mLen += 0xFF
				si++
			}
			mLen += int(src[si])
			si++
		}
		mLen += minMatch

		// Copy the match.
		i := di - offset
		if offset > 0 && mLen >= offset {
			// Efficiently copy the match dst[di-offset:di] into the dst slice.
			bytesToCopy := offset * (mLen / offset)
			expanded := dst[i:]
			for n := offset; n <= bytesToCopy+offset; n *= 2 {
				copy(expanded[n:], expanded[:n])
			}
			di += bytesToCopy
			mLen -= bytesToCopy
		}
		di += copy(dst[di:di+mLen], dst[i:i+mLen])
	}

	return di
}
