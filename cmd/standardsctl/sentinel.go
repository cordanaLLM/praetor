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

	if err := fs.Parse(args); err != nil {
		return err
	}

	report, err := sentinel.CheckHostHealth(*path)
	if err != nil {
		return fmt.Errorf("failed inspecting host sentinel: %w", err)
	}

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
	} else {
		fmt.Println("\nStatus: [PRESSURE / BREACH] Host reservation invariants violated:")
		for _, v := range report.ViolatedInvariants {
			fmt.Printf("  - %s\n", v)
		}
		for _, rec := range report.ThrottlingRecommendations {
			fmt.Printf("Advice: %s\n", rec)
		}
	}

	if *checkVRAM > 0 {
		canAlloc := sentinel.CanAllocateModel(&report.Stats, *checkVRAM)
		if canAlloc {
			fmt.Printf("\nModel Allocation (%.1f GB): [APPROVED] Headroom sufficient.\n", *checkVRAM)
		} else {
			fmt.Printf("\nModel Allocation (%.1f GB): [DENIED] Would breach workstation reservation threshold.\n", *checkVRAM)
		}
	}

	return nil
}
