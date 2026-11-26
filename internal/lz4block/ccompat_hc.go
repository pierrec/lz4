package lz4block

import (
	"math/bits"
	"sync"
)

// CompressorCCompatHC is a port of LZ4_compress_HC from the reference
// implementation (lz4 1.10), and produces the same output for each of its
// levels: level 2 (lz4mid) looks up one 4-byte and one 8-byte hash per
// position, levels 3 to 9 search a hash chain and look ahead at overlapping
// matches before choosing one, and levels 10 to 12 search for the cheapest
// sequence of matches (the optimal parser).
//
// Positions are indexes as in lz4hc.c: those of a call start at least 64kiB
// past the last one of the previous call, so that entries left in the tables
// are out of reach, like the zeroed tables of a fresh LZ4_streamHC_t, and the
// tables need clearing only every 1GiB of input.
//
// Constants and names follow lz4hc.c.
const (
	mlBits         = 4                 // ML_BITS
	mlMask         = 1<<mlBits - 1     // ML_MASK
	runMask        = 1<<(8-mlBits) - 1 // RUN_MASK
	hcHashLog      = 15                // LZ4HC_HASH_LOG
	hcHashTabSize  = 1 << hcHashLog
	hcMidHashLog   = hcHashLog - 1 // LZ4MID_HASHLOG
	hcMidHashSize  = 1 << hcMidHashLog
	hcOptimalML    = mlMask - 1 + minMatch // OPTIMAL_ML
	hcOptNum       = 1 << 12               // LZ4_OPT_NUM
	hcTrailingLits = 3                     // TRAILING_LITERALS
	hcMinLength    = ccompatMFLimit + 1    // LZ4_minLength
	hcLevelDefault = 9                     // LZ4HC_CLEVEL_DEFAULT
	hcLevelMax     = 12                    // LZ4HC_CLEVEL_MAX
	hcIndexReset   = 1 << 30               // tables are cleared past 1GB of indexes
)

type hcStrategy uint8

const (
	hcMid hcStrategy = iota
	hcChain
	hcOpt
)

// hcLevels is k_clTable: the strategy, number of searches and target length
// of each level.
var hcLevels = [hcLevelMax + 1]struct {
	strat        hcStrategy
	nbSearches   int
	targetLength int
}{
	{hcMid, 2, 16},
	{hcMid, 2, 16},
	{hcMid, 2, 16},
	{hcChain, 4, 16},
	{hcChain, 8, 16},
	{hcChain, 16, 16},
	{hcChain, 32, 16},
	{hcChain, 64, 16},
	{hcChain, 128, 16},
	{hcChain, 256, 16},
	{hcOpt, 96, 64},
	{hcOpt, 512, 128},
	{hcOpt, 16384, hcOptNum},
}

type CompressorCCompatHC struct {
	// hashTable holds the latest index for each hash. lz4mid uses its two
	// halves as its 4-byte and 8-byte tables.
	hashTable [hcHashTabSize]uint32
	// chainTable holds, for each index modulo 64ki, the distance back to the
	// previous index with the same hash.
	chainTable [1 << 16]uint16
	// next is the index after the last input position of the previous call.
	next uint32
	opt  []hcOptimal // for levels 10 to 12, allocated on first use
}

type hcOptimal struct {
	price, off, mlen, litlen int32
}

var compressorCCompatHCPool = sync.Pool{New: func() any { return new(CompressorCCompatHC) }}

// CompressBlockCCompatHC is CompressorCCompatHC.CompressBlock using a pooled
// CompressorCCompatHC.
func CompressBlockCCompatHC(src, dst []byte, level int) (int, error) {
	c := compressorCCompatHCPool.Get().(*CompressorCCompatHC)
	n, err := c.CompressBlock(src, dst, level)
	compressorCCompatHCPool.Put(c)
	return n, err
}

