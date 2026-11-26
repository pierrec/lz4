package lz4_test

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

type flushBlockingWriter struct {
	bytes.Buffer
	entered chan struct{}
	release chan struct{}
	writes  int
}

func (w *flushBlockingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == 3 {
		close(w.entered)
		<-w.release
	}
	return w.Buffer.Write(p)
}

func TestWriterFlushBufferOwnership(t *testing.T) {
	for _, concurrency := range []int{2, 4} {
		t.Run(fmt.Sprintf("concurrency=%d", concurrency), func(t *testing.T) {
			out := &flushBlockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
			zw := lz4.NewWriter(out)
			if err := zw.Apply(lz4.ConcurrencyOption(concurrency), lz4.BlockSizeOption(lz4.Block64Kb)); err != nil {
				t.Fatal(err)
			}
			first := make([]byte, 4096)
			_, _ = rand.New(rand.NewSource(192)).Read(first)
			second := bytes.Repeat([]byte{0xff}, len(first))
			if _, err := zw.Write(first); err != nil {
				t.Fatal(err)
			}
			if err := zw.Flush(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-out.entered:
			case <-time.After(5 * time.Second):
				close(out.release)
				t.Fatal("timed out waiting for block write")
			}
			n, err := zw.Write(second)
			close(out.release)
			if err != nil || n != len(second) {
				t.Fatalf("Write: got (%d, %v), want (%d, nil)", n, err, len(second))
			}
			if err := zw.Flush(); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(lz4.NewReader(&out.Buffer))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, append(first, second...)) {
				t.Fatal("uncompressed data does not match original after Flush followed by Write")
			}
		})
	}
}

func TestWriterFlushRepeated(t *testing.T) {
	for _, concurrency := range []int{1, 2, 4} {
		for _, level := range []lz4.CompressionLevel{lz4.Fast, lz4.Level1} {
			for _, checksum := range []bool{false, true} {
				t.Run(fmt.Sprintf("concurrency=%d/level=%d/checksum=%t", concurrency, level, checksum), func(t *testing.T) {
					var out, want bytes.Buffer
					zw := lz4.NewWriter(&out)
					if err := zw.Apply(lz4.ConcurrencyOption(concurrency), lz4.BlockSizeOption(lz4.Block64Kb), lz4.CompressionLevelOption(level), lz4.ChecksumOption(checksum), lz4.BlockChecksumOption(checksum)); err != nil {
						t.Fatal(err)
					}
					if err := zw.Flush(); err != nil {
						t.Fatal(err)
					}
					rng := rand.New(rand.NewSource(192))
					for i := range 32 {
						sizes := []int{1, 4096, int(lz4.Block64Kb) - 1, int(lz4.Block64Kb), int(lz4.Block64Kb) + 1}
						data := bytes.Repeat([]byte{byte(i)}, sizes[i%len(sizes)])
						if i%2 == 0 {
							_, _ = rng.Read(data)
						}
						want.Write(data)
						if n, err := zw.Write(data); err != nil || n != len(data) {
							t.Fatalf("Write: got (%d, %v), want (%d, nil)", n, err, len(data))
						}
						if i == 31 {
							break
						}
						for range 2 {
							if err := zw.Flush(); err != nil {
								t.Fatal(err)
							}
						}
					}
					if err := zw.Close(); err != nil {
						t.Fatal(err)
					}
					got, err := io.ReadAll(lz4.NewReader(&out))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, want.Bytes()) {
						t.Fatal("uncompressed data does not match original")
					}
				})
			}
		}
	}
}

type flushErrorWriter struct {
	writes int
	failAt int
	once   bool // fail only write failAt, as with a transient error
}

