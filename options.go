package lz4

import (
	"bytes"
	"fmt"
	"reflect"
	"runtime"

	"github.com/pierrec/lz4/v4/internal/lz4block"
	"github.com/pierrec/lz4/v4/internal/lz4errors"
)

//go:generate go run golang.org/x/tools/cmd/stringer -type=BlockSize,CompressionLevel -output options_gen.go

type (
	applier interface {
		Apply(...Option) error
		private()
	}
	// Option defines the parameters to setup an LZ4 Writer or Reader.
	Option func(applier) error
)

// String returns a string representation of the option with its parameter(s).
func (o Option) String() string {
	return o(nil).Error()
}

// Default options.
var (
	DefaultBlockSizeOption = BlockSizeOption(Block4Mb)
	DefaultChecksumOption  = ChecksumOption(true)
	DefaultConcurrency     = ConcurrencyOption(1)
	defaultOnBlockDone     = OnBlockDoneOption(nil)
)

const (
	Block64Kb BlockSize = 1 << (16 + iota*2)
	Block256Kb
	Block1Mb
	Block4Mb
)

// BlockSizeIndex defines the size of the blocks to be compressed.
type BlockSize uint32

// BlockSizeOption defines the maximum size of compressed blocks (default=Block4Mb).
func BlockSizeOption(size BlockSize) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("BlockSizeOption(%s)", size)
			return lz4errors.Error(s)
		case *Writer:
			size := uint32(size)
			if !lz4block.IsValid(size) {
				return fmt.Errorf("%w: %d", lz4errors.ErrOptionInvalidBlockSize, size)
			}
			w.frame.Descriptor.Flags.BlockSizeIndexSet(lz4block.Index(size))
			return nil
		case *CompressingReader:
			size := uint32(size)
			if !lz4block.Index(size).IsValid() { // never legacy, so no Block8Mb
				return fmt.Errorf("%w: %d", lz4errors.ErrOptionInvalidBlockSize, size)
			}
			w.frame.Descriptor.Flags.BlockSizeIndexSet(lz4block.Index(size))
			return nil
		}
		return lz4errors.ErrOptionNotApplicable
	}
}

// BlockChecksumOption enables or disables block checksum (default=false).
func BlockChecksumOption(flag bool) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("BlockChecksumOption(%v)", flag)
			return lz4errors.Error(s)
		case *Writer:
			w.frame.Descriptor.Flags.BlockChecksumSet(flag)
			return nil
		case *CompressingReader:
			w.frame.Descriptor.Flags.BlockChecksumSet(flag)
			return nil
		}
		return lz4errors.ErrOptionNotApplicable
	}
}

// ChecksumOption enables/disables all blocks or content checksum (default=true).
func ChecksumOption(flag bool) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("ChecksumOption(%v)", flag)
			return lz4errors.Error(s)
		case *Writer:
			w.frame.Descriptor.Flags.ContentChecksumSet(flag)
			return nil
		case *CompressingReader:
			w.frame.Descriptor.Flags.ContentChecksumSet(flag)
			return nil
		}
		return lz4errors.ErrOptionNotApplicable
	}
}

// SizeOption sets the size of the original uncompressed data (default=0). It is useful to know the size of the
// whole uncompressed data stream. It must be exact: readers reject a frame whose data differs from it, so
// closing a Writer, or reading to the end of a CompressingReader, fails with ErrInvalidContentSize if it does.
// Like the other options, it applies to every frame until changed: before writing a frame of another size after
// Reset, apply it again, or SizeOption(0) to omit the size.
func SizeOption(size uint64) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("SizeOption(%d)", size)
			return lz4errors.Error(s)
		case *Writer:
			w.frame.Descriptor.Flags.SizeSet(size > 0)
			w.frame.Descriptor.ContentSize = size
			return nil
		case *CompressingReader:
			w.frame.Descriptor.Flags.SizeSet(size > 0)
			w.frame.Descriptor.ContentSize = size
			return nil
		}
		return lz4errors.ErrOptionNotApplicable
	}
}