// CompressBlock compresses src into dst at the given level, as
// LZ4_compress_HC does: levels below 1 mean 9, and levels above 12 mean 12.
// It returns 0 if the result does not fit in dst, which cannot happen if
// len(dst) >= CompressBlockBound(len(src)).
func (c *CompressorCCompatHC) CompressBlock(src, dst []byte, level int) (int, error) {
	if level < 1 {
		level = hcLevelDefault
	} else if level > hcLevelMax {
		level = hcLevelMax
	}
	if len(src) > ccompatMaxInput {
		// Beyond what lz4hc.c accepts, and what 32-bit indexes can hold.
		return CompressBlock(src, dst)
	}
	// LZ4HC_init_internal.
	base := c.next
	if base > hcIndexReset {
		c.hashTable = [hcHashTabSize]uint32{}
		base = 0
	}
	base += 64 << 10
	c.next = base + uint32(len(src))

	if len(src) == 0 {
		if len(dst) == 0 {
			return 0, nil
		}
		dst[0] = 0
		return 1, nil
	}
	h := hcCtx{
		c:            c,
		src:          src,
		dst:          dst,
		r:            newSrcReader(src),
		base:         base,
		nextToUpdate: base,
		limited:      len(dst) < CompressBlockBound(len(src)),
	}
	p := hcLevels[level]
	switch p.strat {
	case hcMid:
		return h.compressMid(), nil
	case hcChain:
		return h.compressHashChain(p.nbSearches), nil
	}
	if c.opt == nil {
		c.opt = make([]hcOptimal, hcOptNum+hcTrailingLits)
	}
	return h.compressOptimal(p.nbSearches, p.targetLength, level >= hcLevelMax), nil
}

// hcCtx is the state of one call. Positions in src are ints; indexes, as
// stored in the tables, are uint32s, base more than their positions.
type hcCtx struct {
	c            *CompressorCCompatHC
	src, dst     []byte
	r            srcReader
	base         uint32 // prefixIdx, dictLimit and lowLimit in lz4hc.c
	nextToUpdate uint32
	limited      bool // limitedOutput: check that the output fits
}

func hcHash(v uint32) uint32 { return v * 2654435761 >> (32 - hcHashLog) }

func hcMidHash4(v uint32) uint32 { return v * 2654435761 >> (32 - hcMidHashLog) }

// hcMidHash7 hashes the low 7 bytes of v.
func hcMidHash7(v uint64) uint32 { return uint32(v << 8 * 58295818150454627 >> (64 - hcMidHashLog)) }

// insert is LZ4HC_Insert: it adds the positions up to ip, excluded, to the
// hash chains.
func (h *hcCtx) insert(ip int) {
	c := h.c
	target := uint32(ip) + h.base
	for idx := h.nextToUpdate; idx < target; idx++ {
		hv := hcHash(h.r.load32(h.src, int(idx-h.base)))
		c.chainTable[uint16(idx)] = uint16(min(idx-c.hashTable[hv], distanceMax))
		c.hashTable[hv] = idx
	}
	h.nextToUpdate = target
}

// countBack is LZ4HC_countBack: minus the number of bytes before ip that
// match those before match, going no lower than iMin or 0.
func (h *hcCtx) countBack(ip, match, iMin int) int {
	back := 0
	min := max(iMin-ip, -match)
	for back-min > 3 {
		v := h.r.load32(h.src, ip+back-4) ^ h.r.load32(h.src, match+back-4)
		if v != 0 {
			return back - bits.LeadingZeros32(v)>>3
		}
		back -= 4
	}
	for back > min && h.src[ip+back-1] == h.src[match+back-1] {
		back--
	}
	return back
}