func (w *flushErrorWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt || !w.once && w.writes > w.failAt {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

// A failed Flush fails the Writer until Reset, even if the error was
// transient: retrying the block could duplicate the part already written.
func TestWriterFlushError(t *testing.T) {
	for _, concurrency := range []int{1, 2, 4} {
		for _, failAt := range []int{2, 3} {
			for _, once := range []bool{false, true} {
				t.Run(fmt.Sprintf("concurrency=%d/failAt=%d/once=%v", concurrency, failAt, once), func(t *testing.T) {
					testWriterFlushError(t, concurrency, failAt, once)
				})
			}
		}
	}
}

func testWriterFlushError(t *testing.T, concurrency, failAt int, once bool) {
	dst := &flushErrorWriter{failAt: failAt, once: once}
	zw := lz4.NewWriter(dst)
	if err := zw.Apply(lz4.ConcurrencyOption(concurrency), lz4.BlockSizeOption(lz4.Block64Kb)); err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write([]byte("first block")); err != nil {
		t.Fatal(err)
	}
	err := zw.Flush()
	if concurrency == 1 {
		if err != io.ErrClosedPipe {
			t.Fatalf("Flush: got %v, want %v", err, io.ErrClosedPipe)
		}
		writes := dst.writes
		if n, err := zw.Write([]byte("more")); !errors.Is(err, io.ErrClosedPipe) || n != 0 {
			t.Fatalf("Write after failed Flush: n=%d err=%v", n, err)
		}
		if n, err := zw.ReadFrom(bytes.NewReader([]byte("more"))); !errors.Is(err, io.ErrClosedPipe) || n != 0 {
			t.Fatalf("ReadFrom after failed Flush: n=%d err=%v", n, err)
		}
		if err := zw.Flush(); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Flush after failed Flush: %v", err)
		}
		if err := zw.Close(); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Close after failed Flush: %v", err)
		}
		if dst.writes != writes {
			t.Fatalf("%d writes after failed Flush", dst.writes-writes)
		}
		var buf bytes.Buffer
		zw.Reset(&buf)
		if _, err := zw.Write([]byte("recovered")); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if out, err := io.ReadAll(lz4.NewReader(&buf)); err != nil || string(out) != "recovered" {
			t.Fatalf("after Reset: %q, %v", out, err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if _, err := zw.Write([]byte("next block")); err != nil {
			t.Fatal(err)
		}
		if err := zw.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != io.ErrClosedPipe {
		t.Fatalf("Close: got %v, want %v", err, io.ErrClosedPipe)
	}
}

// A zero-value Writer, not made by NewWriter, fails instead of panicking.
func TestWriterZeroValue(t *testing.T) {
	var zw lz4.Writer
	if err := zw.Flush(); !errors.Is(err, lz4.ErrInternalUnhandledState) {
		t.Fatalf("Flush: %v", err)
	}
	if err := zw.Close(); !errors.Is(err, lz4.ErrInternalUnhandledState) {
		t.Fatalf("Close: %v", err)
	}
}

func BenchmarkWriterFlush(b *testing.B) {
	for _, concurrency := range []int{1, 4} {
		b.Run(fmt.Sprintf("concurrency=%d", concurrency), func(b *testing.B) {
			data := bytes.Repeat([]byte("0123456789abcdef"), 256)
			b.SetBytes(int64(len(data) * 32))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				zw := lz4.NewWriter(io.Discard)
				if err := zw.Apply(lz4.ConcurrencyOption(concurrency), lz4.BlockSizeOption(lz4.Block64Kb)); err != nil {
					b.Fatal(err)
				}
				for range 32 {
					if _, err := zw.Write(data); err != nil {
						b.Fatal(err)
					}
					if err := zw.Flush(); err != nil {
						b.Fatal(err)
					}
				}
				if err := zw.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// legacyInputs returns the inputs for the legacy writer tests. The random
// one spans more than two 8MB blocks, each of which expands when compressed.
func legacyInputs(t *testing.T) []struct {
	name string
	src  []byte
} {
	random := make([]byte, 2*lz4block.Block8Mb+12345)
	_, _ = rand.New(rand.NewSource(156)).Read(random)
	return []struct {
		name string
		src  []byte
	}{
		{"vmlinux", loadGolden(t, "testdata/vmlinux_LZ4_19377.gz")},
		{"random", random},
	}
}

// checkLegacyBlocks fails unless frame is a legacy frame whose block sizes
// are all at most CompressBlockBound(8MB). Legacy frames have no
// uncompressed flag: the C decoder and the Linux kernel stop at one.
func checkLegacyBlocks(t *testing.T, frame []byte) {
	t.Helper()
	const magic = 0x184C2102
	bound := uint32(lz4block.CompressBlockBound(int(lz4block.Block8Mb)))
	if len(frame) < 4 || binary.LittleEndian.Uint32(frame) != magic {
		t.Fatal("missing legacy magic")
	}
	for b := frame[4:]; len(b) > 0; {
		if len(b) < 4 {
			t.Fatalf("truncated block header: %x", b)
		}
		size := binary.LittleEndian.Uint32(b)
		if size > bound {
			t.Fatalf("invalid legacy block size %#x", size)
		}
		if int(size) > len(b)-4 {
			t.Fatalf("block size %d overruns the frame", size)
		}
		b = b[4+size:]
	}
}

func TestWriterLegacy(t *testing.T) {
	options := []struct {
		name string
		opts []lz4.Option
	}{
		{"Fast", []lz4.Option{lz4.CompressionLevelOption(lz4.Fast)}},
		{"CCompatFast", []lz4.Option{lz4.CompressionLevelOption(lz4.CCompatFast)}},
		{"Level1", []lz4.Option{lz4.CompressionLevelOption(lz4.Level1)}},
		{"Concurrency4", []lz4.Option{lz4.ConcurrencyOption(4)}},
	}
	for _, in := range legacyInputs(t) {
		for _, o := range options {
			t.Run(in.name+"/"+o.name, func(t *testing.T) {
				t.Parallel()

				out := new(bytes.Buffer)
				zw := lz4.NewWriter(out)
				if err := zw.Apply(append([]lz4.Option{lz4.LegacyOption(true)}, o.opts...)...); err != nil {
					t.Fatal(err)
				}
				if _, err := io.Copy(zw, bytes.NewReader(in.src)); err != nil {
					t.Fatal(err)
				}
				if err := zw.Close(); err != nil {
					t.Fatal(err)
				}
				checkLegacyBlocks(t, out.Bytes())

				out2 := new(bytes.Buffer)
				zr := lz4.NewReader(out)
				if _, err := io.Copy(out2, zr); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(out2.Bytes(), in.src) {
					t.Fatalf("round trip mismatch: got %d bytes; want %d", out2.Len(), len(in.src))
				}
			})
		}
	}
}

// TestWriterCommand checks that the lz4 command decompresses the Writer's
// output to the original bytes. lz4 --test exits 0 even when it stops at
// undecodable data, so compare the output. CI runs it in the silesia job.
func TestWriterCommand(t *testing.T) {
	lz4cmd, err := exec.LookPath("lz4")
	if err != nil {
		t.Skip("no lz4 binary to test against")
	}
	options := []struct {
		name string
		opts []lz4.Option
	}{
		{"Fast", nil},
		{"CCompatFast-Block64Kb", []lz4.Option{lz4.CompressionLevelOption(lz4.CCompatFast), lz4.BlockSizeOption(lz4.Block64Kb)}},
		{"Level1-BlockChecksum", []lz4.Option{lz4.CompressionLevelOption(lz4.Level1), lz4.BlockChecksumOption(true)}},
		{"Concurrency4-Block1Mb", []lz4.Option{lz4.ConcurrencyOption(4), lz4.BlockSizeOption(lz4.Block1Mb)}},
		{"Legacy-Fast", []lz4.Option{lz4.LegacyOption(true)}},
		{"Legacy-Level1", []lz4.Option{lz4.LegacyOption(true), lz4.CompressionLevelOption(lz4.Level1)}},
	}
	for _, in := range legacyInputs(t) {
		for _, o := range options {
			t.Run(in.name+"/"+o.name, func(t *testing.T) {
				t.Parallel()

				out := new(bytes.Buffer)
				zw := lz4.NewWriter(out)
				if err := zw.Apply(o.opts...); err != nil {
					t.Fatal(err)
				}
				if _, err := io.Copy(zw, bytes.NewReader(in.src)); err != nil {
					t.Fatal(err)
				}
				if err := zw.Close(); err != nil {
					t.Fatal(err)
				}

				cmd := exec.Command(lz4cmd, "-d", "-c")
				cmd.Stdin = out
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				got, err := cmd.Output()
				if err != nil {
					t.Fatalf("%v: %s", err, stderr.Bytes())
				}
				if !bytes.Equal(got, in.src) {
					t.Fatalf("lz4 output mismatch: got %d bytes; want %d: %s", len(got), len(in.src), stderr.Bytes())
				}
			})
		}
	}
}

// TestDictCommand checks that the lz4 command and this package decode each
// other's frames compressed with a dictionary: lz4 -D at levels 1 and 9, with
// independent and linked blocks, read by Reader, and Writer's frames
// decompressed by lz4 -d -D. CI runs it in the silesia job.
func TestDictCommand(t *testing.T) {
	lz4cmd, err := exec.LookPath("lz4")
	if err != nil {
		t.Skip("no lz4 binary to test against")
	}
	pg := loadGolden(t, "testdata/pg1661.txt.lz4")
	data := pg[100<<10 : 356<<10]
	for _, dict := range [][]byte{
		pg[:100],     // shorter than the window
		pg[:64<<10],  // the window
		pg[:100<<10], // longer: only its last 64 KiB are used
	} {
		dictFile := filepath.Join(t.TempDir(), "dict")
		if err := os.WriteFile(dictFile, dict, 0o600); err != nil {
			t.Fatal(err)
		}
		runLZ4 := func(t *testing.T, in []byte, args ...string) []byte {
			t.Helper()
			cmd := exec.Command(lz4cmd, append(args, "-D", dictFile, "-c")...)
			cmd.Stdin = bytes.NewReader(in)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("lz4 %v: %v: %s", args, err, stderr.Bytes())
			}
			return out
		}
		name := fmt.Sprintf("dict%d", len(dict))

		for _, args := range [][]string{{"-1", "-BI"}, {"-9", "-BI"}, {"-1", "-BD"}, {"-9", "-BD"}} {
			t.Run(name+"/lz4"+strings.Join(args, ""), func(t *testing.T) {
				frame := runLZ4(t, data, append(args, "-B4")...) // 64 KiB blocks
				for _, conc := range []int{1, 4} {
					zr := lz4.NewReader(bytes.NewReader(frame))
					if err := zr.Apply(lz4.DictionaryOption(dict), lz4.ConcurrencyOption(conc)); err != nil {
						t.Fatal(err)
					}
					got, err := io.ReadAll(zr)
					if err != nil || !bytes.Equal(got, data) {
						t.Fatalf("Reader with concurrency %d: %d bytes, %v", conc, len(got), err)
					}
				}
				// Without the dictionary, the frame does not decode.
				if _, err := io.ReadAll(lz4.NewReader(bytes.NewReader(frame))); err == nil {
					t.Fatal("decoded without the dictionary")
				}
			})
		}

		t.Run(name+"/Writer", func(t *testing.T) {
			var frame bytes.Buffer
			zw := lz4.NewWriter(&frame)
			if err := zw.Apply(lz4.BlockSizeOption(lz4.Block64Kb), lz4.DictionaryOption(dict)); err != nil {
				t.Fatal(err)
			}
			if _, err := zw.Write(data); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			if got := runLZ4(t, frame.Bytes(), "-d"); !bytes.Equal(got, data) {
				t.Fatalf("lz4 output mismatch: got %d bytes; want %d", len(got), len(data))
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
		lz4.ChecksumOption(true),
	); err != nil {
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

// OnBlockDone reports each block's compressed size once, whether the data
// arrives through Write or ReadFrom.
func TestWriter_OnBlockDone(t *testing.T) {
	data := bytes.Repeat([]byte("onblockdone "), 3*int(lz4.Block64Kb)/12+1000)
	var want int
	for _, tc := range []struct {
		name     string
		conc     int
		readFrom bool
	}{
		{"Write", 1, false},
		{"Write concurrent", 4, false},
		{"ReadFrom", 1, true},
		{"ReadFrom concurrent", 4, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var done atomic.Int64
			buf := new(bytes.Buffer)
			zw := lz4.NewWriter(buf)
			err := zw.Apply(
				lz4.BlockSizeOption(lz4.Block64Kb),
				lz4.ConcurrencyOption(tc.conc),
				lz4.OnBlockDoneOption(func(n int) { done.Add(int64(n)) }),
			)
			if err != nil {
				t.Fatal(err)
			}
			if tc.readFrom {
				_, err = zw.ReadFrom(bytes.NewReader(data))
			} else {
				_, err = zw.Write(data)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			got := int(done.Load())
			if got <= 0 || got >= buf.Len() {
				t.Fatalf("OnBlockDone total %d, want between 0 and the frame size %d", got, buf.Len())
			}
			if want == 0 {
				want = got
			}
			if got != want {
				t.Fatalf("OnBlockDone total %d, want %d as with Write", got, want)
			}
		})
	}
}

// Ensure a Close followed by a Reset does not block forever
// if concurrency is > 1.
func TestWriterConcurrentCloseThenReset(t *testing.T) {
	in := bytes.Repeat([]byte("payload "), 100000)
	var buf bytes.Buffer
	zw := lz4.NewWriter(&buf)
	if err := zw.Apply(lz4.ConcurrencyOption(4)); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		buf.Reset()
		zw.Reset(&buf)
		if _, err := zw.Write(in); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(lz4.NewReader(&buf))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, in) {
			t.Fatalf("round %d: output mismatch", i)
		}
	}
}

// After Close, a second Close is a no-op and a Write fails.
func TestWriterAfterClose(t *testing.T) {
	in := bytes.Repeat([]byte("payload "), 1000)
	var buf bytes.Buffer
	zw := lz4.NewWriter(&buf)
	if _, err := zw.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	size := buf.Len()
	if err := zw.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if n, err := zw.Write(in); err != lz4.ErrWriterClosed || n != 0 {
		t.Fatalf("Write after Close: n=%d err=%v", n, err)
	}
	if n, err := zw.ReadFrom(bytes.NewReader(in)); err != lz4.ErrWriterClosed || n != 0 {
		t.Fatalf("ReadFrom after Close: n=%d err=%v", n, err)
	}
	if err := zw.Flush(); err != nil {
		t.Fatalf("Flush after Close: %v", err)
	}
	if buf.Len() != size {
		t.Fatalf("closed writer appended %d bytes", buf.Len()-size)
	}
	out, err := io.ReadAll(lz4.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Fatal("output mismatch")
	}
	// Reset makes the writer usable again.
	buf.Reset()
	zw.Reset(&buf)
	if _, err := zw.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if out, err := io.ReadAll(lz4.NewReader(&buf)); err != nil || !bytes.Equal(out, in) {
		t.Fatalf("after Reset: err %v, match %v", err, bytes.Equal(out, in))
	}
}

// Legacy frames do not use the shared (non legacy) descriptor; this test
// ensures:
// * a Writer never adds block checksums to a legacy frame
// * a Writer keeps its configured block size for the frames after a legacy frame
// * a Reader does not carry the previous frame's flags into a legacy frame
func TestLegacyFrameKeepsDescriptor(t *testing.T) {
	in := bytes.Repeat([]byte("payload "), 100000)
	var buf bytes.Buffer
	zw := lz4.NewWriter(&buf)
	if err := zw.Apply(lz4.BlockSizeOption(lz4.Block64Kb), lz4.BlockChecksumOption(true), lz4.LegacyOption(true)); err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	legacy := append([]byte{}, buf.Bytes()...)
	if out, err := io.ReadAll(lz4.NewReader(bytes.NewReader(legacy))); err != nil || !bytes.Equal(out, in) {
		t.Fatalf("legacy frame with a block checksum option: err %v, match %v", err, bytes.Equal(out, in))
	}

	buf.Reset()
	zw.Reset(&buf)
	if err := zw.Apply(lz4.LegacyOption(false)); err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if bd := buf.Bytes()[5] >> 4 & 7; bd != 4 {
		t.Fatalf("frame after a legacy frame has block size index %d, want 4 (64Kb)", bd)
	}
	if out, err := io.ReadAll(lz4.NewReader(&buf)); err != nil || !bytes.Equal(out, in) {
		t.Fatalf("frame after a legacy frame: err %v, match %v", err, bytes.Equal(out, in))
	}

	buf.Reset()
	zw.Reset(&buf)
	if err := zw.Apply(lz4.BlockChecksumOption(true)); err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr := lz4.NewReader(&buf)
	if _, err := io.Copy(io.Discard, zr); err != nil {
		t.Fatal(err)
	}
	zr.Reset(bytes.NewReader(legacy))
	var out bytes.Buffer
	if _, err := io.Copy(&out, zr); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), in) {
		t.Fatalf("legacy frame after a checksummed frame: got %d bytes, want %d", out.Len(), len(in))
	}
}

// A block checksum is written whenever the option is set, even when the
// checksum value happens to be zero. Previously, zero was skipped, which broke
// the reader.
func TestWriterZeroBlockChecksum(t *testing.T) {
	zero := []byte{0x27, 0x11, 0x4b, 0x23} // too short to compress; xxh32 is zero
	rest := []byte("second block")
	var buf bytes.Buffer
	zw := lz4.NewWriter(&buf)
	if err := zw.Apply(lz4.BlockChecksumOption(true)); err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(zero); err != nil {
		t.Fatal(err)
	}
	if err := zw.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(rest); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(lz4.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if want := append(zero, rest...); !bytes.Equal(out, want) {
		t.Fatalf("got % x, want % x", out, want)
	}
}

func TestWriter_DictionaryRoundTrip(t *testing.T) {
	dict := []byte("schema:id,name,email,timestamp,event,status,metadata,payload,response")
	src := []byte("schema:id=1001,name=Alice,email=alice@example.com,timestamp=1690000000,event=login,status=ok\n" +
		"schema:id=1002,name=Bob,email=bob@example.com,timestamp=1690000001,event=logout,status=ok\n")

	// Compress with dictionary.
	var bufWithDict bytes.Buffer
	zwWithDict := lz4.NewWriter(&bufWithDict)
	if err := zwWithDict.Apply(lz4.DictionaryOption(dict)); err != nil {
		t.Fatalf("zwWithDict Apply DictionaryOption failed: %v", err)
	}
	if _, err := zwWithDict.Write(src); err != nil {
		t.Fatalf("zwWithDict Write failed: %v", err)
	}
	if err := zwWithDict.Close(); err != nil {
		t.Fatalf("zwWithDict Close failed: %v", err)
	}

	// Compress without dictionary.
	var bufNoDict bytes.Buffer
	zwNoDict := lz4.NewWriter(&bufNoDict)
	if _, err := zwNoDict.Write(src); err != nil {
		t.Fatalf("zwNoDict Write failed: %v", err)
	}
	if err := zwNoDict.Close(); err != nil {
		t.Fatalf("zwNoDict Close failed: %v", err)
	}

	if bufWithDict.Len() >= bufNoDict.Len() {
		t.Fatalf("expected dictionary-compressed frame (%d bytes) to be smaller than no-dict (%d bytes)",
			bufWithDict.Len(), bufNoDict.Len())
	}

	// Decompress with dictionary.
	zr := lz4.NewReader(bytes.NewReader(bufWithDict.Bytes()))
	if err := zr.Apply(lz4.DictionaryOption(dict)); err != nil {
		t.Fatalf("zr Apply DictionaryOption failed: %v", err)
	}
	decompressed, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("zr ReadAll failed: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatalf("decompressed data mismatch")
	}

	// Multi-block round trip, twice with the same Writer so that the second
	// frame reuses the dictionary table primed for the first.
	zwMulti := lz4.NewWriter(nil)
	if err := zwMulti.Apply(lz4.BlockSizeOption(lz4.Block64Kb), lz4.DictionaryOption(dict)); err != nil {
		t.Fatalf("zwMulti Apply failed: %v", err)
	}
	for frame := range 2 {
		var multiSrc bytes.Buffer
		for i := 0; multiSrc.Len() < 4*64<<10; i++ {
			fmt.Fprintf(&multiSrc, "schema:id=%d,name=User%d,status=active,payload=frame_%d_item_%d\n", i, i, frame, i)
		}
		multiData := multiSrc.Bytes()

		var multiBuf bytes.Buffer
		zwMulti.Reset(&multiBuf)
		if _, err := zwMulti.Write(multiData); err != nil {
			t.Fatalf("frame %d: zwMulti Write failed: %v", frame, err)
		}
		if err := zwMulti.Close(); err != nil {
			t.Fatalf("frame %d: zwMulti Close failed: %v", frame, err)
		}

		zrMulti := lz4.NewReader(bytes.NewReader(multiBuf.Bytes()))
		if err := zrMulti.Apply(lz4.DictionaryOption(dict)); err != nil {
			t.Fatalf("frame %d: zrMulti Apply failed: %v", frame, err)
		}
		multiOut, err := io.ReadAll(zrMulti)
		if err != nil {
			t.Fatalf("frame %d: zrMulti ReadAll failed: %v", frame, err)
		}
		if !bytes.Equal(multiOut, multiData) {
			t.Fatalf("frame %d: multi-block decompressed data mismatch", frame)
		}
	}
}

// TestWriter_DictionaryConcurrency checks that a Writer with a dictionary
// compresses concurrently, at Fast and HC levels, writing the same frame as without concurrency, as
// each block refers only to the dictionary, and that the Reader decodes it
// concurrently.
func TestWriter_DictionaryConcurrency(t *testing.T) {
	dict := []byte("schema:id,name,status,payload\n")
	var src bytes.Buffer
	for i := 0; src.Len() < 10*64<<10; i++ {
		fmt.Fprintf(&src, "schema:id=%d,name=User%d,status=active,payload=item_%d\n", i, i%97, i)
	}
	data := src.Bytes()

	for _, level := range []lz4.CompressionLevel{lz4.Fast, lz4.Level2, lz4.Level9} {
		var frames [][]byte
		for _, conc := range []int{1, 4} {
			var blocks atomic.Int64
			var buf bytes.Buffer
			zw := lz4.NewWriter(&buf)
			if err := zw.Apply(lz4.BlockSizeOption(lz4.Block64Kb), lz4.ConcurrencyOption(conc), lz4.CompressionLevelOption(level),
				lz4.DictionaryOption(dict), lz4.OnBlockDoneOption(func(int) { blocks.Add(1) })); err != nil {
				t.Fatal(err)
			}
			// Two frames with the same Writer, so that the second reuses the
			// Compressors that indexed the dictionary.
			for range 2 {
				buf.Reset()
				zw.Reset(&buf)
				if _, err := zw.Write(data); err != nil {
					t.Fatal(err)
				}
				if err := zw.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if want := int64(2 * ((len(data) + 64<<10 - 1) / (64 << 10))); blocks.Load() != want {
				t.Fatalf("%s: concurrency %d: %d blocks, want %d", level, conc, blocks.Load(), want)
			}
			frames = append(frames, bytes.Clone(buf.Bytes()))
		}
		if !bytes.Equal(frames[0], frames[1]) {
			t.Fatalf("%s: concurrent Writer wrote a different frame", level)
		}
		for _, conc := range []int{1, 4} {
			zr := lz4.NewReader(bytes.NewReader(frames[1]))
			if err := zr.Apply(lz4.ConcurrencyOption(conc), lz4.DictionaryOption(dict)); err != nil {
				t.Fatal(err)
			}
			out, err := io.ReadAll(zr)
			if err != nil || !bytes.Equal(out, data) {
				t.Fatalf("%s: Reader concurrency %d: %d bytes, %v", level, conc, len(out), err)
			}
		}
		// The blocks refer to the dictionary, so that the checks above
		// would fail if either Writer ignored it.
		if out, err := io.ReadAll(lz4.NewReader(bytes.NewReader(frames[1]))); err == nil && bytes.Equal(out, data) {
			t.Fatalf("%s: frame decodes without the dictionary", level)
		}
	}
}

func TestWriter_DictionaryOptionsValidation(t *testing.T) {
	dict := []byte("test dictionary content")

	// A dictionary works with Fast and Level1 to Level9, not the C-compatible
	// levels, and only in non-legacy frames, whichever option is applied first.
	for _, tc := range []struct {
		name string
		opts []lz4.Option
	}{
		{"level then dict", []lz4.Option{lz4.CompressionLevelOption(lz4.CCompatFast), lz4.DictionaryOption(dict)}},
		{"dict then level", []lz4.Option{lz4.DictionaryOption(dict), lz4.CompressionLevelOption(lz4.CCompatFast)}},
		{"C level 2 then dict", []lz4.Option{lz4.CompressionLevelOption(lz4.CCompatLevel2), lz4.DictionaryOption(dict)}},
		{"dict then C level 12", []lz4.Option{lz4.DictionaryOption(dict), lz4.CompressionLevelOption(lz4.CCompatLevel12)}},
		{"legacy then dict", []lz4.Option{lz4.LegacyOption(true), lz4.DictionaryOption(dict)}},
		{"dict then legacy", []lz4.Option{lz4.DictionaryOption(dict), lz4.LegacyOption(true)}},
	} {
		if err := lz4.NewWriter(io.Discard).Apply(tc.opts...); !errors.Is(err, lz4.ErrOptionNotApplicable) {
			t.Errorf("%s: got %v, want ErrOptionNotApplicable", tc.name, err)
		}
	}

	// Caller slice mutation and spare capacity do not affect dictionary compression/decompression.
	rawDict := make([]byte, len(dict), len(dict)+64)
	copy(rawDict, dict)
	opt := lz4.DictionaryOption(rawDict)

	var compressed bytes.Buffer
	zw3 := lz4.NewWriter(&compressed)
	if err := zw3.Apply(opt); err != nil {
		t.Fatalf("zw3 Apply failed: %v", err)
	}
	src := []byte("test dictionary content repeated payload for round trip check")
	if _, err := zw3.Write(src); err != nil {
		t.Fatalf("zw3 Write failed: %v", err)
	}
	if err := zw3.Close(); err != nil {
		t.Fatalf("zw3 Close failed: %v", err)
	}

	// Mutate caller slice and its spare capacity.
	for i := range rawDict {
		rawDict[i] = 0xAA
	}
	_ = append(rawDict, "polluted spare capacity bytes"...)

	zr := lz4.NewReader(bytes.NewReader(compressed.Bytes()))
	if err := zr.Apply(opt); err != nil {
		t.Fatalf("zr Apply failed: %v", err)
	}
	decompressed, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("zr ReadAll failed: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatalf("decoded output mismatch after mutating caller dictionary")
	}
}

func TestWriterReadFromAfterWrite(t *testing.T) {
	const block = int(lz4.Block64Kb)
	for _, concurrency := range []int{1, 4} {
		for _, prefixSize := range []int{0, 1, block, block + 1} {
			for _, tc := range []struct {
				name       string
				readPrefix bool
				flush      bool
				middle     string
			}{
				{name: "Write-ReadFrom", middle: "ReadFrom"},
				{name: "ReadFrom-ReadFrom", readPrefix: true, middle: "ReadFrom"},
				{name: "Write-CopyN", middle: "CopyN"},
				{name: "Write-Flush-ReadFrom", flush: true, middle: "ReadFrom"},
				{name: "ReadFrom-Write", readPrefix: true, middle: "Write"},
				{name: "Write-Write", middle: "Write"},
			} {
				t.Run(fmt.Sprintf("concurrency=%d/prefix=%d/%s", concurrency, prefixSize, tc.name), func(t *testing.T) {
					prefix := bytes.Repeat([]byte("p"), prefixSize)
					middle := bytes.Repeat([]byte("m"), block+17)
					suffix := []byte("trailer")
					var frame bytes.Buffer
					zw := lz4.NewWriter(&frame)
					if err := zw.Apply(lz4.ConcurrencyOption(concurrency), lz4.BlockSizeOption(lz4.Block64Kb)); err != nil {
						t.Fatal(err)
					}
					var n int64
					var err error
					if tc.readPrefix {
						n, err = zw.ReadFrom(bytes.NewReader(prefix))
					} else {
						var written int
						written, err = zw.Write(prefix)
						n = int64(written)
					}
					if err != nil || n != int64(len(prefix)) {
						t.Fatalf("prefix: n=%d, err=%v; want %d, nil", n, err, len(prefix))
					}
					if tc.flush {
						if err := zw.Flush(); err != nil {
							t.Fatal(err)
						}
					}
					switch tc.middle {
					case "ReadFrom":
						n, err = zw.ReadFrom(bytes.NewReader(middle))
					case "CopyN":
						n, err = io.CopyN(zw, bytes.NewReader(middle), int64(len(middle)))
					case "Write":
						var written int
						written, err = zw.Write(middle)
						n = int64(written)
					}
					if err != nil || n != int64(len(middle)) {
						t.Fatalf("middle: n=%d, err=%v; want %d, nil", n, err, len(middle))
					}
					if _, err := zw.Write(suffix); err != nil {
						t.Fatal(err)
					}
					if err := zw.Close(); err != nil {
						t.Fatal(err)
					}
					out, err := io.ReadAll(lz4.NewReader(bytes.NewReader(frame.Bytes())))
					if err != nil {
						t.Fatal(err)
					}
					want := append(append(prefix, middle...), suffix...)
					if !bytes.Equal(out, want) {
						t.Fatalf("round trip: got %d bytes, want %d in original order", len(out), len(want))
					}
				})
			}
		}
	}
}

// TestWriterReadFromComposesLikeWrite checks that feeding a frame through
// repeated io.CopyN (Writer.ReadFrom) produces the same frame as Write with the
// same chunking: ReadFrom must not end a block early.
func TestWriterReadFromComposesLikeWrite(t *testing.T) {
	data := bytes.Repeat([]byte(`{"ts":"2026-11-07T00:00:00Z","svc":"api","dur_ms":12.5,"status":200}`+"\n"), 20000)
	for _, concurrency := range []int{1, 4} {
		for _, chunk := range []int{100, 4096, int(lz4.Block64Kb), int(lz4.Block64Kb) + 1, 1 << 20} {
			t.Run(fmt.Sprintf("concurrency=%d/chunk=%d", concurrency, chunk), func(t *testing.T) {
				frame := func(useReadFrom bool) []byte {
					var buf bytes.Buffer
					zw := lz4.NewWriter(&buf)
					if err := zw.Apply(lz4.ConcurrencyOption(concurrency)); err != nil {
						t.Fatal(err)
					}
					for rest := data; len(rest) > 0; {
						n := min(chunk, len(rest))
						var err error
						if useReadFrom {
							_, err = io.CopyN(zw, bytes.NewReader(rest[:n]), int64(n))
						} else {
							_, err = zw.Write(rest[:n])
						}
						if err != nil {
							t.Fatal(err)
						}
						rest = rest[n:]
					}
					if err := zw.Close(); err != nil {
						t.Fatal(err)
					}
					return buf.Bytes()
				}
				got, want := frame(true), frame(false)
				if !bytes.Equal(got, want) {
					t.Fatalf("io.CopyN frame is %d bytes, Write frame is %d bytes; want identical frames", len(got), len(want))
				}
			})
		}
	}
}

// TestWriterReadFromWriteError checks that a failed block write in ReadFrom
// is returned, counts the bytes taken from r and leaves the Writer failed.
// With concurrency the error surfaces later; TestWriterFlushError covers that.
func TestWriterReadFromWriteError(t *testing.T) {
	const block = int(lz4.Block64Kb)
	// The frame descriptor is the first write and the first block the second.
	zw := lz4.NewWriter(&flushErrorWriter{failAt: 2})
	if err := zw.Apply(lz4.ConcurrencyOption(1), lz4.BlockSizeOption(lz4.Block64Kb)); err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write([]byte("buffered prefix")); err != nil {
		t.Fatal(err)
	}
	src := bytes.NewReader(make([]byte, 2*block))
	n, err := zw.ReadFrom(src)
	if err != io.ErrClosedPipe {
		t.Fatalf("ReadFrom: got %v, want %v", err, io.ErrClosedPipe)
	}
	if taken := int64(2*block - src.Len()); n != taken {
		t.Fatalf("ReadFrom: n=%d, but it took %d bytes from r", n, taken)
	}
	if _, err := zw.Write([]byte("more")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Write after the error: got %v, want %v", err, io.ErrClosedPipe)
	}
	if _, err := zw.ReadFrom(bytes.NewReader([]byte("more"))); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("ReadFrom after the error: got %v, want %v", err, io.ErrClosedPipe)
	}
	if err := zw.Close(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Close after the error: got %v, want %v", err, io.ErrClosedPipe)
	}
	zw.Reset(io.Discard)
}
