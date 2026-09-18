package sentinel

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Invariant configuration constants for host resource protection.
const (
	MinReservedRAMBytes  uint64  = 4 * 1024 * 1024 * 1024  // 4 GB
	RAMReserveRatio      float64 = 0.20                    // 20% of total RAM
	MinReservedDiskBytes uint64  = 10 * 1024 * 1024 * 1024 // 10 GB
	DiskReserveRatio     float64 = 0.15                    // 15% of total disk
	RAMPressureThreshold float64 = 0.85                    // 85% RAM utilization
	MaxScanLines         int     = 1000                    // Upper bound for bounded loops
)

// Package-level configurable paths and hooks for testability and cross-platform execution.
var (
	defaultMeminfoPath = "/proc/meminfo"
	loadavgPath        = "/proc/loadavg"
)

// HostStats holds measured system metrics for memory, disk, and CPU load.
type HostStats struct {
	RAMTotalBytes  uint64  `json:"ram_total_bytes"`
	RAMFreeBytes   uint64  `json:"ram_free_bytes"`
	DiskTotalBytes uint64  `json:"disk_total_bytes"`
	DiskFreeBytes  uint64  `json:"disk_free_bytes"`
	CPULoad1Min    float64 `json:"cpu_load_1min"`
	CPULoad5Min    float64 `json:"cpu_load_5min"`
	CPULoad15Min   float64 `json:"cpu_load_15min"`
	// CPULoadMeasured is false where the platform has no load average this package can read. The
	// three loads are then zero, which an idle Linux host can also report, so zero alone cannot
	// say whether anything was measured.
	CPULoadMeasured bool `json:"cpu_load_measured"`
}

// HostReport summarizes health evaluations, invariant checks, and throttling directives.
type HostReport struct {
	Healthy                   bool      `json:"healthy"`
	Stats                     HostStats `json:"stats"`
	RAMPressure               bool      `json:"ram_pressure"`
	RAMUtilizationPercent     float64   `json:"ram_utilization_percent"`
	DiskUtilizationPercent    float64   `json:"disk_utilization_percent"`
	ViolatedInvariants        []string  `json:"violated_invariants"`
	ThrottlingRecommendations []string  `json:"throttling_recommendations"`
}

// CheckHostHealth inspects host memory, disk capacity, and CPU load, evaluating health invariants.
func CheckHostHealth(path string) (*HostReport, error) {
	if path == "" {
		path = "."
	}

	stats, err := ReadHostStats(path)
	if err != nil {
		return nil, fmt.Errorf("sentinel: failed to read host stats: %w", err)
	}

	report := EvaluateHostStats(stats)
	return report, nil
}

// ReadHostStats gathers current hardware metrics across memory, disk, and CPU subsystems.
func ReadHostStats(path string) (HostStats, error) {
	var stats HostStats

	ramTotal, ramFree, err := readHostMemory()
	if err != nil {
		return stats, fmt.Errorf("read memory stats: %w", err)
	}
	stats.RAMTotalBytes = ramTotal
	stats.RAMFreeBytes = ramFree

	diskTotal, diskFree, err := readHostDisk(path)
	if err != nil {
		return stats, fmt.Errorf("read disk stats: %w", err)
	}
	stats.DiskTotalBytes = diskTotal
	stats.DiskFreeBytes = diskFree

	l1, l5, l15, measured, err := readHostCPULoad()
	if err != nil {
		return stats, fmt.Errorf("read cpu load stats: %w", err)
	}
	stats.CPULoadMeasured = measured
	stats.CPULoad1Min = l1
	stats.CPULoad5Min = l5
	stats.CPULoad15Min = l15

	return stats, nil
}

// CalculateRAMReservation computes required free RAM: max(4 GB, 0.20 * RAM_total).
func CalculateRAMReservation(total uint64) uint64 {
	ratioBytes := uint64(float64(total) * RAMReserveRatio)
	if ratioBytes > MinReservedRAMBytes {
		return ratioBytes
	}
	return MinReservedRAMBytes
}