// countPattern is LZ4HC_countPattern: the number of bytes from ip, up to
// iEnd, that repeat pattern, whose period is 1, 2 or 4 bytes.
func (h *hcCtx) countPattern(ip, iEnd int, pattern uint32) int {
	start := ip
	p := uint64(pattern) | uint64(pattern)<<32
	for ip < iEnd-7 {
		if diff := h.r.load64(h.src, ip) ^ p; diff != 0 {
			return ip + bits.TrailingZeros64(diff)>>3 - start
		}
		ip += 8
	}
	for ip < iEnd && h.src[ip] == byte(p) {
		ip++
		p >>= 8
	}
	return ip - start
}

// reverseCountPattern is LZ4HC_reverseCountPattern: the number of bytes
// before ip, down to iLow, that repeat pattern.
func (h *hcCtx) reverseCountPattern(ip, iLow int, pattern uint32) int {
	start := ip
	for ip >= iLow+4 && h.r.load32(h.src, ip-4) == pattern {
		ip -= 4
	}
	for shift := 24; ip > iLow && h.src[ip-1] == byte(pattern>>shift); shift -= 8 {
		ip--
	}
	return start - ip
}

type hcMatch struct {
	off, len int
	back     int // <= 0
}

// getWiderMatch is LZ4HC_InsertAndGetWiderMatch without dictionaries and
// favorDecSpeed: it returns the longest match than longest found starting
// at ip, or before it down to iLowLimit, and ending by iHighLimit.
func (h *hcCtx) getWiderMatch(ip, iLowLimit, iHighLimit, longest, maxNbAttempts int, patternAnalysis, chainSwap bool) hcMatch {
	const (
		repUntested = iota
		repNot
		repConfirmed
	)
	c, src, r := h.c, h.src, h.r
	prefixIdx := h.base
	ipIndex := uint32(ip) + prefixIdx
	lowestMatchIndex := prefixIdx
	if prefixIdx+distanceMax+1 <= ipIndex {
		lowestMatchIndex = ipIndex - distanceMax
	}
	lookBackLength := ip - iLowLimit
	nbAttempts := maxNbAttempts
	var matchChainPos uint32
	pattern := r.load32(src, ip)
	repeat := repUntested
	srcPatternLength := 0
	offset, sBack := 0, 0

	h.insert(ip)
	matchIndex := c.hashTable[hcHash(pattern)]

	// Indexes are never below prefixIdx, so this omits lz4hc.c's extDict
	// cases, and LZ4HC_protectDictEnd always holds.
	for matchIndex >= lowestMatchIndex && nbAttempts > 0 {
		matchLength := 0
		nbAttempts--
		matchPtr := int(matchIndex - prefixIdx)
		if r.load16(src, iLowLimit+longest-1) == r.load16(src, matchPtr-lookBackLength+longest-1) &&
			r.load32(src, matchPtr) == pattern {
			back := 0
			if lookBackLength != 0 {
				back = h.countBack(ip, matchPtr, iLowLimit)
			}
			matchLength = minMatch + countMatch(r, src, ip+minMatch, matchPtr+minMatch, iHighLimit) - back
			if matchLength > longest {
				longest = matchLength
				offset = int(ipIndex - matchIndex)
				sBack = back
			}
		}

		if chainSwap && matchLength == longest && matchIndex+uint32(longest) <= ipIndex {
			// A better match: follow the chain of whichever of its
			// positions has the farthest previous occurrence.
			const kTrigger = 4
			distanceToNextMatch := uint32(1)
			end := longest - minMatch + 1
			step := 1
			accel := 1 << kTrigger
			for pos := 0; pos < end; pos += step {
				candidateDist := uint32(c.chainTable[uint16(matchIndex+uint32(pos))])
				step = accel >> kTrigger
				accel++
				if candidateDist > distanceToNextMatch {
					distanceToNextMatch = candidateDist
					matchChainPos = uint32(pos)
					accel = 1 << kTrigger
				}
			}
			if distanceToNextMatch > 1 {
				if distanceToNextMatch > matchIndex {
					break
				}
				matchIndex -= distanceToNextMatch
				continue
			}
		}

		if distNextMatch := c.chainTable[uint16(matchIndex)]; patternAnalysis && distNextMatch == 1 && matchChainPos == 0 {
			// Maybe a repeated pattern.
			matchCandidateIdx := matchIndex - 1
			if repeat == repUntested {
				if pattern&0xFFFF == pattern>>16 && pattern&0xFF == pattern>>24 {
					repeat = repConfirmed
					srcPatternLength = h.countPattern(ip+4, iHighLimit, pattern) + 4
				} else {
					repeat = repNot
				}
			}
			if repeat == repConfirmed && matchCandidateIdx >= lowestMatchIndex {
				matchPtr := int(matchCandidateIdx - prefixIdx)
				if r.load32(src, matchPtr) == pattern {
					forwardPatternLength := h.countPattern(matchPtr+4, iHighLimit, pattern) + 4
					backLength := h.reverseCountPattern(matchPtr, 0, pattern)
					// Go no further back than lowestMatchIndex.
					backLength = int(matchCandidateIdx - max(matchCandidateIdx-uint32(backLength), lowestMatchIndex))
					currentSegmentLength := backLength + forwardPatternLength
					if currentSegmentLength >= srcPatternLength && forwardPatternLength <= srcPatternLength {
						// The whole source pattern fits: take the position
						// where it ends with the segment.
						matchIndex = matchCandidateIdx + uint32(forwardPatternLength) - uint32(srcPatternLength)
					} else {
						// The farthest position in the segment.
						matchIndex = matchCandidateIdx - uint32(backLength)
						if lookBackLength == 0 {
							if maxML := min(currentSegmentLength, srcPatternLength); longest < maxML {
								if uint32(ip)+prefixIdx-matchIndex > distanceMax {
									break
								}
								longest = maxML
								offset = int(ipIndex - matchIndex)
							}
							distToNextPattern := uint32(c.chainTable[uint16(matchIndex)])
							if distToNextPattern > matchIndex {
								break
							}
							matchIndex -= distToNextPattern
						}
					}
					continue
				}
			}
		}

		// Follow the current chain.
		matchIndex -= uint32(c.chainTable[uint16(matchIndex+matchChainPos)])
	}
	return hcMatch{off: offset, len: longest, back: sBack}
}

