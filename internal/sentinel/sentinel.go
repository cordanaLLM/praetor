package sentinel

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Invariant configuration constants for host resource protection.
const (
	MinReservedRAMBytes  uint64  = 4 * 1024 * 1024 * 1024  // 4 GB
	RAMReserveRatio      float64 = 0.20                   // 20% of total RAM
	MinReservedDiskBytes uint64  = 10 * 1024 * 1024 * 1024 // 10 GB
	DiskReserveRatio     float64 = 0.15                   // 15% of total disk
	RAMPressureThreshold float64 = 0.85                   // 85% RAM utilization
	MaxScanLines         int     = 1000                   // Upper bound for bounded loops
)

// Package-level configurable paths and hooks for testability and cross-platform execution.
var (
	meminfoPath = "/proc/meminfo"
	loadavgPath = "/proc/loadavg"
	statfsFunc  = syscall.Statfs
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

	l1, l5, l15, err := readHostCPULoad()
	if err != nil {
		return stats, fmt.Errorf("read cpu load stats: %w", err)
	}
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
	if stats.RAMTotalBytes == 0 {
		report.ViolatedInvariants = append(report.ViolatedInvariants, "RAM total cannot be zero")
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
	if newUtil > RAMPressureThreshold {
		return false
	}

	return true
}

// readHostMemory inspects /proc/meminfo or utilizes fallback logic.
func readHostMemory() (uint64, uint64, error) {
	f, err := os.Open(meminfoPath)
	if err != nil {
		return readMemoryFallback()
	}
	defer f.Close()

	return parseMeminfo(f)
}

// readMemoryFallback provides cross-platform heuristic memory stats when /proc is unavailable.
func readMemoryFallback() (uint64, uint64, error) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	const fallbackTotal = uint64(16 * 1024 * 1024 * 1024) // 16 GB baseline
	fallbackFree := uint64(8 * 1024 * 1024 * 1024)        // 8 GB baseline
	if ms.Sys > 0 && ms.Sys < fallbackTotal {
		fallbackFree = fallbackTotal - ms.Sys
	}
	return fallbackTotal, fallbackFree, nil
}

// parseMeminfo parses MemTotal, MemAvailable, MemFree, Buffers, and Cached fields from meminfo.
func parseMeminfo(r io.Reader) (uint64, uint64, error) {
	scanner := bufio.NewScanner(r)
	var memTotal, memFree, memAvailable, buffers, cached uint64
	var foundTotal bool

	for i := 0; i < MaxScanLines && scanner.Scan(); i++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		val, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			continue
		}
		valBytes := val * 1024
		switch key {
		case "MemTotal":
			memTotal = valBytes
			foundTotal = true
		case "MemFree":
			memFree = valBytes
		case "MemAvailable":
			memAvailable = valBytes
		case "Buffers":
			buffers = valBytes
		case "Cached":
			cached = valBytes
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scanner error in meminfo: %w", err)
	}
	if !foundTotal {
		return 0, 0, fmt.Errorf("meminfo missing MemTotal field")
	}

	freeBytes := memAvailable
	if freeBytes == 0 {
		freeBytes = memFree + buffers + cached
	}
	return memTotal, freeBytes, nil
}

// readHostDisk inspects disk metrics via statfsFunc.
func readHostDisk(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := statfsFunc(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs failed for path %q: %w", path, err)
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	return total, free, nil
}

// readHostCPULoad inspects /proc/loadavg or returns safe fallback zero metrics.
func readHostCPULoad() (float64, float64, float64, error) {
	f, err := os.Open(loadavgPath)
	if err != nil {
		return 0.0, 0.0, 0.0, nil
	}
	defer f.Close()

	return parseLoadavg(f)
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