// CalculateDiskReservation computes required free disk: max(10 GB, 0.15 * Disk_total).
func CalculateDiskReservation(total uint64) uint64 {
	ratioBytes := uint64(float64(total) * DiskReserveRatio)
	if ratioBytes > MinReservedDiskBytes {
		return ratioBytes
	}
	return MinReservedDiskBytes
}

// EvaluateHostStats verifies host reservation invariants and checks memory pressure.
func EvaluateHostStats(stats HostStats) *HostReport {
	report := &HostReport{
		Healthy:                   true,
		Stats:                     stats,
		ViolatedInvariants:        make([]string, 0),
		ThrottlingRecommendations: make([]string, 0),
	}

	checkRAMInvariants(stats, report)
	checkDiskInvariants(stats, report)

	if len(report.ViolatedInvariants) > 0 || report.RAMPressure {
		report.Healthy = false
	}
	return report
}

// checkRAMInvariants verifies RAM reservation and detects memory pressure above 85%.
func checkRAMInvariants(stats HostStats, report *HostReport) {
	// An unmeasured host yields no RAM verdict. Deciding pressure from zero bytes would read as
	// a machine under no load at all, which is the opposite of the truth and worse than silence.
	if stats.RAMTotalBytes == 0 {
		report.ViolatedInvariants = append(report.ViolatedInvariants,
			"RAM unavailable: no readable source on this platform; memory checks skipped")
		return
	}

	requiredRAM := CalculateRAMReservation(stats.RAMTotalBytes)
	if stats.RAMFreeBytes < requiredRAM {
		msg := fmt.Sprintf("RAM reservation invariant violated: free %d bytes < required %d bytes",
			stats.RAMFreeBytes, requiredRAM)
		report.ViolatedInvariants = append(report.ViolatedInvariants, msg)
	}

	var ramUsed uint64
	if stats.RAMTotalBytes >= stats.RAMFreeBytes {
		ramUsed = stats.RAMTotalBytes - stats.RAMFreeBytes
	}
	utilRatio := float64(ramUsed) / float64(stats.RAMTotalBytes)
	report.RAMUtilizationPercent = utilRatio * 100.0

	if utilRatio > RAMPressureThreshold {
		report.RAMPressure = true
		rec := "Memory pressure exceeds 85%: throttle background tasks and defer model loads to protect IDE and browser processes"
		report.ThrottlingRecommendations = append(report.ThrottlingRecommendations, rec)
	}
}

// checkDiskInvariants verifies disk reservation invariant.
func checkDiskInvariants(stats HostStats, report *HostReport) {
	if stats.DiskTotalBytes == 0 {
		report.ViolatedInvariants = append(report.ViolatedInvariants, "Disk total cannot be zero")
		return
	}

	requiredDisk := CalculateDiskReservation(stats.DiskTotalBytes)
	if stats.DiskFreeBytes < requiredDisk {
		msg := fmt.Sprintf("Disk reservation invariant violated: free %d bytes < required %d bytes",
			stats.DiskFreeBytes, requiredDisk)
		report.ViolatedInvariants = append(report.ViolatedInvariants, msg)
	}

	var diskUsed uint64
	if stats.DiskTotalBytes >= stats.DiskFreeBytes {
		diskUsed = stats.DiskTotalBytes - stats.DiskFreeBytes
	}
	report.DiskUtilizationPercent = (float64(diskUsed) / float64(stats.DiskTotalBytes)) * 100.0
}

// CanAllocateModel evaluates whether a model requiring vramRequiredGB can safely be loaded
// without violating the host reservation invariant or driving memory utilization past 85%.
func CanAllocateModel(stats *HostStats, vramRequiredGB float64) bool {
	// A zero total means memory was never measured, and admission is refused rather than decided
	// against unknown headroom. This guard already existed; it is what makes the unmeasured case
	// safe without a second check.
	if stats == nil || vramRequiredGB <= 0 || stats.RAMTotalBytes == 0 {
		return false
	}
	if vramRequiredGB > 100000 {
		return false
	}
	reqBytes := uint64(vramRequiredGB * 1024 * 1024 * 1024)
	if reqBytes > stats.RAMFreeBytes {
		return false
	}

	remFree := stats.RAMFreeBytes - reqBytes
	resBytes := CalculateRAMReservation(stats.RAMTotalBytes)
	if remFree < resBytes {
		return false
	}

	newUsed := stats.RAMTotalBytes - remFree
	newUtil := float64(newUsed) / float64(stats.RAMTotalBytes)
	return newUtil <= RAMPressureThreshold
}

