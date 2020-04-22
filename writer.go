package lz4

import (
	"io"

	"github.com/pierrec/lz4/internal/lz4block"
	"github.com/pierrec/lz4/internal/lz4errors"
	"github.com/pierrec/lz4/internal/lz4stream"
)

var writerStates = []aState{
	noState:     newState,
	newState:    writeState,
	writeState:  closedState,
	closedState: newState,
	errorState:  newState,
}

// NewWriter returns a new LZ4 frame encoder.
func NewWriter(w io.Writer) *Writer {
	zw := &Writer{frame: lz4stream.NewFrame()}
	zw.state.init(writerStates)
	_ = zw.Apply(DefaultBlockSizeOption, DefaultChecksumOption, DefaultConcurrency, defaultOnBlockDone)
	return zw.Reset(w)
}

type Writer struct {
	state   _State
	src     io.Writer                 // destination writer
	level   lz4block.CompressionLevel // how hard to try
	num     int                       // concurrency level
	frame   *lz4stream.Frame          // frame being built
	ht      []int                     // hash table (set if no concurrency)
	data    []byte                    // pending data
	idx     int                       // size of pending data
	handler func(int)
}

func (*Writer) private() {}

func (w *Writer) Apply(options ...Option) (err error) {
	defer w.state.check(&err)
	switch w.state.state {
	case newState:
	case errorState:
		return w.state.err
	default:
		return lz4errors.ErrOptionClosedOrError
	}
	for _, o := range options {
		if err = o(w); err != nil {
			return
		}
	}
	w.Reset(w.src)
	return
}

func (w *Writer) isNotConcurrent() bool {
	return w.num == 1
}

func (w *Writer) Write(buf []byte) (n int, err error) {
	defer w.state.check(&err)
	switch w.state.state {
	case writeState:
	case closedState, errorState:
		return 0, w.state.err
	case newState:
		if err = w.frame.Descriptor.Write(w.frame, w.src); w.state.next(err) {
			return
		}
	default:
		return 0, w.state.fail()
	}

	zn := len(w.data)
	for len(buf) > 0 {
		if w.idx == 0 && len(buf) >= zn {
			// Avoid a copy as there is enough data for a block.
			if err = w.write(buf[:zn], false); err != nil {
				return
			}
			n += zn
			buf = buf[zn:]
			continue
		}
		// Accumulate the data to be compressed.
		m := copy(w.data[w.idx:], buf)
		n += m
		w.idx += m
		buf = buf[m:]

		if w.idx < len(w.data) {
			// Buffer not filled.
			return
		}

		// Buffer full.
		if err = w.write(w.data, true); err != nil {
			return
		}
		w.idx = 0
	}
	return
}

func (w *Writer) write(data []byte, direct bool) error {
	if w.isNotConcurrent() {
		defer w.handler(len(data))
		block := w.frame.Blocks.Block
		return block.Compress(w.frame, data, w.ht, w.level).Write(w.frame, w.src)
	}
	size := w.frame.Descriptor.Flags.BlockSizeIndex()
	c := make(chan *lz4stream.FrameDataBlock)
	w.frame.Blocks.Blocks <- c
	go func(c chan *lz4stream.FrameDataBlock, data []byte, size lz4block.BlockSizeIndex) {
		defer w.handler(len(data))
		b := lz4stream.NewFrameDataBlock(size)
		zdata := b.Data
		c <- b.Compress(w.frame, data, nil, w.level)
		// Wait for the compressed or uncompressed data to no longer be in use
		// and free the allocated buffers
		if b.Size.Uncompressed() {
			zdata, data = data, zdata
		}
		size.Put(data)
		<-c
		size.Put(zdata)
	}(c, data, size)

	if direct {
		w.data = size.Get()
	}

	return nil
}

// Close closes the Writer, flushing any unwritten data to the underlying io.Writer,
// but does not close the underlying io.Writer.
func (w *Writer) Close() (err error) {
	switch w.state.state {
	case writeState:
	case errorState:
		return w.state.err
	default:
		return nil
	}
	defer func() { w.state.next(err) }()
	if w.idx > 0 {
		// Flush pending data.
		if err = w.write(w.data[:w.idx], false); err != nil {
			return err
		}
		w.idx = 0
	}
	if w.isNotConcurrent() {
		lz4block.HashTablePool.Put(w.ht)
		size := w.frame.Descriptor.Flags.BlockSizeIndex()
		size.Put(w.data)
		w.data = nil
	}
	return w.frame.CloseW(w.src, w.num)
}

// Reset clears the state of the Writer w such that it is equivalent to its
// initial state from NewWriter, but instead writing to writer.
// Reset keeps the previous options unless overwritten by the supplied ones.
// No access to writer is performed.
//
// w.Close must be called before Reset or it will panic.
func (w *Writer) Reset(writer io.Writer) *Writer {
	switch w.state.state {
	case newState, closedState, errorState:
	default:
		panic(lz4errors.ErrWriterNotClosed)
	}
	w.state.state = noState
	w.state.next(nil)
	w.src = writer
	w.frame.InitW(w.src, w.num)
	size := w.frame.Descriptor.Flags.BlockSizeIndex()
	w.data = size.Get()
	w.idx = 0
	if w.isNotConcurrent() {
		w.ht = lz4block.HashTablePool.Get().([]int)
	}
	return w
}
