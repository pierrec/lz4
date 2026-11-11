package main

import (
	"fmt"
	"os"
)

func openOutputFile(name string, input os.FileInfo) (*os.File, error) {
	// Check the opened file before truncating so aliases cannot erase the input.
	file, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY, input.Mode())
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if os.SameFile(input, info) {
		file.Close()
		return nil, fmt.Errorf("%s: input and output are the same file", name)
	}
	if info.Mode().IsRegular() && info.Size() > 0 {
		if err := file.Truncate(0); err != nil {
			file.Close()
			return nil, err
		}
	}
	return file, nil
}
