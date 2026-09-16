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

	// Each conversion stays inside the call expression on purpose. Hoisting
	// uintptr(unsafe.Pointer(x)) into a local first breaks the unsafe.Pointer rules: the
	// uintptr would stop keeping the object reachable, and the collector may move or free it
	// before the syscall reads it. The exclusions below sit per line because gosec reports G103
	// per line, and they stay at these four reviewed lines rather than widening the rule to
	// *_windows.go, which would silence it for syscall wrappers nobody has read (#106).
	r1, _, errSys := procGetDiskFreeSpaceExW.Call(
		// SAFETY: dirPtr addresses a NUL-terminated UTF-16 buffer allocated in this frame by
		// UTF16PtrFromString above; kernel32 only reads it, and the call cannot outlive it.
		uintptr(unsafe.Pointer(dirPtr)), //#nosec G103 -- Win32 in-parameter, live local
		// SAFETY: freeBytesAvailable is a live local in this frame; kernel32 writes one uint64
		// through this out-parameter and never retains the address.
		uintptr(unsafe.Pointer(&freeBytesAvailable)), //#nosec G103 -- Win32 out-parameter, live local
		// SAFETY: totalNumberOfBytes is a live local in this frame; kernel32 writes one uint64
		// through this out-parameter and never retains the address.
		uintptr(unsafe.Pointer(&totalNumberOfBytes)), //#nosec G103 -- Win32 out-parameter, live local
		// SAFETY: totalNumberOfFreeBytes is a live local in this frame; kernel32 writes one
		// uint64 through this out-parameter and never retains the address.
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)), //#nosec G103 -- Win32 out-parameter, live local
	)
	if r1 == 0 {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceEx failed for %q: %w", directoryName, errSys)
	}
	return totalNumberOfBytes, freeBytesAvailable, nil
}

func readHostDisk(path string) (uint64, uint64, error) {
	return getDiskFreeSpaceFunc(path)
}
