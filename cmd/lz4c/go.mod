module github.com/pierrec/lz4/v4/cmd/lz4c

go 1.26.0

require (
	code.cloudfoundry.org/bytefmt v0.92.0
	github.com/pierrec/cmdflag v0.0.2
	github.com/pierrec/lz4/v4 v4.1.31
	github.com/schollz/progressbar/v3 v3.19.1
)

require (
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.46.0 // indirect
)

//replace github.com/pierrec/lz4/v4 => ../..
