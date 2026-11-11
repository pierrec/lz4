package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/schollz/progressbar/v3"

	"github.com/pierrec/cmdflag"
	"github.com/pierrec/lz4/v4"
)

// Uncompress uncompresses a set of files or from stdin to stdout.
func Uncompress(fs *flag.FlagSet) cmdflag.Handler {
	bench := fs.Int("bench", 0, "Run benchmark n times. No output will be written")
	return func(args ...string) (int, error) {
		zr := lz4.NewReader(nil)

		// Use stdin/stdout if no file provided.
		if len(args) == 0 {
			zr.Reset(os.Stdin)
			_, err := io.Copy(os.Stdout, zr)
			return 0, err
		}

		for fidx, zfilename := range args {
			// Input file.
			zfile, err := os.Open(zfilename)
			if err != nil {
				return fidx, err
			}

			if *bench > 0 {
				fmt.Print("Reading ", zfilename, "...")
				compressed, err := io.ReadAll(zfile)
				if err != nil {
					return fidx, err
				}
				zfile.Close()
				for i := 0; i < *bench; i++ {
					fmt.Print("\nDecompressing...")
					runtime.GC()
					start := time.Now()
					zr.Reset(bytes.NewReader(compressed))
					output, err := io.Copy(io.Discard, zr)
					if err != nil {
						return fidx, err
					}
					elapsed := time.Since(start)
					ms := elapsed.Round(time.Millisecond)
					mbPerSec := (float64(output) / 1e6) / (float64(elapsed) / (float64(time.Second)))
					pct := float64(output) * 100 / float64(len(compressed))
					fmt.Printf(" %d -> %d [%.02f%%]; %v, %.01fMB/s", len(compressed), output, pct, ms, mbPerSec)
				}
				fmt.Println("")
				continue
			}
			zinfo, err := zfile.Stat()
			if err != nil {
				return fidx, err
			}

			// Output file.
			filename := strings.TrimSuffix(zfilename, lz4Extension)
			file, err := openOutputFile(filename, zinfo)
			if err != nil {
				zfile.Close()
				return fidx, err
			}
			zr.Reset(zfile)

			zfinfo, err := zfile.Stat()
			if err != nil {
				return fidx, err
			}
			var (
				size  int
				out   io.Writer = file
				zsize           = zfinfo.Size()
				bar   *progressbar.ProgressBar
			)
			if zsize > 0 {
				bar = progressbar.NewOptions64(zsize,
					// File transfers are usually slow, make sure we display the bar at 0%.
					progressbar.OptionSetRenderBlankState(true),
					// Display the filename.
					progressbar.OptionSetDescription(filename),
					progressbar.OptionClearOnFinish(),
				)
				out = io.MultiWriter(out, bar)
				_ = zr.Apply(
					lz4.OnBlockDoneOption(func(n int) {
						size += n
					}),
				)
			}

			// Uncompress.
			_, err = io.Copy(out, zr)
			if err != nil {
				return fidx, err
			}
			for _, c := range []io.Closer{zfile, file} {
				err := c.Close()
				if err != nil {
					return fidx, err
				}
			}

			if bar != nil {
				_ = bar.Clear()
				fmt.Printf("%s %d\n", zfilename, size)
			}
		}

		return len(args), nil
	}
}