// meminfoPath is a variable so the absent-source path can be exercised on a host that does have
// /proc/meminfo. Without this the regression is only reachable on macOS or Windows, which is
// precisely where nobody runs the tests -- and a test that cannot run where the bug was introduced
// does not defend against it.
var meminfoPath = defaultMeminfoPath

// readMeminfoMemory reports host memory from meminfoPath, and whether it could be read at all.
// It is the source readHostMemory uses on every platform except Windows, which has no
// /proc/meminfo and is read through its own API instead.
//
// It previously returned a hardcoded 16 GB total and 8 GB free wherever /proc/meminfo was absent --
// every macOS and Windows host -- with a nil error, so no caller could tell the numbers were
// invented. The "free" figure was worse than a constant: it was derived from runtime.MemStats,
// which measures this Go process's heap rather than the machine. RAM pressure, the reserve ratio
// and model admission were all decided from that.
//
// There is no estimate now. Where the platform exposes no source this repository can read, the
// figures are zero -- a total no real machine reports -- and every decision that would have used
// them is skipped and reported as unavailable.
func readMeminfoMemory() (total uint64, free uint64, resultErr error) {
	f, err := os.Open(meminfoPath)
	if err != nil {
		// Unreadable, not zero-sized. Callers distinguish the two by the total being zero,
		// which no real machine reports.
		return 0, 0, nil
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()

	return parseMeminfo(f)
}

// parseMeminfo parses MemTotal, MemAvailable, MemFree, Buffers, and Cached fields from meminfo.
func parseMeminfo(r io.Reader) (uint64, uint64, error) {
	scanner := bufio.NewScanner(r)
	values := make(map[string]uint64)
	for i := 0; i < MaxScanLines && scanner.Scan(); i++ {
		key, value, ok := memoryField(scanner.Text())
		if ok {
			values[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scanner error in meminfo: %w", err)
	}
	total, found := values["MemTotal"]
	if !found {
		return 0, 0, fmt.Errorf("meminfo missing MemTotal field")
	}
	free := values["MemAvailable"]
	if free == 0 {
		free = values["MemFree"] + values["Buffers"] + values["Cached"]
	}
	return total, free, nil
}

func memoryField(line string) (string, uint64, bool) {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", 0, false
	}
	key := strings.TrimSuffix(parts[0], ":")
	switch key {
	case "MemTotal", "MemFree", "MemAvailable", "Buffers", "Cached":
	default:
		return "", 0, false
	}
	value, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || value > ^uint64(0)/1024 {
		return "", 0, false
	}
	return key, value * 1024, true
}

// readHostCPULoad inspects /proc/loadavg. Where it cannot be opened -- macOS and Windows have no
// procfs -- the loads are zero and measured is false, so a caller can say the load is unknown
// instead of presenting zero as an idle machine.
func readHostCPULoad() (load1, load5, load15 float64, measured bool, resultErr error) {
	f, err := os.Open(loadavgPath)
	if err != nil {
		return 0, 0, 0, false, nil
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()

	load1, load5, load15, resultErr = parseLoadavg(f)
	return load1, load5, load15, resultErr == nil, resultErr
}

// parseLoadavg extracts 1, 5, and 15-minute load averages from reader.
func parseLoadavg(r io.Reader) (float64, float64, float64, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return 0, 0, 0, fmt.Errorf("loadavg scan error: %w", err)
		}
		return 0, 0, 0, fmt.Errorf("loadavg is empty")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("invalid loadavg format")
	}

	l1, err1 := strconv.ParseFloat(fields[0], 64)
	l5, err2 := strconv.ParseFloat(fields[1], 64)
	l15, err3 := strconv.ParseFloat(fields[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, fmt.Errorf("failed to parse loadavg floats")
	}
	return l1, l5, l15, nil
}
