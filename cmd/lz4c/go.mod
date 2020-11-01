module github.com/pierrec/lz4/v4/cmd/lz4c

go 1.14

require (
	code.cloudfoundry.org/bytefmt v0.0.0-20200131002437-cf55d5288a48
	github.com/onsi/ginkgo v1.14.2 // indirect
	github.com/onsi/gomega v1.10.3 // indirect
	github.com/pierrec/cmdflag v0.0.2
	github.com/pierrec/lz4/v4 v4.1.0
	github.com/schollz/progressbar/v3 v3.5.1
)

//replace github.com/pierrec/lz4/v4 => ../..
