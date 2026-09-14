package lz4

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"fmt"
	"io"
	"testing"
)

var testDataSizes = []int{
	0, 1, 16 << 10, 32 << 10, 64 << 10, 128 << 10, 256 << 10,
	512 << 10, 1 << 20, 2 << 20, 4 << 20, 8 << 20, 16 << 20,
	20 << 20, 32 << 20, 100 << 20,
}

// generateCompressedData generates random data of given size, compresses it using lz4,
// and returns the compressed data along with the MD5 checksum of original data.
func generateCompressedData(size int) ([]byte, []byte, error) {
	reader := io.LimitReader(rand.Reader, int64(size))
	hasher := md5.New()
	teeReader := io.TeeReader(reader, hasher)

	var compressed bytes.Buffer
	lz4Writer := NewWriter(&compressed)
	if _, err := io.Copy(lz4Writer, teeReader); err != nil {
		return nil, nil, fmt.Errorf("error writing compressed data: %w", err)
	}
	if err := lz4Writer.Close(); err != nil {
		return nil, nil, fmt.Errorf("error closing lz4 writer: %w", err)
	}
	checksum := hasher.Sum(nil)
	return compressed.Bytes(), checksum, nil
}

func TestReaderWithRandomData(t *testing.T) {
	for _, size := range testDataSizes {
		size := size // capture range variable
		t.Run(fmt.Sprintf("Size_%d_bytes", size), func(t *testing.T) {
			compressedData, originalChecksum, err := generateCompressedData(size)
			if err != nil {
				t.Fatalf("failed to generate compressed data: %v", err)
			}

			decompressReader := NewReader(bytes.NewReader(compressedData))
			decompressedData, err := io.ReadAll(decompressReader)
			if err != nil {
				t.Fatalf("decompression failed: %v", err)
			}

			if len(decompressedData) != size {
				t.Errorf("expected decompressed data size %d, got %d", size, len(decompressedData))
			}

			newChecksum := md5.Sum(decompressedData)
			if !bytes.Equal(originalChecksum, newChecksum[:]) {
				t.Errorf("checksum mismatch for size %d", size)
			}
		})
	}
}

func TestWriterToWithRandomData(t *testing.T) {
	for _, size := range testDataSizes {
		size := size // capture range variable
		t.Run(fmt.Sprintf("Size_%d_bytes", size), func(t *testing.T) {
			compressedData, originalChecksum, err := generateCompressedData(size)
			if err != nil {
				t.Fatalf("failed to generate compressed data: %v", err)
			}

			decompressReader := NewReader(bytes.NewReader(compressedData))
			var decompressed bytes.Buffer
			n, err := io.Copy(&decompressed, decompressReader)
			if err != nil {
				t.Fatalf("decompression failed: %v", err)
			}

			if n != int64(size) {
				t.Errorf("expected decompressed data size %d, got %d", size, n)
			}

			newChecksum := md5.Sum(decompressed.Bytes())
			if !bytes.Equal(originalChecksum, newChecksum[:]) {
				t.Errorf("checksum mismatch for size %d", size)
			}
		})
	}
}
