package lz4_test

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/pierrec/lz4/v4"
	"github.com/pierrec/lz4/v4/internal/lz4block"
)

func TestWriter(t *testing.T) {
	goldenFiles := []string{
		"testdata/empty.txt.gz",
		"testdata/e.txt.gz",
		"testdata/gettysburg.txt.gz",
		"testdata/Mark.Twain-Tom.Sawyer.txt.gz",
		"testdata/Mark.Twain-Tom.Sawyer_long.txt.gz",
		"testdata/pg1661.txt.gz",
		"testdata/pi.txt.gz",
		"testdata/random.data.gz",
		"testdata/repeat.txt.gz",
		"testdata/issue102.data.gz",
	}

	for _, fname := range goldenFiles {
		for _, option := range []lz4.Option{
			lz4.ConcurrencyOption(1),
			lz4.BlockChecksumOption(true),
			lz4.ConcurrencyOption(4),
		} {
			label := fmt.Sprintf("%s/%s", fname, option)
			t.Run(label, func(t *testing.T) {
				fname := fname
				option := option
				t.Parallel()

				raw, err := os.ReadFile(fname)
				if err != nil {
					t.Fatal(err)
				}
				r := bytes.NewReader(raw)

				// Compress.
				zout := new(bytes.Buffer)
				zw := lz4.NewWriter(zout)
				if err := zw.Apply(option, lz4.CompressionLevelOption(lz4.Level1)); err != nil {
					t.Fatal(err)
				}
				_, err = io.Copy(zw, r)
				if err != nil {
					t.Fatal(err)
				}
				err = zw.Close()
				if err != nil {
					t.Fatal(err)
				}

				// Uncompress.
				out := new(bytes.Buffer)
				zr := lz4.NewReader(zout)
				n, err := io.Copy(out, zr)
				if err != nil {
					t.Fatal(err)
				}

				// The uncompressed data must be the same as the initial input.
				if got, want := int(n), len(raw); got != want {
					t.Errorf("invalid sizes: got %d; want %d", got, want)
				}

				if got, want := out.Bytes(), raw; !bytes.Equal(got, want) {
					t.Fatal("uncompressed data does not match original")
				}
			})
		}
	}
}

func TestWriter_Reset(t *testing.T) {
	data := pg1661
	buf := new(bytes.Buffer)
	src := bytes.NewReader(data)
	zw := lz4.NewWriter(buf)

	// Partial write.
	_, _ = io.CopyN(zw, src, int64(len(data))/2)

	buf.Reset()
	src.Reset(data)
	zw.Reset(buf)
	zw.Reset(buf)
	// Another time to maybe trigger some edge case.
	if _, err := io.Copy(zw, src); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	// Cannot compare compressed outputs directly, so compare the uncompressed output.
	out := new(bytes.Buffer)
	if _, err := io.Copy(out, lz4.NewReader(buf)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Bytes(), data) {
		t.Fatal("result does not match original")
	}
}

func TestIssue41(t *testing.T) {
	r, w := io.Pipe()
	zw := lz4.NewWriter(w)
	zr := lz4.NewReader(r)

	data := "x"
	go func() {
		_, _ = fmt.Fprint(zw, data)
		_ = zw.Close()
		_ = w.Close()
	}()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(zr)
	if got, want := buf.String(), data; got != want {
		t.Fatal("uncompressed data does not match original")
	}
}

func TestIssue43(t *testing.T) {
	r, w := io.Pipe()
	go func() {
		defer w.Close()

		f, err := os.Open("testdata/issue43.data.gz")
		if err != nil {
			panic(err)
		}
		defer f.Close()

		zw := lz4.NewWriter(w)
		defer zw.Close()

		_, err = io.Copy(zw, f)
		if err != nil {
			panic(err)
		}
	}()
	_, err := io.Copy(io.Discard, lz4.NewReader(r))
	if err != nil {
		t.Fatal(err)
	}
}

