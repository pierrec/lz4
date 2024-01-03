module github.com/pierrec/lz4/v4/cmd/lz4c

go 1.14

require (
	code.cloudfoundry.org/bytefmt v0.0.0-20231017140541-3b893ed0421b
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/pierrec/cmdflag v0.0.2
	github.com/pierrec/lz4/v4 v4.1.19
	github.com/rivo/uniseg v0.4.4 // indirect
	github.com/schollz/progressbar/v3 v3.14.1
	golang.org/x/term v0.15.0 // indirect
)

//replace github.com/pierrec/lz4/v4 => ../..
