module github.com/pierrec/lz4/v4

go 1.22

// Reader.WriteTo, and so io.Copy, can silently drop data after a partial Read.
retract [v4.1.23, v4.1.29]