func TestIssue51(t *testing.T) {
	data, err := os.ReadFile("testdata/issue51.data.gz")
	if err != nil {
		t.Fatal(err)
	}

	zbuf := make([]byte, 8192)

	n, err := lz4block.CompressBlock(data, zbuf)
	if err != nil {
		t.Fatal(err)
	}
	zbuf = zbuf[:n]

	buf := make([]byte, 8192)
	n, err = lz4block.UncompressBlock(zbuf, buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	buf = buf[:n]
	if !bytes.Equal(data, buf) {
		t.Fatal("processed data does not match input")
	}
}

func TestIssue167(t *testing.T) {
	src := []byte("\xe300000000000000\t\x00\x00")
	dst := make([]byte, 18)
	_, err := lz4.UncompressBlock(src, dst)
	if err == nil {
		t.Fatal("expected buffer too short error")
	}
}

func TestIssue71(t *testing.T) {
	for _, tc := range []string{
		"abc",               // < mfLimit
		"abcdefghijklmnopq", // > mfLimit
	} {
		t.Run(tc, func(t *testing.T) {
			src := []byte(tc)
			bound := lz4block.CompressBlockBound(len(tc))

			// Small buffer.
			zSmall := make([]byte, bound-1)
			n, err := lz4block.CompressBlock(src, zSmall)
			if err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatal("should be incompressible")
			}

			// Large enough buffer.
			zLarge := make([]byte, bound)
			n, err = lz4block.CompressBlock(src, zLarge)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				t.Fatal("should be compressible")
			}
		})
	}
}

func TestWriterFlush(t *testing.T) {
	out := new(bytes.Buffer)
	zw := lz4.NewWriter(out)
	if err := zw.Apply(); err != nil {
		t.Fatal(err)
	}
	data := strings.Repeat("0123456789", 100)
	if _, err := zw.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	// header only
	if got, want := out.Len(), 7; got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
	if err := zw.Flush(); err != nil {
		t.Fatal(err)
	}
	// header + data
	if got, want := out.Len(), 7; got == want {
		t.Fatalf("got %d, want %d", got, want)
	}
}

func TestWriterLegacy(t *testing.T) {
	goldenFiles := []string{
		"testdata/vmlinux_LZ4_19377.gz",
		"testdata/bzImage_lz4_isolated.gz",
	}

	for _, fname := range goldenFiles {
		t.Run(fname, func(t *testing.T) {
			fname := fname
			t.Parallel()

			src := loadGolden(t, fname)

			out := new(bytes.Buffer)
			zw := lz4.NewWriter(out)
			if err := zw.Apply(lz4.LegacyOption(true), lz4.CompressionLevelOption(lz4.Fast)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(zw, bytes.NewReader(src)); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}

			out2 := new(bytes.Buffer)
			zr := lz4.NewReader(out)
			if _, err := io.Copy(out2, zr); err != nil {
				t.Fatal(err)
			}

			if len(src) != out2.Len() {
				t.Fatalf("uncompressed output not correct size. %d != %d", len(src), out2.Len())
			}

			if !bytes.Equal(out2.Bytes(), src) {
				t.Fatal("uncompressed compressed output different from source")
			}
		})
	}
}

