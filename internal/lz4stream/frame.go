// Package lz4stream provides the types that support reading and writing LZ4 data streams.
package lz4stream

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"sync"

	"github.com/pierrec/lz4/v4/internal/lz4block"
	"github.com/pierrec/lz4/v4/internal/lz4errors"
	"github.com/pierrec/lz4/v4/internal/xxh32"
)

//go:generate go run gen.go

const (
	frameMagic       uint32 = 0x184D2204
	frameSkipMagic   uint32 = 0x184D2A50
	frameMagicLegacy uint32 = 0x184C2102
)

func NewFrame() *Frame {
	return &Frame{}
}

type Frame struct {
	buf        [15]byte // frame descriptor needs at most 4(magic)+4+8+1=11 bytes
	Magic      uint32
	Descriptor FrameDescriptor
	Blocks     Blocks
	Checksum   uint32
	checksum   xxh32.XXHZero
}

// Reset allows reusing the Frame.
// The Descriptor configuration is not modified.
func (f *Frame) Reset(num int) {
	f.Magic = 0
	f.Descriptor.Checksum = 0
	f.Descriptor.ContentSize = 0
	_ = f.Blocks.close(f, num)
	f.Checksum = 0
}

func (f *Frame) InitW(dst io.Writer, num int, legacy bool) {
	if legacy {
		f.Magic = frameMagicLegacy
		idx := lz4block.Index(lz4block.Block8Mb)
		f.Descriptor.Flags.BlockSizeIndexSet(idx)
	} else {
		f.Magic = frameMagic
		f.Descriptor.initW()
	}
	f.Blocks.initW(f, dst, num)
	f.checksum.Reset()
}

func (f *Frame) CloseW(dst io.Writer, num int) error {
	if err := f.Blocks.close(f, num); err != nil {
		return err
	}
	if f.isLegacy() {
		return nil
	}
	buf := f.buf[:0]
	// End mark (data block size of uint32(0)).
	buf = append(buf, 0, 0, 0, 0)
	if f.Descriptor.Flags.ContentChecksum() {
		buf = f.checksum.Sum(buf)
	}
	_, err := dst.Write(buf)
	return err
}

func (f *Frame) isLegacy() bool {
	return f.Magic == frameMagicLegacy
}

func (f *Frame) InitR(src io.Reader, num int) (chan []byte, error) {
	if f.Magic > 0 {
		// Header already read.
		return nil, nil
	}

newFrame:
	var err error
	if f.Magic, err = f.readUint32(src); err != nil {
		return nil, err
	}
	switch m := f.Magic; {
	case m == frameMagic || m == frameMagicLegacy:
	// All 16 values of frameSkipMagic are valid.
	case m>>8 == frameSkipMagic>>8:
		skip, err := f.readUint32(src)
		if err != nil {
			return nil, err
		}
		if _, err := io.CopyN(ioutil.Discard, src, int64(skip)); err != nil {
			return nil, err
		}
		goto newFrame
	default:
		return nil, lz4errors.ErrInvalidFrame
	}
	if err := f.Descriptor.initR(f, src); err != nil {
		return nil, err
	}
	f.checksum.Reset()
	return f.Blocks.initR(f, num, src)
}

func (f *Frame) CloseR(src io.Reader) (err error) {
	if f.isLegacy() {
		return nil
	}
	if !f.Descriptor.Flags.ContentChecksum() {
		return nil
	}
	if f.Checksum, err = f.readUint32(src); err != nil {
		return err
	}
	if c := f.checksum.Sum32(); c != f.Checksum {
		return fmt.Errorf("%w: got %x; expected %x", lz4errors.ErrInvalidFrameChecksum, c, f.Checksum)
	}
	return nil
}

type FrameDescriptor struct {
	Flags       DescriptorFlags
	ContentSize uint64
	Checksum    uint8
}

func (fd *FrameDescriptor) initW() {
	fd.Flags.VersionSet(1)
	fd.Flags.BlockIndependenceSet(true)
}

func (fd *FrameDescriptor) Write(f *Frame, dst io.Writer) error {
	if fd.Checksum > 0 {
		// Header already written.
		return nil
	}

	buf := f.buf[:4]
	// Write the magic number here even though it belongs to the Frame.
	binary.LittleEndian.PutUint32(buf, f.Magic)
	if !f.isLegacy() {
		buf = buf[:4+2]
		binary.LittleEndian.PutUint16(buf[4:], uint16(fd.Flags))

		if fd.Flags.Size() {
			buf = buf[:4+2+8]
			binary.LittleEndian.PutUint64(buf[4+2:], fd.ContentSize)
		}
		fd.Checksum = descriptorChecksum(buf[4:])
		buf = append(buf, fd.Checksum)
	}

	_, err := dst.Write(buf)
	return err
}

