module github.com/pierrec/lz4/v4

go 1.22

retract (
	// Writer.ReadFrom returns io.EOF and leaves the frame unfinished on empty or whole-block input: https://github.com/pierrec/lz4/pull/245
	v4.1.26
	// Reader.WriteTo silently drops data after a partial Read: https://github.com/pierrec/lz4/pull/251
	[v4.1.23, v4.1.29]
)
