package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"code.cloudfoundry.org/bytefmt"
	"github.com/schollz/progressbar/v3"

	"github.com/pierrec/cmdflag"
	"github.com/pierrec/lz4/v4"
)

type writer interface {
	io.WriteCloser
	Apply(options ...lz4.Option) error
	Reset(io.Writer)
}

var newWriter = func(w io.Writer) writer {
	return lz4.NewWriter(w)
}

// Compress compresses a set of files or from stdin to stdout.
func Compress(fs *flag.FlagSet) cmdflag.Handler {
	var blockMaxSize string
	fs.StringVar(&blockMaxSize, "size", "4M", "block max size [64K,256K,1M,4M]")
	var blockChecksum bool
	fs.BoolVar(&blockChecksum, "bc", false, "enable block checksum")
	var streamChecksum bool
	fs.BoolVar(&streamChecksum, "sc", false, "enable stream checksum")
	var level uint
	fs.UintVar(&level, "l", 0, "compression level (0=fastest)")
	var ccompat bool
	fs.BoolVar(&ccompat, "ccompat", false, "-l is a level of the C lz4 tool, 1 to 12, and blocks are compressed as it compresses them")
	var concurrency int
	fs.IntVar(&concurrency, "c", -1, "concurrency; -1 uses all CPUs")
	bench := fs.Int("bench", 0, "Run benchmark n times. No output will be written")
	dictFile := fs.String("D", "", "compress with the dictionary in this file, not with -ccompat; uncompress with the same one")

	return func(args ...string) (int, error) {
		levels := []lz4.CompressionLevel{lz4.Fast, lz4.Level1, lz4.Level2, lz4.Level3, lz4.Level4,
			lz4.Level5, lz4.Level6, lz4.Level7, lz4.Level8, lz4.Level9}
		if ccompat {
			// As in the C tool, 0 compresses as 1 does.
			levels = []lz4.CompressionLevel{lz4.CCompatFast, lz4.CCompatFast, lz4.CCompatLevel2,
				lz4.CCompatLevel3, lz4.CCompatLevel4, lz4.CCompatLevel5, lz4.CCompatLevel6,
				lz4.CCompatLevel7, lz4.CCompatLevel8, lz4.CCompatLevel9, lz4.CCompatLevel10,
				lz4.CCompatLevel11, lz4.CCompatLevel12}
		}
		if level >= uint(len(levels)) {
			return 0, fmt.Errorf("%w: %d", lz4.ErrOptionInvalidCompressionLevel, level)
		}
		if ccompat && *dictFile != "" {
			return 0, fmt.Errorf("%w: -D with -ccompat: the C-compatible levels take no dictionary", lz4.ErrOptionNotApplicable)
		}
		lvl := levels[level]
		sz, err := bytefmt.ToBytes(blockMaxSize)
		if err != nil {
			return 0, err
		}

		zw := newWriter(nil)
		options := []lz4.Option{
			lz4.BlockChecksumOption(blockChecksum),
			lz4.BlockSizeOption(lz4.BlockSize(sz)),
			lz4.ChecksumOption(streamChecksum),
			lz4.CompressionLevelOption(lvl),
			lz4.ConcurrencyOption(concurrency),
		}
		if *dictFile != "" {
			dict, err := os.ReadFile(*dictFile)
			if err != nil {
				return 0, err
			}
			options = append(options, lz4.DictionaryOption(dict))
		}
		if err := zw.Apply(options...); err != nil {
			return 0, err
		}

		// Use stdin/stdout if no file provided.
		if len(args) == 0 {
			zw.Reset(os.Stdout)
			_, err := io.Copy(zw, os.Stdin)
			if err != nil {
				return 0, err
			}
			return 0, zw.Close()
		}

		for fidx, filename := range args {
			// Input file.
			file, err := os.Open(filename)
			if err != nil {
				return fidx, err
			}
			if *bench > 0 {
				fmt.Print("Reading ", filename, "...")
				input, err := io.ReadAll(file)
				if err != nil {
					return fidx, err
				}
				file.Close()
				for i := 0; i < *bench; i++ {
					fmt.Print("\nCompressing...")
					runtime.GC()
					start := time.Now()
					counter := wCounter{out: io.Discard}
					zw.Reset(&counter)
					_, err := io.Copy(zw, bytes.NewReader(input))
					if err != nil {
						return fidx, err
					}
					output := counter.n
					elapsed := time.Since(start)
					ms := elapsed.Round(time.Millisecond)
					mbPerSec := (float64(len(input)) / 1e6) / (float64(elapsed) / (float64(time.Second)))
					pct := float64(output) * 100 / float64(len(input))
					fmt.Printf(" %d -> %d [%.02f%%]; %v, %.01fMB/s", len(input), output, pct, ms, mbPerSec)
				}
				fmt.Println("")
				continue
			}
			finfo, err := file.Stat()
			if err != nil {
				return fidx, err
			}

			// Output file.
			zfilename := fmt.Sprintf("%s%s", filename, lz4Extension)
			zfile, err := openOutputFile(zfilename, finfo)
			if err != nil {
				file.Close()
				return fidx, err
			}
			// Reset before applying per-file options after Close.
			zw.Reset(zfile)

			// Accumulate compressed bytes num.
			var (
				zsize atomic.Int64
				size  = finfo.Size()
			)
			if size > 0 {
				// Progress bar setup.
				numBlocks := int(size) / int(sz)
				bar := progressbar.NewOptions(numBlocks,
					// File transfers are usually slow, make sure we display the bar at 0%.
					progressbar.OptionSetRenderBlankState(true),
					// Display the filename.
					progressbar.OptionSetDescription(filename),
					progressbar.OptionClearOnFinish(),
				)
				err = zw.Apply(
					lz4.OnBlockDoneOption(func(n int) {
						_ = bar.Add(1)
						zsize.Add(int64(n))
					}),
				)
				if err != nil {
					return 0, err
				}
			}

			// Compress.
			_, err = io.Copy(zw, file)
			if err != nil {
				return fidx, err
			}
			for _, c := range []io.Closer{zw, zfile} {
				err := c.Close()
				if err != nil {
					return fidx, err
				}
			}

			if size > 0 {
				fmt.Printf("%s %.02f%%\n", zfilename, float64(zsize.Load())*100/float64(size))
			}
		}

		return len(args), nil
	}
}

type wCounter struct {
	n   int
	out io.Writer
}

func (w *wCounter) Write(p []byte) (n int, err error) {
	n, err = w.out.Write(p)
	w.n += n
	return n, err
}