// findLongerMatch is LZ4HC_FindLongerMatch: a match from ip longer than
// minLen, or none.
func (h *hcCtx) findLongerMatch(ip, iHighLimit, minLen, nbSearches int) hcMatch {
	m := h.getWiderMatch(ip, ip, iHighLimit, minLen, nbSearches, true, true)
	if m.len <= minLen {
		return hcMatch{}
	}
	return m
}

// encodeSequence is LZ4HC_encodeSequence: it writes the literals from
// anchor to ip and a match, and returns the new op, or -1 if limited and
// they do not fit.
func (h *hcCtx) encodeSequence(ip, op, anchor, matchLength, offset int) int {
	dst := h.dst
	token := op
	op++
	length := ip - anchor
	if h.limited && op+length/255+length+(2+1+lastLiterals) > len(dst) {
		return -1
	}
	if length >= runMask {
		dst[token] = runMask << mlBits
		l := length - runMask
		for ; l >= 255; l -= 255 {
			dst[op] = 255
			op++
		}
		dst[op] = byte(l)
		op++
	} else {
		dst[token] = byte(length << mlBits)
	}
	op += copy(dst[op:op+length], h.src[anchor:ip])

	dst[op] = byte(offset)
	dst[op+1] = byte(offset >> 8)
	op += 2

	length = matchLength - minMatch
	if h.limited && op+length/255+(1+lastLiterals) > len(dst) {
		return -1
	}
	if length >= mlMask {
		dst[token] += mlMask
		length -= mlMask
		for ; length >= 255; length -= 255 {
			dst[op] = 255
			op++
		}
		dst[op] = byte(length)
		op++
	} else {
		dst[token] += byte(length)
	}
	return op
}

