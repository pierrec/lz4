package lz4block

import (
	"reflect"
	"syscall"
	"testing"
	"unsafe"
)

// lowBuffer returns n bytes below the maximum LZ4 match offset.
func lowBuffer(t testing.TB, n int) []byte {
	t.Helper()
	page := syscall.Getpagesize()
	// Never replace an existing mapping. Older kernels may ignore this flag
	// and return a different address, so check the result before using it.
	const mapFixedNoReplace = 0x100000
	for hint := uintptr(page); hint < 1<<16; hint += uintptr(page) {
		addr, _, errno := syscall.Syscall6(syscall.SYS_MMAP, hint, uintptr(page),
			syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE|mapFixedNoReplace, ^uintptr(0), 0)
		if errno != 0 {
			continue
		}
		unmap := func() { _, _, _ = syscall.Syscall(syscall.SYS_MUNMAP, addr, uintptr(page), 0) }
		if addr < uintptr(page) || addr >= winMask {
			unmap()
			continue
		}
		t.Cleanup(unmap)
		var mem []byte
		header := (*reflect.SliceHeader)(unsafe.Pointer(&mem))
		header.Data = addr
		header.Len = page
		header.Cap = page
		return mem[:n:n]
	}
	t.Skip("cannot map a destination below 64 KiB")
	return nil
}
