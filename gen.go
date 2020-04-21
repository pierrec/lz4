//+build ignore

package main

import (
	"log"
	"os"

	"github.com/pierrec/lz4"
	"github.com/pierrec/packer"
)

type DescriptorFlags struct {
	// FLG
	_                 [2]int
	ContentChecksum   [1]bool
	Size              [1]bool
	BlockChecksum     [1]bool
	BlockIndependence [1]bool
	Version           [2]uint16
	// BD
	_              [4]int
	BlockSizeIndex [3]lz4.BlockSizeIndex
	_              [1]int
}

type DataBlockSize struct {
	size         [31]int
	uncompressed bool
}

func main() {
	out, err := os.Create("frame_gen.go")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	pkg := "lz4"
	for i, t := range []interface{}{
		DescriptorFlags{}, DataBlockSize{},
	} {
		if i > 0 {
			pkg = ""
		}
		err := packer.GenPackedStruct(out, &packer.Config{PkgName: pkg}, t)
		if err != nil {
			log.Fatalf("%T: %v", t, err)
		}
	}
}