// ConcurrencyOption sets the number of go routines used for compression.
// If n <= 0, then the output of runtime.GOMAXPROCS(0) is used.
func ConcurrencyOption(n int) Option {
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
	}
	return func(a applier) error {
		switch rw := a.(type) {
		case nil:
			s := fmt.Sprintf("ConcurrencyOption(%d)", n)
			return lz4errors.Error(s)
		case *Writer:
			rw.num = n
			return nil
		case *Reader:
			rw.num = n
			return nil
		}
		return lz4errors.ErrOptionNotApplicable
	}
}

// CompressionLevel defines the level of compression to use. The higher the better, but slower, compression.
type CompressionLevel uint32

const (
	Fast   CompressionLevel = 0
	Level1 CompressionLevel = 1 << (8 + iota)
	Level2
	Level3
	Level4
	Level5
	Level6
	Level7
	Level8
	Level9
)

// CCompatFast compresses with CompressorCCompat, whose output is that of the
// reference implementation's LZ4_compress_fast. Fast suits most data better;
// see CompressorCCompat.
const CCompatFast CompressionLevel = 1

// dictOK reports whether level can compress with a dictionary. The levels
// that reproduce the C library's output byte for byte cannot.
func (l CompressionLevel) dictOK() bool {
	switch l {
	case Fast, Level1, Level2, Level3, Level4, Level5, Level6, Level7, Level8, Level9:
		return true
	}
	return false
}

// CCompatLevel2 to CCompatLevel12 compress with CompressorCCompatHC at the
// reference implementation's levels of the same numbers, and produce the
// same blocks as it does. Higher levels are slower and compress more;
// CCompatLevel9 is the C library's default high compression level. See
// CompressorCCompatHC.
//
// Their values are the C levels, but they only select those levels in
// CompressionLevelOption: CompressorHC takes Level as a search depth.
const (
	CCompatLevel2 CompressionLevel = 2 + iota
	CCompatLevel3
	CCompatLevel4
	CCompatLevel5
	CCompatLevel6
	CCompatLevel7
	CCompatLevel8
	CCompatLevel9
	CCompatLevel10
	CCompatLevel11
	CCompatLevel12
)

func (l CompressionLevel) valid() bool {
	switch l {
	case Fast, CCompatFast, Level1, Level2, Level3, Level4, Level5, Level6, Level7, Level8, Level9:
		return true
	}
	return lz4block.CompressionLevel(l).IsCCompatHC()
}

// CompressionLevelOption defines the compression level (default=Fast).
func CompressionLevelOption(level CompressionLevel) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("CompressionLevelOption(%s)", level)
			return lz4errors.Error(s)
		case *Writer:
			if !level.valid() {
				return fmt.Errorf("%w: %d", lz4errors.ErrOptionInvalidCompressionLevel, level)
			}
			if len(w.dict) > 0 && !level.dictOK() {
				return lz4errors.ErrOptionNotApplicable
			}
			w.level = lz4block.CompressionLevel(level)
			return nil
		case *CompressingReader:
			if !level.valid() {
				return fmt.Errorf("%w: %d", lz4errors.ErrOptionInvalidCompressionLevel, level)
			}
			if len(w.dict) > 0 && !level.dictOK() {
				return lz4errors.ErrOptionNotApplicable
			}
			w.level = lz4block.CompressionLevel(level)
			return nil
		}
		return lz4errors.ErrOptionNotApplicable
	}
}

func onBlockDone(int) {}

// OnBlockDoneOption is triggered when a block has been processed: for a Writer,
// once per block when it has been compressed, with the compressed size, and in
// full before Close returns; for a Reader, as uncompressed data is read out,
// with the number of bytes read. A Writer with a concurrency level above one
// may run the handler from several goroutines at the same time, so it must be
// safe for concurrent use.
func OnBlockDoneOption(handler func(size int)) Option {
	if handler == nil {
		handler = onBlockDone
	}
	return func(a applier) error {
		switch rw := a.(type) {
		case nil:
			s := fmt.Sprintf("OnBlockDoneOption(%s)", reflect.TypeOf(handler).String())
			return lz4errors.Error(s)
		case *Writer:
			rw.handler = handler
			return nil
		case *Reader:
			rw.handler = handler
			return nil
		case *CompressingReader:
			rw.handler = handler
			return nil
		}
		return lz4errors.ErrOptionNotApplicable
	}
}

