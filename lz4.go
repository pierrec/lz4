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
	// ErrInvalidFrame is returned when reading an invalid LZ4 archive.
	ErrInvalidFrame _error = "lz4: bad magic number"
	// ErrInternalUnhandledState is an internal error.
	ErrInternalUnhandledState _error = "lz4: unhandled state"
	// ErrInvalidHeaderChecksum is returned when reading a frame.
	ErrInvalidHeaderChecksum _error = "lz4: invalid header checksum"
	// ErrInvalidBlockChecksum is returned when reading a frame.
	ErrInvalidBlockChecksum _error = "lz4: invalid block checksum"
	// ErrInvalidFrameChecksum is returned when reading a frame.
	ErrInvalidFrameChecksum _error = "lz4: invalid frame checksum"
	// ErrOptionInvalidCompressionLevel is returned when the supplied compression level is invalid.
	ErrOptionInvalidCompressionLevel _error = "lz4: invalid compression level"
	// ErrOptionClosedOrError is returned when an option is applied to a closed or in error object.
	ErrOptionClosedOrError _error = "lz4: cannot apply options on closed or in error object"
	// ErrOptionInvalidBlockSize is returned when
	ErrOptionInvalidBlockSize _error = "lz4: invalid block size"
	// ErrOptionNotApplicable is returned when trying to apply an option to an object not supporting it.
	ErrOptionNotApplicable _error = "lz4: option not applicable"
	// ErrWriterNotClosed is returned when attempting to reset an unclosed writer.
	ErrWriterNotClosed _error = "lz4: writer not closed"
)
