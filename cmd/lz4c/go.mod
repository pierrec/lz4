module github.com/pierrec/lz4/cmd/lz4c

go 1.22

require (
	code.cloudfoundry.org/bytefmt v0.0.0-20231017140541-3b893ed0421b
	github.com/pierrec/cmdflag v0.0.2
	github.com/pierrec/lz4/v4 v4.1.32
	github.com/schollz/progressbar/v3 v3.14.1
)

require (
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db // indirect
	github.com/rivo/uniseg v0.4.4 // indirect
	golang.org/x/sys v0.15.0 // indirect
	golang.org/x/term v0.15.0 // indirect
)

//replace github.com/pierrec/lz4/v4 => ../..
