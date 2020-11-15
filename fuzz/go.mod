module github.com/pierrec/lz4/v4/fuzz

go 1.14

require (
	github.com/dvyukov/go-fuzz v0.0.0-20201115201419-0701ec3cea76
	github.com/elazarl/go-bindata-assetfs v1.0.1 // indirect
	github.com/pierrec/lz4/v4 v4.0.0-00010101000000-000000000000
	github.com/stephens2424/writerset v1.0.2 // indirect
	golang.org/x/tools v0.0.0-20201116002733-ac45abd4c88c // indirect
)

replace github.com/pierrec/lz4/v4 => ../
