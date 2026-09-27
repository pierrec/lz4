//go:build linux || darwin
// +build linux darwin

package lz4block

import (
	"syscall"
	"testing"
)

// guardedTail returns an n-byte slice that ends at a page boundary followed
// by an inaccessible page, so that any read past its end faults.
func guardedTail(t testing.TB, n int) []byte {
	page := syscall.Getpagesize()
	size := (n+page-1)/page*page + page
	mem, err := syscall.Mmap(-1, 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Munmap(mem) })
	if err := syscall.Mprotect(mem[size-page:], syscall.PROT_NONE); err != nil {
		t.Fatal(err)
	}
	return mem[size-page-n : size-page : size-page]
}