func (fd *FrameDescriptor) initR(f *Frame, src io.Reader) error {
	if f.isLegacy() {
		idx := lz4block.Index(lz4block.Block8Mb)
		f.Descriptor.Flags.BlockSizeIndexSet(idx)
		return nil
	}
	// Read the flags and the checksum, hoping that there is not content size.
	buf := f.buf[:3]
	if _, err := io.ReadFull(src, buf); err != nil {
		return err
	}
	descr := binary.LittleEndian.Uint16(buf)
	fd.Flags = DescriptorFlags(descr)
	if fd.Flags.Size() {
		// Append the 8 missing bytes.
		buf = buf[:3+8]
		if _, err := io.ReadFull(src, buf[3:]); err != nil {
			return err
		}
		fd.ContentSize = binary.LittleEndian.Uint64(buf[2:])
	}
	fd.Checksum = buf[len(buf)-1] // the checksum is the last byte
	buf = buf[:len(buf)-1]        // all descriptor fields except checksum
	if c := descriptorChecksum(buf); fd.Checksum != c {
		return fmt.Errorf("%w: got %x; expected %x", lz4errors.ErrInvalidHeaderChecksum, c, fd.Checksum)
	}
	// Validate the elements that can be.
	if idx := fd.Flags.BlockSizeIndex(); !idx.IsValid() {
		return lz4errors.ErrOptionInvalidBlockSize
	}
	return nil
}

func descriptorChecksum(buf []byte) byte {
	return byte(xxh32.ChecksumZero(buf) >> 8)
}

type Blocks struct {
	Block  *FrameDataBlock
	Blocks chan chan *FrameDataBlock
	mu     sync.Mutex
	err    error
}

func (b *Blocks) initW(f *Frame, dst io.Writer, num int) {
	size := f.Descriptor.Flags.BlockSizeIndex()
	if num == 1 {
		b.Blocks = nil
		b.Block = NewFrameDataBlock(size)
		return
	}
	b.Block = nil
	if cap(b.Blocks) != num {
		b.Blocks = make(chan chan *FrameDataBlock, num)
	}
	// goroutine managing concurrent block compression goroutines.
	go func() {
		// Process next block compression item.
		for c := range b.Blocks {
			// Read the next compressed block result.
			// Waiting here ensures that the blocks are output in the order they were sent.
			// The incoming channel is always closed as it indicates to the caller that
			// the block has been processed.
			block := <-c
			if block == nil {
				// Notify the block compression routine that we are done with its result.
				// This is used when a sentinel block is sent to terminate the compression.
				close(c)
				return
			}
			// Do not attempt to write the block upon any previous failure.
			if b.err == nil {
				// Write the block.
				if err := block.Write(f, dst); err != nil {
					// Keep the first error.
					b.err = err
					// All pending compression goroutines need to shut down, so we need to keep going.
				}
			}
			close(c)
		}
	}()
}

func (b *Blocks) close(f *Frame, num int) error {
	if num == 1 {
		if b.Block != nil {
			b.Block.Close(f)
		}
		err := b.err
		b.err = nil
		return err
	}
	if b.Blocks == nil {
		// Not initialized yet.
		return nil
	}
	c := make(chan *FrameDataBlock)
	b.Blocks <- c
	c <- nil
	<-c
	err := b.err
	b.err = nil
	return err
}

// ErrorR returns any error set while uncompressing a stream.
func (b *Blocks) ErrorR() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// initR returns a channel that streams the uncompressed blocks if in concurrent
// mode and no error. When the channel is closed, check for any error with b.ErrorR.
//
// If not in concurrent mode, the uncompressed block is b.Block and the returned error
// needs to be checked.
func (b *Blocks) initR(f *Frame, num int, src io.Reader) (chan []byte, error) {
	size := f.Descriptor.Flags.BlockSizeIndex()
	if num == 1 {
		b.Blocks = nil
		b.Block = NewFrameDataBlock(size)
		return nil, nil
	}
	b.Block = nil
	blocks := make(chan chan []byte, num)
	// data receives the uncompressed blocks.
	data := make(chan []byte)
	// Read blocks from the source sequentially
	// and uncompress them concurrently.
	go func() {
		for b.ErrorR() == nil {
			block := NewFrameDataBlock(size)
			if err := block.Read(f, src); err != nil {
				b.closeR(err)
				break
			}
			// Recheck for an error as reading may be slow and uncompressing is expensive.
			if b.ErrorR() != nil {
				break
			}
			c := make(chan []byte)
			blocks <- c
			go func() {
				data, err := block.Uncompress(f, size.Get())
				if err != nil {
					b.closeR(err)
				} else {
					c <- data
				}
			}()
		}
		// End the collection loop and the data channel.
		c := make(chan *FrameDataBlock)
		b.Blocks <- c
		c <- nil // signal the collection loop that we are done
		<-c      // wait for the collect loop to complete
		close(data)
	}()
	// Collect the uncompressed blocks and make them available
	// on the returned channel.
	go func() {
		for c := range blocks {
			buf := <-c
			if buf == nil {
				// Signal to end the loop.
				close(c)
				return
			}
			data <- buf
			size.Put(buf)
			close(c)
		}
	}()
	return data, nil
}