// lastLiterals writes the literals from anchor to the end of src, and
// returns the size of the output, or 0 if limited and they do not fit.
func (h *hcCtx) lastLiterals(op, anchor int) int {
	dst := h.dst
	lastRunSize := len(h.src) - anchor
	if h.limited && op+1+(lastRunSize+255-runMask)/255+lastRunSize > len(dst) {
		return 0
	}
	if lastRunSize >= runMask {
		dst[op] = runMask << mlBits
		op++
		acc := lastRunSize - runMask
		for ; acc >= 255; acc -= 255 {
			dst[op] = 255
			op++
		}
		dst[op] = byte(acc)
		op++
	} else {
		dst[op] = byte(lastRunSize << mlBits)
		op++
	}
	return op + copy(dst[op:], h.src[anchor:])
}

// compressMid is LZ4MID_compress, for level 2.
func (h *hcCtx) compressMid() int {
	src, r := h.src, h.r
	hash4Table := h.c.hashTable[:hcMidHashSize]
	hash8Table := h.c.hashTable[hcMidHashSize:]
	addPos8 := func(p int, idx uint32) { hash8Table[hcMidHash7(r.load64(src, p))] = idx }
	addPos4 := func(p int, idx uint32) { hash4Table[hcMidHash4(r.load32(src, p))] = idx }

	ip, anchor, op := 0, 0, 0
	mflimit := len(src) - ccompatMFLimit
	matchlimit := len(src) - lastLiterals
	prefixIdx := h.base
	ilimitIdx := uint32(len(src)-8) + prefixIdx
	var matchLength int
	var matchDistance uint32

	if len(src) < hcMinLength {
		return h.lastLiterals(op, anchor)
	}

	for ip <= mflimit {
		ipIndex := uint32(ip) + prefixIdx
		// Search a long match.
		{
			h8 := hcMidHash7(r.load64(src, ip))
			pos8 := hash8Table[h8]
			hash8Table[h8] = ipIndex
			if ipIndex-pos8 <= distanceMax {
				// Never below prefixIdx: older entries are out of reach.
				matchLength = countMatch(r, src, ip, int(pos8-prefixIdx), matchlimit)
				if matchLength >= minMatch {
					matchDistance = ipIndex - pos8
					goto encode
				}
			}
		}
		// Search a short match.
		{
			h4 := hcMidHash4(r.load32(src, ip))
			pos4 := hash4Table[h4]
			hash4Table[h4] = ipIndex
			if ipIndex-pos4 <= distanceMax {
				matchLength = countMatch(r, src, ip, int(pos4-prefixIdx), matchlimit)
				if matchLength >= minMatch {
					// Check ip+1 for a longer match.
					h8 := hcMidHash7(r.load64(src, ip+1))
					pos8 := hash8Table[h8]
					m2Distance := ipIndex + 1 - pos8
					matchDistance = ipIndex - pos4
					if m2Distance <= distanceMax && pos8 >= prefixIdx && ip < mflimit {
						if ml2 := countMatch(r, src, ip+1, int(pos8-prefixIdx), matchlimit); ml2 > matchLength {
							hash8Table[h8] = ipIndex + 1
							ip++
							matchLength = ml2
							matchDistance = m2Distance
						}
					}
					goto encode
				}
			}
		}
		// No match: skip faster over incompressible data.
		ip += 1 + (ip-anchor)>>9
		continue

	encode:
		// Catch back.
		for ip > anchor && uint32(ip) > matchDistance && src[ip-1] == src[ip-int(matchDistance)-1] {
			ip--
			matchLength++
		}

		// Fill the tables with the beginning of the match. As in lz4hc.c,
		// the indexes are from before the catch back and ip++ above.
		addPos8(ip+1, ipIndex+1)
		addPos8(ip+2, ipIndex+2)
		addPos4(ip+1, ipIndex+1)

		if op = h.encodeSequence(ip, op, anchor, matchLength, int(matchDistance)); op < 0 {
			return 0
		}
		ip += matchLength
		anchor = ip

		// Fill the tables with the end of the match.
		endMatchIdx := uint32(ip) + prefixIdx
		if endMatchIdx-2 < ilimitIdx {
			if ip > 5 {
				addPos8(ip-5, endMatchIdx-5)
			}
			addPos8(ip-3, endMatchIdx-3)
			addPos8(ip-2, endMatchIdx-2)
			addPos4(ip-2, endMatchIdx-2)
			addPos4(ip-1, endMatchIdx-1)
		}
	}
	return h.lastLiterals(op, anchor)
}

