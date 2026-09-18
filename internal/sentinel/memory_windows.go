//go:build windows

package sentinel

import (
	"errors"
	"fmt"
	"unsafe"
)

var procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX structure field for field. Its size is 64
// bytes on every Windows architecture Go supports: two DWORDs followed by seven DWORDLONGs, with
// no padding.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// globalMemoryStatusFunc is a variable so the failure paths can be exercised on a host whose call
// succeeds.
var globalMemoryStatusFunc = func() (memoryStatusEx, error) {
	status := memoryStatusEx{}
	// SAFETY: unsafe.Sizeof is evaluated at compile time and dereferences nothing. It is the
	// structure's layout size, which the API requires in dwLength before it writes the rest.
	status.length = uint32(unsafe.Sizeof(status))
	// The conversion stays inside the call expression: hoisting uintptr(unsafe.Pointer(x)) into a
	// local would stop it keeping status reachable (see statfs_windows.go and #106).
	r1, _, errSys := procGlobalMemoryStatusEx.Call(
		// SAFETY: status is a live local in this frame with length set to its own size, as the
		// API requires; kernel32 writes the structure in place and never retains the address.
		uintptr(unsafe.Pointer(&status)), //#nosec G103 -- Win32 in/out-parameter, live local
	)
	if r1 == 0 {
		return memoryStatusEx{}, fmt.Errorf("GlobalMemoryStatusEx failed: %w", errSys)
	}
	return status, nil
}

// readHostMemory reads physical memory through GlobalMemoryStatusEx.
//
// Windows has no /proc/meminfo, so this host always reported both figures as zero and every
// memory decision as unavailable, although the platform exposes exactly the two numbers the
// evaluation needs. ullAvailPhys counts free, zeroed and standby pages -- memory the system can
// hand out without paging anything -- which is what MemAvailable measures on Linux.
//
// A failed call is an error, as an unparsable /proc/meminfo is: the source exists and did not
// answer. A call that succeeds with no physical memory is refused as a broken reading rather than
// passed on as zero, which would read as "no source on this platform".
func readHostMemory() (uint64, uint64, error) {
	status, err := globalMemoryStatusFunc()
	if err != nil {
		return 0, 0, err
	}
	if status.totalPhys == 0 {
		return 0, 0, errors.New("GlobalMemoryStatusEx reported no physical memory")
	}
	if status.availPhys > status.totalPhys {
		return 0, 0, fmt.Errorf("GlobalMemoryStatusEx reported %d bytes available of %d total",
			status.availPhys, status.totalPhys)
	}
	return status.totalPhys, status.availPhys, nil
}