func TestWriterLegacyCommand(t *testing.T) {
	_, err := exec.LookPath("lz4")
	if err != nil {
		t.Skip("no lz4 binary to test against")
	}

	goldenFiles := []string{
		"testdata/vmlinux_LZ4_19377.gz",
		"testdata/bzImage_lz4_isolated.gz",
	}

	for _, fname := range goldenFiles {
		t.Run(fname, func(t *testing.T) {
			fname := fname
			t.Parallel()

			src := loadGolden(t, fname)

			out := new(bytes.Buffer)
			zw := lz4.NewWriter(out)
			if err := zw.Apply(lz4.LegacyOption(true), lz4.CompressionLevelOption(lz4.Fast)); err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(zw, bytes.NewReader(src)); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}

			// write to filesystem for further checking
			tmp, err := os.CreateTemp("", "")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(tmp.Name())
			if _, err := tmp.Write(out.Bytes()); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("lz4", "--test", tmp.Name())
			if _, err := cmd.Output(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWriterConcurrency(t *testing.T) {
	const someGiantFile = "testdata/vmlinux_LZ4_19377.gz"

	out := new(bytes.Buffer)
	zw := lz4.NewWriter(out)
	if err := zw.Apply(
		lz4.ConcurrencyOption(4),
		lz4.BlockSizeOption(lz4.Block4Mb),
		lz4.ChecksumOption(true)); err != nil {
		t.Fatal(err)
	}

	// Test writing a tar file.
	tw := tar.NewWriter(zw)
	stat, err := os.Stat(someGiantFile)
	if err != nil {
		t.Fatal(err)
	}
	header, err := tar.FileInfoHeader(stat, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(someGiantFile)
	if err != nil {
		t.Fatal(err)
	}
	copyBuf := make([]byte, 16<<20) // Use a 16 MiB buffer.
	if _, err := io.CopyBuffer(tw, src, copyBuf); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr := lz4.NewReader(out)
	if _, err := io.Copy(io.Discard, zr); err != nil {
		t.Fatal(err)
	}
}

// TestWriter_ResetWithoutClose verifies that Reset returns the internal buffer
// to the pool even when Close was not called. Before the fix, the buffer would
// leak because Reset did not call lz4block.Put.
func TestWriter_ResetWithoutClose(t *testing.T) {
	data := []byte(strings.Repeat("hello world ", 1000))
	buf := new(bytes.Buffer)
	zw := lz4.NewWriter(buf)

	// Write some data but do NOT close.
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}

	// Reset without Close — this should not panic or leak.
	buf.Reset()
	zw.Reset(buf)

	// The writer should still be fully functional after Reset.
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify the output decompresses correctly.
	out := new(bytes.Buffer)
	if _, err := io.Copy(out, lz4.NewReader(buf)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("decompressed data does not match original after Reset without Close")
	}
}

// zeroThenDataReader returns 0, nil on the first Read call, then delegates
// to the underlying reader. This simulates an io.Reader that occasionally
// returns zero bytes without error (allowed by the io.Reader contract).
type zeroThenDataReader struct {
	r     io.Reader
	zeros int
}

func (z *zeroThenDataReader) Read(p []byte) (int, error) {
	if z.zeros > 0 {
		z.zeros--
		return 0, nil
	}
	return z.r.Read(p)
}

// TestWriter_ReadFromZeroLengthRead verifies that ReadFrom correctly handles
// an io.Reader that returns 0 bytes without error. Before the fix, a zero-length
// read would still call write and handler with empty data.
func TestWriter_ReadFromZeroLengthRead(t *testing.T) {
	data := []byte(strings.Repeat("test data for ReadFrom ", 500))

	buf := new(bytes.Buffer)
	zw := lz4.NewWriter(buf)
	src := &zeroThenDataReader{r: bytes.NewReader(data), zeros: 3}

	n, err := zw.ReadFrom(src)
	if err != nil {
		t.Fatal(err)
	}
	if int(n) != len(data) {
		t.Fatalf("ReadFrom byte count: got %d, want %d", n, len(data))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify decompressed output matches.
	out := new(bytes.Buffer)
	if _, err := io.Copy(out, lz4.NewReader(buf)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("decompressed data does not match original after ReadFrom with zero-length reads")
	}
}

// TestWriter_ReadFromExactBlockMultiple verifies that ReadFrom returns nil when
// the source size is an exact multiple of the block size. The final io.ReadFull
// call returns (0, io.EOF); the named return err must be cleared so callers
// (notably io.Copy via the io.ReaderFrom shortcut) do not observe io.EOF as a
// success-path error, which violates the io.ReaderFrom contract.
func TestWriter_ReadFromExactBlockMultiple(t *testing.T) {
	for _, bs := range []lz4.BlockSize{lz4.Block64Kb, lz4.Block256Kb, lz4.Block1Mb} {
		t.Run(fmt.Sprintf("%d", bs), func(t *testing.T) {
			for _, blocks := range []int{1, 2, 4} {
				blocks := blocks
				t.Run(fmt.Sprintf("blocks=%d", blocks), func(t *testing.T) {
					data := bytes.Repeat([]byte("abcd"), int(bs)/4*blocks)

					buf := new(bytes.Buffer)
					zw := lz4.NewWriter(buf)
					if err := zw.Apply(lz4.BlockSizeOption(bs)); err != nil {
						t.Fatal(err)
					}

					n, err := zw.ReadFrom(bytes.NewReader(data))
					if err != nil {
						t.Fatalf("ReadFrom: got err=%v, want nil", err)
					}
					if int(n) != len(data) {
						t.Fatalf("ReadFrom byte count: got %d, want %d", n, len(data))
					}
					if err := zw.Close(); err != nil {
						t.Fatal(err)
					}

					out := new(bytes.Buffer)
					if _, err := io.Copy(out, lz4.NewReader(buf)); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(out.Bytes(), data) {
						t.Fatal("decompressed data does not match original")
					}
				})
			}
		})
	}
}