// closeR safely sets the error on b if not already set.
func (b *Blocks) closeR(err error) {
	b.mu.Lock()
	if b.err == nil {
		b.err = err
	}
	b.mu.Unlock()
}

func NewFrameDataBlock(size lz4block.BlockSizeIndex) *FrameDataBlock {
	buf := size.Get()
	return &FrameDataBlock{Data: buf, data: buf}
}

type FrameDataBlock struct {
	Size     DataBlockSize
	Data     []byte // compressed or uncompressed data (.data or .src)
	Checksum uint32
	data     []byte // buffer for compressed data
	src      []byte // uncompressed data
	done     bool   // for legacy support
	err      error  // used in concurrent mode
}

func (b *FrameDataBlock) Close(f *Frame) {
	b.Size = 0
	b.Checksum = 0
	b.done = false
	b.err = nil
	if b.data != nil {
		// Block was not already closed.
		size := f.Descriptor.Flags.BlockSizeIndex()
		size.Put(b.data)
		b.Data = nil
		b.data = nil
		b.src = nil
	}
}

// Block compression errors are ignored since the buffer is sized appropriately.
func (b *FrameDataBlock) Compress(f *Frame, src []byte, level lz4block.CompressionLevel) *FrameDataBlock {
	data := b.data
	if f.isLegacy() {
		data = data[:cap(data)]
	} else {
		data = data[:len(src)] // trigger the incompressible flag in CompressBlock
	}
	var n int
	switch level {
	case lz4block.Fast:
		n, _ = lz4block.CompressBlock(src, data)
	default:
		n, _ = lz4block.CompressBlockHC(src, data, level)
	}
	if n == 0 {
		b.Size.UncompressedSet(true)
		b.Data = src
	} else {
		b.Size.UncompressedSet(false)
		b.Data = data[:n]
	}
	b.Size.sizeSet(len(b.Data))
	b.src = src // keep track of the source for content checksum

	if f.Descriptor.Flags.BlockChecksum() {
		b.Checksum = xxh32.ChecksumZero(src)
	}
	return b
}

func (b *FrameDataBlock) Write(f *Frame, dst io.Writer) error {
	if f.Descriptor.Flags.ContentChecksum() {
		_, _ = f.checksum.Write(b.src)
	}
	buf := f.buf[:]
	binary.LittleEndian.PutUint32(buf, uint32(b.Size))
	if _, err := dst.Write(buf[:4]); err != nil {
		return err
	}

	if _, err := dst.Write(b.Data); err != nil {
		return err
	}

	if b.Checksum == 0 {
		return nil
	}
	binary.LittleEndian.PutUint32(buf, b.Checksum)
	_, err := dst.Write(buf[:4])
	return err
}

// Read updates b with the next block data, size and checksum if available.
func (b *FrameDataBlock) Read(f *Frame, src io.Reader) error {
	x, err := f.readUint32(src)
	if err != nil {
		return err
	}
	switch leg := f.isLegacy(); {
	case leg && x == frameMagicLegacy:
		// Concatenated legacy frame.
		return b.Read(f, src)
	case leg && b.done:
		// In legacy mode, all blocks are of size 8Mb.
		// When a uncompressed block size is less than 8Mb,
		// then it means the end of the stream is reached.
		return io.EOF
	case !leg && x == 0:
		// Marker for end of stream.
		return io.EOF
	}
	b.Size = DataBlockSize(x)

	size := b.Size.size()
	if size > cap(b.data) {
		return lz4errors.ErrOptionInvalidBlockSize
	}
	b.data = b.data[:size]
	if _, err := io.ReadFull(src, b.data); err != nil {
		return err
	}
	if f.Descriptor.Flags.BlockChecksum() {
		sum, err := f.readUint32(src)
		if err != nil {
			return err
		}
		b.Checksum = sum
	}
	return nil
}

func (b *FrameDataBlock) Uncompress(f *Frame, dst []byte) ([]byte, error) {
	if b.Size.Uncompressed() {
		n := copy(dst, b.data)
		dst = dst[:n]
	} else {
		n, err := lz4block.UncompressBlock(b.data, dst)
		if err != nil {
			return nil, err
		}
		dst = dst[:n]
		if f.isLegacy() && uint32(n) < lz4block.Block8Mb {
			b.done = true
		}
	}
	if f.Descriptor.Flags.BlockChecksum() {
		if c := xxh32.ChecksumZero(dst); c != b.Checksum {
			err := fmt.Errorf("%w: got %x; expected %x", lz4errors.ErrInvalidBlockChecksum, c, b.Checksum)
			return nil, err
		}
	}
	if f.Descriptor.Flags.ContentChecksum() {
		_, _ = f.checksum.Write(dst)
	}
	return dst, nil
}

func (f *Frame) readUint32(r io.Reader) (x uint32, err error) {
	if _, err = io.ReadFull(r, f.buf[:4]); err != nil {
		return
	}
	x = binary.LittleEndian.Uint32(f.buf[:4])
	return
}
