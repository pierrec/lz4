package lz4

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
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

var (
	blockPool64K  = sync.Pool{New: func() interface{} { return make([]byte, Block64Kb) }}
	blockPool256K = sync.Pool{New: func() interface{} { return make([]byte, Block256Kb) }}
	blockPool1M   = sync.Pool{New: func() interface{} { return make([]byte, Block1Mb) }}
	blockPool4M   = sync.Pool{New: func() interface{} { return make([]byte, Block4Mb) }}
)

// BlockSizeIndex defines the size of the blocks to be compressed.
type BlockSize uint32

func (b BlockSize) isValid() bool {
	return b.index() > 0
}

func (b BlockSize) index() BlockSizeIndex {
	switch b {
	case Block64Kb:
		return 4
	case Block256Kb:
		return 5
	case Block1Mb:
		return 6
	case Block4Mb:
		return 7
	}
	return 0
}

type BlockSizeIndex uint8

func (b BlockSizeIndex) get() []byte {
	var buf interface{}
	switch b {
	case 4:
		buf = blockPool64K.Get()
	case 5:
		buf = blockPool256K.Get()
	case 6:
		buf = blockPool1M.Get()
	case 7:
		buf = blockPool4M.Get()
	}
	return buf.([]byte)
}

func (b BlockSizeIndex) put(buf []byte) {
	switch b {
	case 4:
		blockPool64K.Put(buf)
	case 5:
		blockPool256K.Put(buf)
	case 6:
		blockPool1M.Put(buf)
	case 7:
		blockPool4M.Put(buf)
	}
}

// BlockSizeOption defines the maximum size of compressed blocks (default=Block4Mb).
func BlockSizeOption(size BlockSize) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("BlockSizeOption(%s)", size)
			return _error(s)
		case *Writer:
			if !size.isValid() {
				return fmt.Errorf("%w: %d", ErrOptionInvalidBlockSize, size)
			}
			w.frame.Descriptor.Flags.BlockSizeIndexSet(size.index())
			return nil
		}
		return ErrOptionNotApplicable
	}
}

// BlockChecksumOption enables or disables block checksum (default=false).
func BlockChecksumOption(flag bool) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("BlockChecksumOption(%v)", flag)
			return _error(s)
		case *Writer:
			w.frame.Descriptor.Flags.BlockChecksumSet(flag)
			return nil
		}
		return ErrOptionNotApplicable
	}
}

// ChecksumOption enables/disables all blocks checksum (default=true).
func ChecksumOption(flag bool) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("BlockChecksumOption(%v)", flag)
			return _error(s)
		case *Writer:
			w.frame.Descriptor.Flags.ContentChecksumSet(flag)
			return nil
		}
		return ErrOptionNotApplicable
	}
}

// SizeOption sets the size of the original uncompressed data (default=0).
func SizeOption(size uint64) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("SizeOption(%d)", size)
			return _error(s)
		case *Writer:
			w.frame.Descriptor.Flags.SizeSet(size > 0)
			w.frame.Descriptor.ContentSize = size
			return nil
		}
		return ErrOptionNotApplicable
	}
}

// ConcurrencyOption sets the number of go routines used for compression.
// If n<0, then the output of runtime.GOMAXPROCS(0) is used.
func ConcurrencyOption(n int) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("ConcurrencyOption(%d)", n)
			return _error(s)
		case *Writer:
			switch n {
			case 0, 1:
			default:
				if n < 0 {
					n = runtime.GOMAXPROCS(0)
				}
			}
			w.num = n
			return nil
		}
		return ErrOptionNotApplicable
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

// CompressionLevelOption defines the compression level (default=Fast).
func CompressionLevelOption(level CompressionLevel) Option {
	return func(a applier) error {
		switch w := a.(type) {
		case nil:
			s := fmt.Sprintf("CompressionLevelOption(%s)", level)
			return _error(s)
		case *Writer:
			switch level {
			case Fast, Level1, Level2, Level3, Level4, Level5, Level6, Level7, Level8, Level9:
			default:
				return fmt.Errorf("%w: %d", ErrOptionInvalidCompressionLevel, level)
			}
			w.level = level
			return nil
		}
		return ErrOptionNotApplicable
	}
}

func onBlockDone(int) {}

// OnBlockDoneOption is triggered
func OnBlockDoneOption(handler func(size int)) Option {
	if handler == nil {
		handler = onBlockDone
	}
	return func(a applier) error {
		switch rw := a.(type) {
		case nil:
			s := fmt.Sprintf("OnBlockDoneOption(%s)", reflect.TypeOf(handler).String())
			return _error(s)
		case *Writer:
			rw.handler = handler
		case *Reader:
			rw.handler = handler
		}
		return nil
	}
}
