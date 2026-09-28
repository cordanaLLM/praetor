package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/sentinel"
)

const testMiB = uint64(1024 * 1024)

// TestReportHeadroom_Positive: measured free RAM that covers the headroom passes.
func TestReportHeadroom_Positive(t *testing.T) {
	stats := sentinel.HostStats{RAMTotalBytes: 8192 * testMiB, RAMFreeBytes: 2048 * testMiB}
	if err := reportHeadroom(&stats, 2048); err != nil {
		t.Fatalf("free RAM equal to the headroom failed: %v", err)
	}
}

// TestReportHeadroom_Negative: too little free RAM, and a host without a consistent reading,
// fail the command instead of reporting a headroom nobody measured.
func TestReportHeadroom_Negative(t *testing.T) {
	below := sentinel.HostStats{RAMTotalBytes: 8192 * testMiB, RAMFreeBytes: 2048*testMiB - 1}
	if err := reportHeadroom(&below, 2048); err == nil || !strings.Contains(err.Error(), "below the required headroom") {
		t.Fatalf("free RAM under the headroom passed: %v", err)
	}
	for _, stats := range []sentinel.HostStats{{}, {RAMTotalBytes: testMiB, RAMFreeBytes: 2 * testMiB}} {
		if err := reportHeadroom(&stats, 1); err == nil || !strings.Contains(err.Error(), "unverified") {
			t.Fatalf("reading %+v was not reported unverified: %v", stats, err)
		}
	}
}

// TestRunSentinel_Boundary_MinFreeFlag: one past the limit is refused before measuring, the limit
// itself parses and fails only on the measurement, a negative value does not parse, zero skips the
// check, and 1 MiB passes on a host that has a memory source.
func TestRunSentinel_Boundary_MinFreeFlag(t *testing.T) {
	limit := sentinel.MaxHeadroomMB
	if err := runSentinel([]string{"--min-free-mb=" + strconv.FormatUint(limit+1, 10)}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("a headroom past the limit was accepted: %v", err)
	}
	if err := runSentinel([]string{"--min-free-mb=-1"}); err == nil {
		t.Fatal("a negative headroom parsed")
	}
	if err := runSentinel([]string{"--min-free-mb=" + strconv.FormatUint(limit, 10)}); err == nil || strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("the limit was refused as out of range, or 1 PiB of free RAM was reported: %v", err)
	}
	if err := runSentinel([]string{"--min-free-mb=0"}); err != nil {
		t.Fatalf("a zero headroom did not skip the check: %v", err)
	}
	stats, err := sentinel.ReadHostStats(".")
	if err != nil {
		t.Fatalf("read host stats: %v", err)
	}
	if stats.RAMTotalBytes == 0 {
		t.Skip("no memory source on this platform (macOS); TestReportHeadroom_Negative covers the unverified path")
	}
	if err := runSentinel([]string{"--min-free-mb=1"}); err != nil {
		t.Fatalf("a 1 MiB headroom failed on a measured host: %v", err)
	}
}