// compressHashChain is LZ4HC_compress_hashChain, for levels 3 to 9.
func (h *hcCtx) compressHashChain(maxNbAttempts int) int {
	patternAnalysis := maxNbAttempts > 128 // level 9
	ip, anchor, op := 0, 0, 0
	mflimit := len(h.src) - ccompatMFLimit
	matchlimit := len(h.src) - lastLiterals
	var (
		start0, start2, start3 int
		m0, m1, m2, m3         hcMatch
	)

	if len(h.src) < hcMinLength {
		return h.lastLiterals(op, anchor)
	}

	// encode writes ip's sequence with m and moves past it, and reports
	// whether it fitted.
	encode := func(m hcMatch) bool {
		if op = h.encodeSequence(ip, op, anchor, m.len, m.off); op < 0 {
			return false
		}
		ip += m.len
		anchor = ip
		return true
	}

	for ip <= mflimit {
		m1 = h.getWiderMatch(ip, ip, matchlimit, minMatch-1, maxNbAttempts, patternAnalysis, false)
		if m1.len < minMatch {
			ip++
			continue
		}

		// Saved, in case we would skip too much.
		start0, m0 = ip, m1

	search2:
		if ip+m1.len <= mflimit {
			start2 = ip + m1.len - 2
			m2 = h.getWiderMatch(start2, ip, matchlimit, m1.len, maxNbAttempts, patternAnalysis, false)
			start2 += m2.back
		} else {
			m2 = hcMatch{}
		}

		if m2.len <= m1.len {
			// No better match: encode m1 immediately.
			if !encode(m1) {
				return 0
			}
			continue
		}

		if start0 < ip && start2 < ip+m0.len {
			// The first match was skipped at least once: squeeze m1
			// between m0 (the original m1) and m2.
			ip, m1 = start0, m0
		}

		if start2-ip < 3 {
			// The first match is too small: remove it.
			ip, m1 = start2, m2
			goto search2
		}

	search3:
		if start2-ip < hcOptimalML {
			newML := min(m1.len, hcOptimalML)
			if ip+newML > start2+m2.len-minMatch {
				newML = start2 - ip + m2.len - minMatch
			}
			if correction := newML - (start2 - ip); correction > 0 {
				start2 += correction
				m2.len -= correction
			}
		}

		if start2+m2.len <= mflimit {
			start3 = start2 + m2.len - 3
			m3 = h.getWiderMatch(start3, start2, matchlimit, m2.len, maxNbAttempts, patternAnalysis, false)
			start3 += m3.back
		} else {
			m3 = hcMatch{}
		}

		if m3.len <= m2.len {
			// No better match: encode m1 and m2.
			if start2 < ip+m1.len {
				m1.len = start2 - ip
			}
			if !encode(m1) {
				return 0
			}
			ip = start2
			if !encode(m2) {
				return 0
			}
			continue
		}

		if start3 < ip+m1.len+3 {
			// Not enough space for m2: remove it.
			if start3 >= ip+m1.len {
				// m1 can be written now; m3 becomes m1.
				if start2 < ip+m1.len {
					correction := ip + m1.len - start2
					start2 += correction
					m2.len -= correction
					if m2.len < minMatch {
						start2, m2 = start3, m3
					}
				}
				if !encode(m1) {
					return 0
				}
				ip, m1 = start3, m3
				start0, m0 = start2, m2
				goto search2
			}
			start2, m2 = start3, m3
			goto search3
		}

		// Three ascending matches: write m1, deciding its length.
		if start2 < ip+m1.len {
			if start2-ip < hcOptimalML {
				m1.len = min(m1.len, hcOptimalML)
				if ip+m1.len > start2+m2.len-minMatch {
					m1.len = start2 - ip + m2.len - minMatch
				}
				if correction := m1.len - (start2 - ip); correction > 0 {
					start2 += correction
					m2.len -= correction
				}
			} else {
				m1.len = start2 - ip
			}
		}
		if !encode(m1) {
			return 0
		}
		// m2 becomes m1 and m3 m2; find a new m3.
		ip, m1 = start2, m2
		start2, m2 = start3, m3
		goto search3
	}
	return h.lastLiterals(op, anchor)
}

