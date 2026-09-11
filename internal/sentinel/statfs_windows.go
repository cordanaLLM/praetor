//go:build windows

package sentinel

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
)

var getDiskFreeSpaceFunc = func(directoryName string) (uint64, uint64, error) {
	if directoryName == "" {
		directoryName = "."
	}
	dirPtr, err := syscall.UTF16PtrFromString(directoryName)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid path %q: %w", directoryName, err)
	}
	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes uint64

	// SAFETY: Pointers are passed to valid local uint64 variables for kernel32 to populate.
	r1, _, errSys := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(dirPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if r1 == 0 {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceEx failed for %q: %w", directoryName, errSys)
	}
	return totalNumberOfBytes, freeBytesAvailable, nil
}

func readHostDisk(path string) (uint64, uint64, error) {
	return getDiskFreeSpaceFunc(path)
}
