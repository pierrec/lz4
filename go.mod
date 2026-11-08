module github.com/pierrec/lz4/v4

go 1.22

// io.Copy into a Writer returns io.EOF if the source is a whole number of blocks.
retract v4.1.26
