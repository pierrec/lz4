package lz4

import (
	"io"
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
	zr := new(Reader)
	zr.state.init(readerStates)
	_ = zr.Apply(defaultOnBlockDone)
	return zr.Reset(r)
}

type Reader struct {
	state   _State
	buf     [11]byte  // frame descriptor needs at most 2+8+1=11 bytes
	src     io.Reader // source reader
	frame   Frame     // frame being read
	data    []byte    // pending data
	idx     int       // size of pending data
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
		return ErrOptionClosedOrError
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
		if err = r.frame.initR(r); r.state.next(err) {
			return
		}
		r.data = r.frame.Descriptor.Flags.BlockSizeIndex().get()
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
		switch bn, err = r.frame.Blocks.Block.uncompress(r, buf); err {
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
	switch bn, err = r.frame.Blocks.Block.uncompress(r, r.data); err {
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
	if er := r.frame.closeR(r); er != nil {
		err = er
	}
	r.frame.Descriptor.Flags.BlockSizeIndex().put(r.data)
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
