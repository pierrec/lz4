package lz4errors

type Error string

func (e Error) Error() string { return string(e) }

const (
	// ErrInvalidSourceShortBuffer is returned by UncompressBlock or CompressBLock when a compressed
	// block is corrupted or the destination buffer is not large enough for the uncompressed data.
	ErrInvalidSourceShortBuffer Error = "lz4: invalid source or destination buffer too short"
	// ErrInvalidFrame is returned when reading an invalid LZ4 archive.
	ErrInvalidFrame Error = "lz4: bad magic number"
	// ErrInternalUnhandledState is an internal error.
	ErrInternalUnhandledState Error = "lz4: unhandled state"
	// ErrInvalidHeaderChecksum is returned when reading a frame.
	ErrInvalidHeaderChecksum Error = "lz4: invalid header checksum"
	// ErrInvalidBlockChecksum is returned when reading a frame.
	ErrInvalidBlockChecksum Error = "lz4: invalid block checksum"
	// ErrInvalidFrameChecksum is returned when reading a frame.
	ErrInvalidFrameChecksum Error = "lz4: invalid frame checksum"
	// ErrOptionInvalidCompressionLevel is returned when the supplied compression level is invalid.
	ErrOptionInvalidCompressionLevel Error = "lz4: invalid compression level"
	// ErrOptionClosedOrError is returned when an option is applied to a closed or in error object.
	ErrOptionClosedOrError Error = "lz4: cannot apply options on closed or in error object"
	// ErrOptionInvalidBlockSize is returned when
	ErrOptionInvalidBlockSize Error = "lz4: invalid block size"
	// ErrOptionNotApplicable is returned when trying to apply an option to an object not supporting it.
	ErrOptionNotApplicable Error = "lz4: option not applicable"
	// ErrWriterNotClosed is returned when attempting to reset an unclosed writer.
	ErrWriterNotClosed Error = "lz4: writer not closed"
)
