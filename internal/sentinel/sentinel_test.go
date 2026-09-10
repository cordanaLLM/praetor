package sentinel

import (
	"strings"
	"syscall"
	"testing"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestCheckHostHealth_Positive(t *testing.T) {
	report, err := CheckHostHealth(".")
	if err != nil {
		t.Fatalf("CheckHostHealth failed on valid path: %v", err)
	}
	if report == nil {
		t.Fatalf("expected non-nil report")
	}
	if report.Stats.RAMTotalBytes == 0 {
		t.Errorf("expected RAMTotalBytes > 0, got %d", report.Stats.RAMTotalBytes)
	}
	if report.Stats.DiskTotalBytes == 0 {
		t.Errorf("expected DiskTotalBytes > 0, got %d", report.Stats.DiskTotalBytes)
	}
}

func TestEvaluateHostStats_Positive(t *testing.T) {
	// Generous resources: 64 GB total RAM, 40 GB free; 500 GB disk, 300 GB free
	stats := HostStats{
		RAMTotalBytes:  64 * 1024 * 1024 * 1024,
		RAMFreeBytes:   40 * 1024 * 1024 * 1024,
		DiskTotalBytes: 500 * 1024 * 1024 * 1024,
		DiskFreeBytes:  300 * 1024 * 1024 * 1024,
		CPULoad1Min:    1.2,
		CPULoad5Min:    1.1,
		CPULoad15Min:   0.9,
	}

	report := EvaluateHostStats(stats)
	if !report.Healthy {
		t.Errorf("expected report to be healthy, got false. Violated: %v", report.ViolatedInvariants)
	}
	if report.RAMPressure {
		t.Errorf("expected no RAM pressure under generous memory, got true")
	}
	if len(report.ViolatedInvariants) != 0 {
		t.Errorf("expected zero violated invariants, got %d: %v", len(report.ViolatedInvariants), report.ViolatedInvariants)
	}
	if len(report.ThrottlingRecommendations) != 0 {
		t.Errorf("expected zero throttling recommendations, got %d", len(report.ThrottlingRecommendations))
	}
}

func TestCanAllocateModel_Positive(t *testing.T) {
	stats := HostStats{
		RAMTotalBytes: 64 * 1024 * 1024 * 1024,
		RAMFreeBytes:  35 * 1024 * 1024 * 1024,
	}

	// 8 GB model should easily fit (35 - 8 = 27 GB > 12.8 GB reservation; util = (64 - 27)/64 = 57.8% < 85%)
	if !CanAllocateModel(&stats, 8.0) {
		t.Errorf("expected CanAllocateModel to return true for 8 GB allocation")
	}
	// 4 GB model check
	if !CanAllocateModel(&stats, 4.0) {
		t.Errorf("expected CanAllocateModel to return true for 4 GB allocation")
	}
	// 1 GB model check
	if !CanAllocateModel(&stats, 1.0) {
		t.Errorf("expected CanAllocateModel to return true for 1 GB allocation")
	}
}

func TestParsers_Positive(t *testing.T) {
	memContent := `MemTotal:       32000000 kB
MemFree:         4000000 kB
MemAvailable:   20000000 kB
Buffers:          500000 kB
Cached:         15500000 kB
`
	total, free, err := parseMeminfo(strings.NewReader(memContent))
	if err != nil {
		t.Fatalf("parseMeminfo failed: %v", err)
	}
	if total != 32000000*1024 {
		t.Errorf("unexpected total: got %d, expected %d", total, 32000000*1024)
	}
	if free != 20000000*1024 {
		t.Errorf("unexpected free: got %d, expected %d", free, 20000000*1024)
	}

	loadContent := "0.75 1.25 2.50 2/512 9999\n"
	l1, l5, l15, err := parseLoadavg(strings.NewReader(loadContent))
	if err != nil {
		t.Fatalf("parseLoadavg failed: %v", err)
	}
	if l1 != 0.75 || l5 != 1.25 || l15 != 2.50 {
		t.Errorf("unexpected load averages: %f %f %f", l1, l5, l15)
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestEvaluateHostStats_Negative_RAMReservationViolation(t *testing.T) {
	// Total = 100 GB. 20% = 20 GB. Required = 20 GB. Free = 15 GB (< 20 GB)
	stats := HostStats{
		RAMTotalBytes:  100 * 1024 * 1024 * 1024,
		RAMFreeBytes:   15 * 1024 * 1024 * 1024,
		DiskTotalBytes: 500 * 1024 * 1024 * 1024,
		DiskFreeBytes:  300 * 1024 * 1024 * 1024,
	}

	report := EvaluateHostStats(stats)
	if report.Healthy {
		t.Errorf("expected report to be unhealthy due to RAM reservation breach")
	}
	if len(report.ViolatedInvariants) == 0 {
		t.Errorf("expected violated invariants for RAM reservation breach")
	}
	found := false
	for _, inv := range report.ViolatedInvariants {
		if strings.Contains(inv, "RAM reservation invariant violated") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("missing RAM reservation invariant message in: %v", report.ViolatedInvariants)
	}
}

func TestEvaluateHostStats_Negative_DiskReservationViolation(t *testing.T) {
	// Total = 100 GB. 15% = 15 GB. Required = 15 GB. Free = 10 GB (< 15 GB)
	stats := HostStats{
		RAMTotalBytes:  64 * 1024 * 1024 * 1024,
		RAMFreeBytes:   30 * 1024 * 1024 * 1024,
		DiskTotalBytes: 100 * 1024 * 1024 * 1024,
		DiskFreeBytes:  10 * 1024 * 1024 * 1024,
	}

	report := EvaluateHostStats(stats)
	if report.Healthy {
		t.Errorf("expected report to be unhealthy due to Disk reservation breach")
	}
	if len(report.ViolatedInvariants) == 0 {
		t.Errorf("expected violated invariants for Disk reservation breach")
	}
	found := false
	for _, inv := range report.ViolatedInvariants {
		if strings.Contains(inv, "Disk reservation invariant violated") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("missing Disk reservation invariant message in: %v", report.ViolatedInvariants)
	}
}

func TestEvaluateHostStats_Negative_MemoryPressure(t *testing.T) {
	// Total = 100 GB, Free = 10 GB (90% utilized > 85% threshold)
	stats := HostStats{
		RAMTotalBytes:  100 * 1024 * 1024 * 1024,
		RAMFreeBytes:   10 * 1024 * 1024 * 1024,
		DiskTotalBytes: 500 * 1024 * 1024 * 1024,
		DiskFreeBytes:  300 * 1024 * 1024 * 1024,
	}

	report := EvaluateHostStats(stats)
	if !report.RAMPressure {
		t.Errorf("expected RAMPressure == true for 90%% utilization")
	}
	if report.Healthy {
		t.Errorf("expected Healthy == false under memory pressure")
	}
	if len(report.ThrottlingRecommendations) == 0 {
		t.Errorf("expected throttling recommendations under memory pressure")
	}
}

func TestCanAllocateModel_Negative(t *testing.T) {
	stats := HostStats{
		RAMTotalBytes: 64 * 1024 * 1024 * 1024,
		RAMFreeBytes:  20 * 1024 * 1024 * 1024,
	}

	// 1. Nil stats
	if CanAllocateModel(nil, 4.0) {
		t.Errorf("expected false when stats is nil")
	}
	// 2. Non-positive allocation
	if CanAllocateModel(&stats, 0.0) {
		t.Errorf("expected false when vramRequiredGB is 0")
	}
	if CanAllocateModel(&stats, -4.0) {
		t.Errorf("expected false when vramRequiredGB is negative")
	}
	// 3. Exceeds total free RAM
	if CanAllocateModel(&stats, 25.0) {
		t.Errorf("expected false when vram exceeds free RAM")
	}
	// 4. Leaves free RAM below 20% reservation (64 * 0.20 = 12.8 GB; 20 - 10 = 10 GB < 12.8 GB)
	if CanAllocateModel(&stats, 10.0) {
		t.Errorf("expected false when allocation breaches host reservation")
	}
}

func TestCheckHostHealth_Negative_InvalidPath(t *testing.T) {
	report, err := CheckHostHealth("/path/to/nonexistent/directory/impossible/to/exist/12345")
	if err == nil {
		t.Errorf("expected error for nonexistent path, got nil")
	}
	if report != nil {
		t.Errorf("expected nil report on failure, got %v", report)
	}
}

func TestParsers_Negative(t *testing.T) {
	// Missing MemTotal
	badMem := `MemFree: 40000 kB`
	_, _, err := parseMeminfo(strings.NewReader(badMem))
	if err == nil {
		t.Errorf("expected error for meminfo missing MemTotal")
	}

	// Empty loadavg
	_, _, _, err = parseLoadavg(strings.NewReader(""))
	if err == nil {
		t.Errorf("expected error for empty loadavg")
	}

	// Malformed loadavg
	_, _, _, err = parseLoadavg(strings.NewReader("bad format"))
	if err == nil {
		t.Errorf("expected error for invalid loadavg format")
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestEvaluateHostStats_Boundary_RAMReservation(t *testing.T) {
	// Scale 1: RAM Total = 100 GB -> 20% is 20 GB (exceeds 4 GB minimum)
	totalLarge := uint64(100 * 1024 * 1024 * 1024)
	reqLarge := CalculateRAMReservation(totalLarge)
	if reqLarge != 20*1024*1024*1024 {
		t.Fatalf("expected 20 GB reservation for 100 GB total, got %d", reqLarge)
	}

	// Exact boundary: Free == 20 GB
	statsPass := HostStats{
		RAMTotalBytes:  totalLarge,
		RAMFreeBytes:   reqLarge,
		DiskTotalBytes: 200 * 1024 * 1024 * 1024,
		DiskFreeBytes:  100 * 1024 * 1024 * 1024,
	}
	repPass := EvaluateHostStats(statsPass)
	if len(repPass.ViolatedInvariants) != 0 {
		t.Errorf("expected exact RAM reservation to pass, got: %v", repPass.ViolatedInvariants)
	}

	// Off by 1 byte: Free == 20 GB - 1 byte
	statsFail := HostStats{
		RAMTotalBytes:  totalLarge,
		RAMFreeBytes:   reqLarge - 1,
		DiskTotalBytes: 200 * 1024 * 1024 * 1024,
		DiskFreeBytes:  100 * 1024 * 1024 * 1024,
	}
	repFail := EvaluateHostStats(statsFail)
	if len(repFail.ViolatedInvariants) == 0 {
		t.Errorf("expected 1-byte deficit to fail RAM reservation invariant")
	}

	// Scale 2: Small host: RAM Total = 10 GB -> 20% is 2 GB -> minimum 4 GB floor activates
	totalSmall := uint64(10 * 1024 * 1024 * 1024)
	reqSmall := CalculateRAMReservation(totalSmall)
	if reqSmall != MinReservedRAMBytes {
		t.Fatalf("expected 4 GB floor reservation for 10 GB total, got %d", reqSmall)
	}
}

func TestEvaluateHostStats_Boundary_DiskReservation(t *testing.T) {
	// Scale 1: Disk Total = 100 GB -> 15% is 15 GB (exceeds 10 GB floor)
	totalLarge := uint64(100 * 1024 * 1024 * 1024)
	reqLarge := CalculateDiskReservation(totalLarge)
	if reqLarge != 15*1024*1024*1024 {
		t.Fatalf("expected 15 GB reservation for 100 GB total, got %d", reqLarge)
	}

	// Exact boundary
	statsPass := HostStats{
		RAMTotalBytes:  64 * 1024 * 1024 * 1024,
		RAMFreeBytes:   30 * 1024 * 1024 * 1024,
		DiskTotalBytes: totalLarge,
		DiskFreeBytes:  reqLarge,
	}
	repPass := EvaluateHostStats(statsPass)
	if len(repPass.ViolatedInvariants) != 0 {
		t.Errorf("expected exact Disk reservation to pass, got: %v", repPass.ViolatedInvariants)
	}

	// Off by 1 byte
	statsFail := statsPass
	statsFail.DiskFreeBytes = reqLarge - 1
	repFail := EvaluateHostStats(statsFail)
	if len(repFail.ViolatedInvariants) == 0 {
		t.Errorf("expected 1-byte deficit to fail Disk reservation invariant")
	}

	// Scale 2: Small disk: 40 GB -> 15% is 6 GB -> 10 GB floor activates
	totalSmall := uint64(40 * 1024 * 1024 * 1024)
	reqSmall := CalculateDiskReservation(totalSmall)
	if reqSmall != MinReservedDiskBytes {
		t.Fatalf("expected 10 GB floor reservation for 40 GB total, got %d", reqSmall)
	}
}

func TestEvaluateHostStats_Boundary_ZeroAndMax(t *testing.T) {
	// Zero totals should report cleanly without divide-by-zero panic
	statsZero := HostStats{
		RAMTotalBytes:  0,
		RAMFreeBytes:   0,
		DiskTotalBytes: 0,
		DiskFreeBytes:  0,
	}
	repZero := EvaluateHostStats(statsZero)
	if repZero.Healthy {
		t.Errorf("expected zero stats to be unhealthy")
	}
	if len(repZero.ViolatedInvariants) < 2 {
		t.Errorf("expected at least 2 invariant violations for zero RAM/Disk, got %d", len(repZero.ViolatedInvariants))
	}

	// CanAllocateModel with zero total RAM
	if CanAllocateModel(&statsZero, 2.0) {
		t.Errorf("expected false for CanAllocateModel with 0 total RAM")
	}

	// Absurdly large allocation request (overflow check)
	hugeStats := HostStats{
		RAMTotalBytes: 64 * 1024 * 1024 * 1024,
		RAMFreeBytes:  32 * 1024 * 1024 * 1024,
	}
	if CanAllocateModel(&hugeStats, 1e9) {
		t.Errorf("expected false for extreme vram requirement")
	}
}

func TestMockStatfs_Boundary(t *testing.T) {
	oldStatfs := statfsFunc
	defer func() { statfsFunc = oldStatfs }()

	statfsFunc = func(path string, buf *syscall.Statfs_t) error {
		buf.Bsize = 4096
		buf.Blocks = 1000000
		buf.Bavail = 500000
		return nil
	}

	total, free, err := readHostDisk("dummy")
	if err != nil {
		t.Fatalf("unexpected readHostDisk error: %v", err)
	}
	if total != 1000000*4096 {
		t.Errorf("expected total %d, got %d", 1000000*4096, total)
	}
	if free != 500000*4096 {
		t.Errorf("expected free %d, got %d", 500000*4096, free)
	}
}
