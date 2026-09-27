// Package benchdata provides the inputs for the compression benchmarks and
// golden output tests, so that results can be compared across compressors,
// changes and implementations (see the bench module for the C reference).
package benchdata

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
)

// Input is a named benchmark input.
type Input struct {
	Name string
	Data []byte
}

// BlockSizes are the block sizes the benchmarks split inputs into: the
// smallest blocks commonly used, and the frame format's 64kiB and 4MiB.
var BlockSizes = []int{4 << 10, 16 << 10, 64 << 10, 4 << 20}

const maxInput = 4 << 20

// Corpus returns the inputs: files from the testdata directory dir, and
// generated data that stands in for common structured payloads. The
// generated inputs are deterministic.
func Corpus(dir string) ([]Input, error) {
	var in []Input
	for _, f := range []struct{ name, file string }{
		{"pg1661", "pg1661.txt.gz"},               // English text
		{"twain", "Mark.Twain-Tom.Sawyer.txt.gz"}, // English text
		{"vmlinux", "vmlinux_LZ4_19377.gz"},       // executable code
		{"pgtar", "pg_control.tar.gz"},            // tar of small, mostly zero files
		{"e", "e.txt.gz"},                         // decimal digits: high entropy text
		{"random", "random.data.gz"},              // incompressible
	} {
		b, err := loadGz(filepath.Join(dir, f.file))
		if err != nil {
			return nil, err
		}
		in = append(in, Input{f.name, b})
	}

	rnd := rand.New(rand.NewSource(1))
	var js bytes.Buffer
	for js.Len() < 2<<20 {
		fmt.Fprintf(&js, `{"ts":%d,"service":"svc-%d","duration_ms":%.3f,"status":%d,"trace.trace_id":"%016x","name":"GET /api/v%d/items"}`+"\n",
			1700000000000+rnd.Int63n(1e9), rnd.Intn(20), rnd.Float64()*500, []int{200, 200, 200, 404, 500}[rnd.Intn(5)], rnd.Uint64(), rnd.Intn(3))
	}
	in = append(in, Input{"json", js.Bytes()}) // log lines

	ints := make([]byte, 2<<20)
	for i := 0; i < len(ints); i += 8 {
		binary.LittleEndian.PutUint64(ints[i:], uint64(rnd.Intn(1000)))
	}
	in = append(in, Input{"int64col", ints}) // column of small integers

	floats := make([]byte, 2<<20)
	for i := 0; i < len(floats); i += 8 {
		binary.LittleEndian.PutUint64(floats[i:], math.Float64bits(float64(rnd.Intn(100))*0.25))
	}
	in = append(in, Input{"float64col", floats}) // column of few distinct floats

	in = append(in, Input{"zeros", make([]byte, 2<<20)})
	return in, nil
}

// Blocks splits b into blocks of at most size bytes.
func Blocks(b []byte, size int) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		n := size
		if n > len(b) {
			n = len(b)
		}
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}

func loadGz(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(zr, maxInput))
}