// LegacyOption provides support for writing LZ4 frames in the legacy format.
//
// See https://github.com/lz4/lz4/blob/dev/doc/lz4_Frame_format.md#legacy-frame.
//
// NB. compressed Linux kernel images use a tweaked LZ4 legacy format where
// the compressed stream is followed by the original (uncompressed) size of
// the kernel (https://events.static.linuxfound.org/sites/events/files/lcjpcojp13_klee.pdf).
// This is also supported as a special case.
func LegacyOption(legacy bool) Option {
	return func(a applier) error {
		switch rw := a.(type) {
		case nil:
			s := fmt.Sprintf("LegacyOption(%v)", legacy)
			return lz4errors.Error(s)
		case *Writer:
			if legacy && len(rw.dict) > 0 {
				return lz4errors.ErrOptionNotApplicable
			}
			rw.legacy = legacy
			return nil
		}
		return lz4errors.ErrOptionNotApplicable
	}
}

// errDictIDWithoutDict reports a frame that would name a dictionary it was not
// compressed with, which a Reader without one refuses.
var errDictIDWithoutDict = fmt.Errorf("%w: DictionaryIDOption without DictionaryOption", lz4errors.ErrOptionNotApplicable)

// DictionaryIDOption sets the ID of the dictionary, from DictionaryOption.
// LZ4 does not define how to number dictionaries; 0 means none. A Writer or
// CompressingReader writes the ID in the frame descriptor, so that readers,
// such as the C library's, can tell which dictionary the frame needs. A
// Reader returns ErrInvalidFrameDescriptor for a frame that holds another ID,
// as decoding with the wrong dictionary would produce garbage; it reads
// frames without an ID as before. An ID without a dictionary makes the first
// Write, ReadFrom, Flush or Close of a Writer, or Read of a CompressingReader,
// return ErrOptionNotApplicable.
func DictionaryIDOption(id uint32) Option {
	return func(a applier) error {
		switch rw := a.(type) {
		case nil:
			s := fmt.Sprintf("DictionaryIDOption(%#x)", id)
			return lz4errors.Error(s)
		case *Writer:
			rw.frame.Descriptor.Flags.DictIDSet(id != 0)
			rw.frame.Descriptor.DictID = id
			return nil
		case *CompressingReader:
			rw.frame.Descriptor.Flags.DictIDSet(id != 0)
			rw.frame.Descriptor.DictID = id
			return nil
		case *Reader:
			rw.dictID = id
			return nil
		}
		return lz4errors.ErrOptionNotApplicable
	}
}

// DictionaryOption defines the dictionary to use for compression and decompression.
// Compression with a dictionary supports Fast and Level1 to Level9, not the
// levels that reproduce the C library's output, and only non-legacy frames:
// with another CompressionLevelOption on a Writer or CompressingReader, or
// with LegacyOption(true) on a Writer, either option returns
// ErrOptionNotApplicable.
func DictionaryOption(dict []byte) Option {
	dict = bytes.Clone(dict) // never modified, so shared by all it is applied to
	return func(a applier) error {
		switch rw := a.(type) {
		case nil:
			s := fmt.Sprintf("DictionaryOption(%d bytes)", len(dict))
			return lz4errors.Error(s)
		case *Writer:
			if len(dict) > 0 && (!CompressionLevel(rw.level).dictOK() || rw.legacy) {
				return lz4errors.ErrOptionNotApplicable
			}
			rw.dict = dict
			if len(dict) > 0 {
				if rw.compressor == nil {
					rw.compressor = new(lz4block.DictCompressor)
				}
			} else {
				rw.compressor = nil
				rw.compressors = nil
			}
			return nil
		case *Reader:
			rw.presetDict = dict
			rw.resetDict()
			return nil
		case *CompressingReader:
			if len(dict) > 0 && !CompressionLevel(rw.level).dictOK() {
				return lz4errors.ErrOptionNotApplicable
			}
			rw.dict = dict
			if len(dict) > 0 {
				if rw.compressor == nil {
					rw.compressor = new(lz4block.DictCompressor)
				}
			} else {
				rw.compressor = nil
			}
			return nil
		}
		return lz4errors.ErrOptionNotApplicable
	}
}
