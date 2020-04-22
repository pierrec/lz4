package lz4

import "io"

var writerStates = []aState{
	noState:     newState,
	newState:    writeState,
	writeState:  closedState,
	closedState: newState,
	errorState:  newState,
}

// NewWriter returns a new LZ4 frame encoder.
func NewWriter(w io.Writer) *Writer {
	zw := new(Writer)
	zw.state.init(writerStates)
	_ = zw.Apply(defaultBlockSizeOption, defaultChecksumOption, defaultConcurrency, defaultOnBlockDone)
	return zw.Reset(w)
}

type Writer struct {
	state   _State
	buf     [15]byte         // frame descriptor needs at most 4(magic)+4+8+1=11 bytes
	src     io.Writer        // destination writer
	level   CompressionLevel // how hard to try
	num     int              // concurrency level
	frame   Frame            // frame being built
	ht      []int            // hash table (set if no concurrency)
	data    []byte           // pending data
	idx     int              // size of pending data
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
		return ErrOptionClosedOrError
	}
	for _, o := range options {
		if err = o(w); err != nil {
			return
		}
	}
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
		if err = w.frame.Descriptor.write(w); w.state.next(err) {
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
		return block.compress(w, data, w.ht).write(w)
	}
	size := w.frame.Descriptor.Flags.BlockSizeIndex()
	c := make(chan *FrameDataBlock)
	w.frame.Blocks.Blocks <- c
	go func(c chan *FrameDataBlock, data []byte, size BlockSizeIndex) {
		defer w.handler(len(data))
		b := newFrameDataBlock(size)
		zdata := b.Data
		c <- b.compress(w, data, nil)
		// Wait for the compressed or uncompressed data to no longer be in use
		// and free the allocated buffers
		if b.Size.uncompressed() {
			zdata, data = data, zdata
		}
		size.put(data)
		<-c
		size.put(zdata)
	}(c, data, size)

	if direct {
		w.data = size.get()
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
		htPool.Put(w.ht)
		size := w.frame.Descriptor.Flags.BlockSizeIndex()
		size.put(w.data)
		w.data = nil
	}
	return w.frame.closeW(w)
}

// Reset clears the state of the Writer w such that it is equivalent to its
// initial state from NewWriter, but instead writing to writer.
// Reset keeps the previous options unless overwritten by the supplied ones.
// No access to writer is performed.
//
// w.Close must be called before Reset.
func (w *Writer) Reset(writer io.Writer) *Writer {
	w.state.state = noState
	w.state.next(nil)
	w.src = writer
	w.frame.initW(w)
	size := w.frame.Descriptor.Flags.BlockSizeIndex()
	w.data = size.get()
	w.idx = 0
	if w.isNotConcurrent() {
		w.ht = htPool.Get().([]int)
	}
	return w
}