// hcLiteralsPrice is LZ4HC_literalsPrice, in bytes.
func hcLiteralsPrice(litlen int) int {
	price := litlen
	if litlen >= runMask {
		price += 1 + (litlen-runMask)/255
	}
	return price
}

// hcSequencePrice is LZ4HC_sequencePrice; mlen >= minMatch.
func hcSequencePrice(litlen, mlen int) int {
	price := 1 + 2 + hcLiteralsPrice(litlen) // token, offset and literals
	if mlen >= mlMask+minMatch {
		price += 1 + (mlen-(mlMask+minMatch))/255
	}
	return price
}

// compressOptimal is LZ4HC_compress_optimal without favorDecSpeed, for
// levels 10 to 12.
func (h *hcCtx) compressOptimal(nbSearches, sufficientLen int, fullUpdate bool) int {
	opt := h.c.opt[:hcOptNum+hcTrailingLits]
	ip, anchor, op := 0, 0, 0
	mflimit := len(h.src) - ccompatMFLimit
	matchlimit := len(h.src) - lastLiterals
	sufficientLen = min(sufficientLen, hcOptNum-1)

	for ip <= mflimit {
		llen := ip - anchor
		var bestMLen, bestOff, cur int

		firstMatch := h.findLongerMatch(ip, matchlimit, minMatch-1, nbSearches)
		if firstMatch.len == 0 {
			ip++
			continue
		}

		if firstMatch.len > sufficientLen {
			// Good enough: encode it immediately.
			if op = h.encodeSequence(ip, op, anchor, firstMatch.len, firstMatch.off); op < 0 {
				return 0
			}
			ip += firstMatch.len
			anchor = ip
			continue
		}

		// Set prices for the first positions (literals).
		for rPos := range minMatch {
			opt[rPos] = hcOptimal{
				mlen:   1,
				litlen: int32(llen + rPos),
				price:  int32(hcLiteralsPrice(llen + rPos)),
			}
		}
		// Set prices using the first match.
		for mlen := minMatch; mlen <= firstMatch.len; mlen++ {
			opt[mlen] = hcOptimal{
				mlen:   int32(mlen),
				off:    int32(firstMatch.off),
				litlen: int32(llen),
				price:  int32(hcSequencePrice(llen, mlen)),
			}
		}
		lastMatchPos := firstMatch.len
		for addLit := 1; addLit <= hcTrailingLits; addLit++ {
			opt[lastMatchPos+addLit] = hcOptimal{
				mlen:   1,
				litlen: int32(addLit),
				price:  opt[lastMatchPos].price + int32(hcLiteralsPrice(addLit)),
			}
		}

		// Check further positions.
		for cur = 1; cur < lastMatchPos; cur++ {
			curPtr := ip + cur
			if curPtr > mflimit {
				break
			}
			if fullUpdate {
				// Not worth searching if the next position costs the
				// same or less, unless the cost rises sharply after it.
				if opt[cur+1].price <= opt[cur].price && opt[cur+minMatch].price < opt[cur].price+3 {
					continue
				}
			} else if opt[cur+1].price <= opt[cur].price {
				continue
			}

			var newMatch hcMatch
			if fullUpdate {
				newMatch = h.findLongerMatch(curPtr, matchlimit, minMatch-1, nbSearches)
			} else {
				// Only test matches of minimum length.
				newMatch = h.findLongerMatch(curPtr, matchlimit, lastMatchPos-cur, nbSearches)
			}
			if newMatch.len == 0 {
				continue
			}

			if newMatch.len > sufficientLen || newMatch.len+cur >= hcOptNum {
				// Encode immediately.
				bestMLen = newMatch.len
				bestOff = newMatch.off
				lastMatchPos = cur + 1
				goto encode
			}

			// Before the match: set prices with literals at the beginning.
			{
				baseLitlen := int(opt[cur].litlen)
				for litlen := 1; litlen < minMatch; litlen++ {
					price := int(opt[cur].price) - hcLiteralsPrice(baseLitlen) + hcLiteralsPrice(baseLitlen+litlen)
					if pos := cur + litlen; price < int(opt[pos].price) {
						opt[pos] = hcOptimal{
							mlen:   1,
							litlen: int32(baseLitlen + litlen),
							price:  int32(price),
						}
					}
				}
			}

			// Set prices using the match at cur.
			for ml := minMatch; ml <= newMatch.len; ml++ {
				pos := cur + ml
				var price, ll int
				if opt[cur].mlen == 1 {
					ll = int(opt[cur].litlen)
					if cur > ll {
						price = int(opt[cur-ll].price)
					}
					price += hcSequencePrice(ll, ml)
				} else {
					price = int(opt[cur].price) + hcSequencePrice(0, ml)
				}
				if pos > lastMatchPos+hcTrailingLits || price <= int(opt[pos].price) {
					if ml == newMatch.len && lastMatchPos < pos {
						lastMatchPos = pos
					}
					opt[pos] = hcOptimal{
						mlen:   int32(ml),
						off:    int32(newMatch.off),
						litlen: int32(ll),
						price:  int32(price),
					}
				}
			}
			// Complete the following positions with literals.
			for addLit := 1; addLit <= hcTrailingLits; addLit++ {
				opt[lastMatchPos+addLit] = hcOptimal{
					mlen:   1,
					litlen: int32(addLit),
					price:  opt[lastMatchPos].price + int32(hcLiteralsPrice(addLit)),
				}
			}
		}

		bestMLen = int(opt[lastMatchPos].mlen)
		bestOff = int(opt[lastMatchPos].off)
		cur = lastMatchPos - bestMLen

	encode:
		// Reverse traversal, to find the shortest path.
		{
			candidatePos := cur
			selectedMatchLength := int32(bestMLen)
			selectedOffset := int32(bestOff)
			for {
				nextMatchLength := opt[candidatePos].mlen // 1 for a literal
				nextOffset := opt[candidatePos].off
				opt[candidatePos].mlen = selectedMatchLength
				opt[candidatePos].off = selectedOffset
				selectedMatchLength = nextMatchLength
				selectedOffset = nextOffset
				if int(nextMatchLength) > candidatePos {
					break // the first match to encode
				}
				candidatePos -= int(nextMatchLength)
			}
		}

		// Encode the sequences in order.
		for rPos := 0; rPos < lastMatchPos; {
			ml, offset := int(opt[rPos].mlen), int(opt[rPos].off)
			if ml == 1 {
				// A literal.
				ip++
				rPos++
				continue
			}
			rPos += ml
			if op = h.encodeSequence(ip, op, anchor, ml, offset); op < 0 {
				return 0
			}
			ip += ml
			anchor = ip
		}
	}
	return h.lastLiterals(op, anchor)
}
