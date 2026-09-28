package main

import (
	"flag"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/sentinel"
)

func runSentinel(args []string) error {
	fs := flag.NewFlagSet("sentinel", flag.ContinueOnError)
	path := fs.String("path", ".", "Path to inspect for disk storage")
	checkVRAM := fs.Float64("check-vram", 0, "Check if given VRAM (GB) can be allocated safely")
	minFreeMB := fs.Uint64("min-free-mb", 0, "Fail unless measured free RAM is at least this many MiB (0 skips the check)")

	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *minFreeMB > sentinel.MaxHeadroomMB {
		return fmt.Errorf("--min-free-mb %d exceeds the %d MiB limit", *minFreeMB, sentinel.MaxHeadroomMB)
	}

	report, err := sentinel.CheckHostHealth(*path)
	if err != nil {
		return fmt.Errorf("failed inspecting host sentinel: %w", err)
	}

	printHostReport(report)
	reportModelAllocation(&report.Stats, *checkVRAM)
	if *minFreeMB == 0 {
		return nil
	}
	return reportHeadroom(&report.Stats, *minFreeMB)
}

// printHostReport prints the measured memory, disk and CPU load and the reservation verdict.
func printHostReport(report *sentinel.HostReport) {
	fmt.Println("=== Workstation Resource Sentinel ===")
	fmt.Printf("Memory: %.1f GB free / %.1f GB total (%.1f%% used)\n",
		float64(report.Stats.RAMFreeBytes)/(1024*1024*1024),
		float64(report.Stats.RAMTotalBytes)/(1024*1024*1024),
		report.RAMUtilizationPercent)
	fmt.Printf("Disk:   %.1f GB free / %.1f GB total (%.1f%% used)\n",
		float64(report.Stats.DiskFreeBytes)/(1024*1024*1024),
		float64(report.Stats.DiskTotalBytes)/(1024*1024*1024),
		report.DiskUtilizationPercent)
	if report.Stats.CPULoadMeasured {
		fmt.Printf("CPU:    load %.2f (1m), %.2f (5m), %.2f (15m)\n",
			report.Stats.CPULoad1Min, report.Stats.CPULoad5Min, report.Stats.CPULoad15Min)
	} else {
		fmt.Println("CPU:    load unavailable (no load average source on this platform)")
	}

	if report.Healthy {
		fmt.Println("\nStatus: [HEALTHY] Host reservation invariants satisfied (RAM >= 20%, Disk >= 15%).")
		return
	}
	fmt.Println("\nStatus: [PRESSURE / BREACH] Host reservation invariants violated:")
	for _, v := range report.ViolatedInvariants {
		fmt.Printf("  - %s\n", v)
	}
	for _, rec := range report.ThrottlingRecommendations {
		fmt.Printf("Advice: %s\n", rec)
	}
}

// reportModelAllocation prints whether a model of vramGB fits the reservation; zero or less skips
// the check, as the --check-vram default does.
func reportModelAllocation(stats *sentinel.HostStats, vramGB float64) {
	if vramGB <= 0 {
		return
	}
	if sentinel.CanAllocateModel(stats, vramGB) {
		fmt.Printf("\nModel Allocation (%.1f GB): [APPROVED] Headroom sufficient.\n", vramGB)
		return
	}
	fmt.Printf("\nModel Allocation (%.1f GB): [DENIED] Would breach workstation reservation threshold.\n", vramGB)
}

// reportHeadroom prints the free-RAM headroom verdict and fails the command unless measured free
// RAM covers headroomMB. The VS Code extension's Check Sentinel Host Headroom command passes its
// standards.sentinel.headroomMB setting here, so the measurement stays in this one implementation.
func reportHeadroom(stats *sentinel.HostStats, headroomMB uint64) error {
	if stats.RAMTotalBytes == 0 || stats.RAMFreeBytes > stats.RAMTotalBytes {
		fmt.Printf("\nHeadroom (%d MiB): [UNMEASURED] No consistent memory reading on this host.\n", headroomMB)
		return fmt.Errorf("headroom of %d MiB unverified: host memory has no consistent reading", headroomMB)
	}
	freeMB := stats.RAMFreeBytes / (1024 * 1024)
	if !sentinel.MeetsHeadroom(stats, headroomMB) {
		fmt.Printf("\nHeadroom (%d MiB): [BELOW] %d MiB free.\n", headroomMB, freeMB)
		return fmt.Errorf("free RAM %d MiB is below the required headroom of %d MiB", freeMB, headroomMB)
	}
	fmt.Printf("\nHeadroom (%d MiB): [MET] %d MiB free.\n", headroomMB, freeMB)
	return nil
}
