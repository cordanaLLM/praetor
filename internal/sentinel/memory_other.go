//go:build !windows

package sentinel

// readHostMemory reads /proc/meminfo. Where it is absent -- macOS, or a Linux without procfs --
// both figures are zero and the evaluation reports memory as unavailable.
func readHostMemory() (uint64, uint64, error) {
	return readMeminfoMemory()
}

// readHostSwap reads SwapTotal and SwapFree from /proc/meminfo; where it is absent (macOS) swap
// is reported as unmeasured.
func readHostSwap() (uint64, uint64, bool, error) {
	return readMeminfoSwap()
}
