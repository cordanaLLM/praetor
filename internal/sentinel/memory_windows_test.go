//go:build windows

package sentinel

import (
	"errors"
	"testing"
	"unsafe"
)

func TestReadHostMemory_Positive_WindowsReportsPhysicalMemory(t *testing.T) {
	if size := unsafe.Sizeof(memoryStatusEx{}); size != 64 {
		t.Fatalf("memoryStatusEx is %d bytes; MEMORYSTATUSEX is 64", size)
	}
	total, free, err := readHostMemory()
	if err != nil {
		t.Fatalf("reading physical memory failed: %v", err)
	}
	if total == 0 || free == 0 || free > total {
		t.Fatalf("implausible reading: total=%d free=%d", total, free)
	}
}

func withMemoryStatus(t *testing.T, fake func() (memoryStatusEx, error)) {
	t.Helper()
	original := globalMemoryStatusFunc
	t.Cleanup(func() { globalMemoryStatusFunc = original })
	globalMemoryStatusFunc = fake
}

func TestReadHostMemory_Negative_WindowsCallFailureIsAnError(t *testing.T) {
	failure := errors.New("access denied")
	withMemoryStatus(t, func() (memoryStatusEx, error) { return memoryStatusEx{}, failure })
	total, free, err := readHostMemory()
	if !errors.Is(err, failure) || total != 0 || free != 0 {
		t.Fatalf("a failed call must surface, not read as unavailable: total=%d free=%d err=%v", total, free, err)
	}
	if _, err := ReadHostStats("."); !errors.Is(err, failure) {
		t.Fatalf("the failure was lost on the way to the host stats: %v", err)
	}
}

func TestReadHostMemory_Boundary_WindowsImplausibleReadingsAreRefused(t *testing.T) {
	for name, status := range map[string]memoryStatusEx{
		"zero total":             {},
		"available above total":  {totalPhys: 8 << 30, availPhys: 8<<30 + 1},
		"available equals total": {totalPhys: 8 << 30, availPhys: 8 << 30},
	} {
		withMemoryStatus(t, func() (memoryStatusEx, error) { return status, nil })
		total, free, err := readHostMemory()
		if name == "available equals total" {
			if err != nil || total != status.totalPhys || free != status.availPhys {
				t.Fatalf("%s: a fully free host was refused: total=%d free=%d err=%v", name, total, free, err)
			}
			continue
		}
		if err == nil || total != 0 || free != 0 {
			t.Fatalf("%s: accepted: total=%d free=%d err=%v", name, total, free, err)
		}
	}
}
