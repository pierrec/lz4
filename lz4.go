package lz4

const (
	frameMagic     uint32 = 0x184D2204
	frameSkipMagic uint32 = 0x184D2A50

	// The following constants are used to setup the compression algorithm.
	minMatch   = 4  // the minimum size of the match sequence size (4 bytes)
	winSizeLog = 16 // LZ4 64Kb window size limit
	winSize    = 1 << winSizeLog
	winMask    = winSize - 1 // 64Kb window of previous data for dependent blocks

	// hashLog determines the size of the hash table used to quickly find a previous match position.
	// Its value influences the compression speed and memory usage, the lower the faster,
	// but at the expense of the compression ratio.
	// 16 seems to be the best compromise for fast compression.
	hashLog = 16
	htSize  = 1 << hashLog

	mfLimit = 10 + minMatch // The last match cannot start within the last 14 bytes.
)

type _error string

func (e _error) Error() string { return string(e) }

const (
	// ErrInvalidSourceShortBuffer is returned by UncompressBlock or CompressBLock when a compressed
	// block is corrupted or the destination buffer is not large enough for the uncompressed data.
	ErrInvalidSourceShortBuffer _error = "lz4: invalid source or destination buffer too short"
	// ErrClosed is returned when calling Write/Read or Close on an already closed Writer/Reader.
	ErrClosed _error = "lz4: closed Writer"
	// ErrInvalid is returned when reading an invalid LZ4 archive.
	ErrInvalid _error = "lz4: bad magic number"
	// ErrBlockDependency is returned when attempting to decompress an archive created with block dependency.
	ErrBlockDependency _error = "lz4: block dependency not supported"
	// ErrUnsupportedSeek is returned when attempting to Seek any way but forward from the current position.
	ErrUnsupportedSeek _error = "lz4: can only seek forward from io.SeekCurrent"
	// ErrInternalUnhandledState is an internal error.
	ErrInternalUnhandledState _error = "lz4: unhandled state"
	// ErrInvalidHeaderChecksum
	ErrInvalidHeaderChecksum _error = "lz4: invalid header checksum"
	// ErrInvalidBlockChecksum
	ErrInvalidBlockChecksum _error = "lz4: invalid block checksum"
	// ErrInvalidFrameChecksum
	ErrInvalidFrameChecksum _error = "lz4: invalid frame checksum"
	// ErrInvalidCompressionLevel
	ErrInvalidCompressionLevel _error = "lz4: invalid compression level"
	// ErrCannotApplyOptions
	ErrCannotApplyOptions _error = "lz4: cannot apply options"
)
