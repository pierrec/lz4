package lz4

import (
	"io"

	"github.com/pierrec/lz4/internal/lz4errors"
	"github.com/pierrec/lz4/internal/lz4stream"
)

var readerStates = []aState{
	noState:     newState,
	errorState:  newState,
	newState:    readState,
	readState:   closedState,
	closedState: newState,
}

// NewReader returns a new LZ4 frame decoder.
func NewReader(r io.Reader) *Reader {
	zr := &Reader{frame: lz4stream.NewFrame()}
	zr.state.init(readerStates)
	_ = zr.Apply(defaultOnBlockDone)
	return zr.Reset(r)
}

// Reader allows reading an LZ4 stream.
type Reader struct {
	state   _State
	src     io.Reader        // source reader
	frame   *lz4stream.Frame // frame being read
	data    []byte           // pending data
	idx     int              // size of pending data
	handler func(int)
}

func (*Reader) private() {}

func (r *Reader) Apply(options ...Option) (err error) {
	defer r.state.check(&err)
	switch r.state.state {
	case newState:
	case errorState:
		return r.state.err
	default:
		return lz4errors.ErrOptionClosedOrError
	}
	for _, o := range options {
		if err = o(r); err != nil {
			return
		}
	}
	return
}

// Size returns the size of the underlying uncompressed data, if set in the stream.
func (r *Reader) Size() int {
	switch r.state.state {
	case readState, closedState:
		if r.frame.Descriptor.Flags.Size() {
			return int(r.frame.Descriptor.ContentSize)
		}
	}
	return 0
}

func (r *Reader) Read(buf []byte) (n int, err error) {
	defer r.state.check(&err)
	switch r.state.state {
	case readState:
	case closedState, errorState:
		return 0, r.state.err
	case newState:
		// First initialization.
		if err = r.frame.InitR(r.src); r.state.next(err) {
			return
		}
		r.data = r.frame.Descriptor.Flags.BlockSizeIndex().Get()
	default:
		return 0, r.state.fail()
	}
	if len(buf) == 0 {
		return
	}

	var bn int
	if r.idx > 0 {
		// Some left over data, use it.
		goto fillbuf
	}
	// No uncompressed data yet.
	r.data = r.data[:cap(r.data)]
	for len(buf) >= len(r.data) {
		// Input buffer large enough and no pending data: uncompress directly into it.
		switch bn, err = r.frame.Blocks.Block.Uncompress(r.frame, r.src, buf); err {
		case nil:
			r.handler(bn)
			n += bn
			buf = buf[bn:]
		case io.EOF:
			goto close
		default:
			return
		}
	}
	if n > 0 {
		// Some data was read, done for now.
		return
	}
	// Read the next block.
	switch bn, err = r.frame.Blocks.Block.Uncompress(r.frame, r.src, r.data); err {
	case nil:
		r.handler(bn)
		r.data = r.data[:bn]
		goto fillbuf
	case io.EOF:
	default:
		return
	}
close:
	r.handler(bn)
	n += bn
	if er := r.frame.CloseR(r.src); er != nil {
		err = er
	}
	r.frame.Descriptor.Flags.BlockSizeIndex().Put(r.data)
	r.reset(nil)
	return
fillbuf:
	bn = copy(buf, r.data[r.idx:])
	n += bn
	r.idx += bn
	if r.idx == len(r.data) {
		// All data read, get ready for the next Read.
		r.idx = 0
	}
	return
}

func (r *Reader) reset(reader io.Reader) {
	r.src = reader
	r.data = nil
	r.idx = 0
}

// Reset clears the state of the Reader r such that it is equivalent to its
// initial state from NewReader, but instead writing to writer.
// No access to reader is performed.
//
// w.Close must be called before Reset.
func (r *Reader) Reset(reader io.Reader) *Reader {
	r.reset(reader)
	r.state.state = noState
	r.state.next(nil)
	return r
}
