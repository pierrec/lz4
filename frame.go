package lz4

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"

	"github.com/pierrec/lz4/internal/xxh32"
)

//go:generate go run gen.go

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

func (f *Frame) initW(dst io.Writer, num int) {
	f.Magic = frameMagic
	f.Descriptor.initW()
	f.Blocks.initW(f, dst, num)
	f.checksum.Reset()
}

func (f *Frame) closeW(dst io.Writer, num int) error {
	if err := f.Blocks.closeW(f, num); err != nil {
		return err
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

func (f *Frame) initR(src io.Reader) error {
	if f.Magic > 0 {
		// Header already read.
		return nil
	}
newFrame:
	if err := readUint32(src, f.buf[:], &f.Magic); err != nil {
		return err
	}
	switch m := f.Magic; {
	case m == frameMagic:
	// All 16 values of frameSkipMagic are valid.
	case m>>8 == frameSkipMagic>>8:
		var skip uint32
		if err := binary.Read(src, binary.LittleEndian, &skip); err != nil {
			return err
		}
		if _, err := io.CopyN(ioutil.Discard, src, int64(skip)); err != nil {
			return err
		}
		goto newFrame
	default:
		return ErrInvalidFrame
	}
	if err := f.Descriptor.initR(f, src); err != nil {
		return err
	}
	f.Blocks.initR(f)
	f.checksum.Reset()
	return nil
}

func (f *Frame) closeR(src io.Reader) error {
	f.Magic = 0
	if !f.Descriptor.Flags.ContentChecksum() {
		return nil
	}
	if err := readUint32(src, f.buf[:], &f.Checksum); err != nil {
		return err
	}
	if c := f.checksum.Sum32(); c != f.Checksum {
		return fmt.Errorf("%w: got %x; expected %x", ErrInvalidFrameChecksum, c, f.Checksum)
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

func (fd *FrameDescriptor) write(f *Frame, dst io.Writer) error {
	if fd.Checksum > 0 {
		// Header already written.
		return nil
	}

	buf := f.buf[:4+2]
	// Write the magic number here even though it belongs to the Frame.
	binary.LittleEndian.PutUint32(buf, f.Magic)
	binary.LittleEndian.PutUint16(buf[4:], uint16(fd.Flags))

	if fd.Flags.Size() {
		buf = buf[:4+2+8]
		binary.LittleEndian.PutUint64(buf[4+2:], fd.ContentSize)
	}
	fd.Checksum = descriptorChecksum(buf[4:])
	buf = append(buf, fd.Checksum)

	_, err := dst.Write(buf)
	return err
}

func (fd *FrameDescriptor) initR(f *Frame, src io.Reader) error {
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
		return fmt.Errorf("%w: got %x; expected %x", ErrInvalidHeaderChecksum, c, fd.Checksum)
	}
	// Validate the elements that can be.
	if !fd.Flags.BlockSizeIndex().isValid() {
		return ErrOptionInvalidBlockSize
	}
	return nil
}

func descriptorChecksum(buf []byte) byte {
	return byte(xxh32.ChecksumZero(buf) >> 8)
}

type Blocks struct {
	Block  *FrameDataBlock
	Blocks chan chan *FrameDataBlock
	err    error
}

func (b *Blocks) initW(f *Frame, dst io.Writer, num int) {
	size := f.Descriptor.Flags.BlockSizeIndex()
	if num == 1 {
		b.Blocks = nil
		b.Block = newFrameDataBlock(size)
		return
	}
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
				if err := block.write(f, dst); err != nil && b.err == nil {
					// Keep the first error.
					b.err = err
					// All pending compression goroutines need to shut down, so we need to keep going.
				}
			}
			close(c)
		}
	}()
}

func (b *Blocks) closeW(f *Frame, num int) error {
	if num == 1 {
		b.Block.closeW(f)
		b.Block = nil
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

func (b *Blocks) initR(f *Frame) {
	size := f.Descriptor.Flags.BlockSizeIndex()
	b.Block = newFrameDataBlock(size)
}

func newFrameDataBlock(size BlockSizeIndex) *FrameDataBlock {
	return &FrameDataBlock{Data: size.get()}
}

type FrameDataBlock struct {
	Size     DataBlockSize
	Data     []byte
	Checksum uint32
}

func (b *FrameDataBlock) closeW(f *Frame) {
	size := f.Descriptor.Flags.BlockSizeIndex()
	size.put(b.Data)
}

// Block compression errors are ignored since the buffer is sized appropriately.
func (b *FrameDataBlock) compress(f *Frame, src []byte, ht []int, level CompressionLevel) *FrameDataBlock {
	data := b.Data[:len(src)] // trigger the incompressible flag in CompressBlock
	var n int
	switch level {
	case Fast:
		n, _ = CompressBlock(src, data, ht)
	default:
		n, _ = CompressBlockHC(src, data, level, ht)
	}
	if n == 0 {
		b.Size.uncompressedSet(true)
		data = src
	} else {
		b.Size.uncompressedSet(false)
		data = data[:n]
	}
	b.Data = data
	b.Size.sizeSet(len(data))

	if f.Descriptor.Flags.BlockChecksum() {
		b.Checksum = xxh32.ChecksumZero(src)
	}
	if f.Descriptor.Flags.ContentChecksum() {
		_, _ = f.checksum.Write(src)
	}
	return b
}

func (b *FrameDataBlock) write(f *Frame, dst io.Writer) error {
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

func (b *FrameDataBlock) uncompress(f *Frame, src io.Reader, dst []byte) (int, error) {
	buf := f.buf[:]
	var x uint32
	if err := readUint32(src, buf, &x); err != nil {
		return 0, err
	}
	b.Size = DataBlockSize(x)
	if b.Size == 0 {
		// End of frame reached.
		return 0, io.EOF
	}

	isCompressed := !b.Size.uncompressed()
	size := b.Size.size()
	var data []byte
	if isCompressed {
		data = b.Data
	} else {
		data = dst
	}
	data = data[:size]
	if _, err := io.ReadFull(src, data); err != nil {
		return 0, err
	}
	if isCompressed {
		n, err := UncompressBlock(data, dst)
		if err != nil {
			return 0, err
		}
		data = dst[:n]
	}

	if f.Descriptor.Flags.BlockChecksum() {
		if err := readUint32(src, buf, &b.Checksum); err != nil {
			return 0, err
		}
		if c := xxh32.ChecksumZero(data); c != b.Checksum {
			return 0, fmt.Errorf("%w: got %x; expected %x", ErrInvalidBlockChecksum, c, b.Checksum)
		}
	}
	if f.Descriptor.Flags.ContentChecksum() {
		_, _ = f.checksum.Write(data)
	}
	return len(data), nil
}

func readUint32(r io.Reader, buf []byte, x *uint32) error {
	if _, err := io.ReadFull(r, buf[:4]); err != nil {
		return err
	}
	*x = binary.LittleEndian.Uint32(buf)
	return nil
}
