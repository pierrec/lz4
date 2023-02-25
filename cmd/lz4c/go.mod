module github.com/pierrec/lz4/v4/cmd/lz4c

go 1.14

require (
	code.cloudfoundry.org/bytefmt v0.0.0-20211005130812-5bb3c17173e5
	github.com/pierrec/cmdflag v0.0.2
	github.com/pierrec/lz4/v4 v4.1.17
	github.com/rivo/uniseg v0.4.4 // indirect
	github.com/schollz/progressbar/v3 v3.13.0
	golang.org/x/term v0.5.0 // indirect
)

//replace github.com/pierrec/lz4/v4 => ../..
