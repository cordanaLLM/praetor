package sentinel

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	// Which answer is correct depends on whether this host has a memory source, and both answers
	// are asserted: a reading where one exists, and an explicit "unavailable" where none does.
	// Asserting a reading everywhere failed on macOS, which by design reports memory unavailable.
	if hostHasMemorySource() {
		if report.Stats.RAMTotalBytes == 0 {
			t.Errorf("expected RAMTotalBytes > 0, got %d", report.Stats.RAMTotalBytes)
		}
	} else if report.Stats.RAMTotalBytes != 0 || !reportsRAMUnavailable(report) {
		t.Errorf("a host without a memory source must report memory unavailable, got total=%d invariants=%v",
			report.Stats.RAMTotalBytes, report.ViolatedInvariants)
	}
	if report.Stats.DiskTotalBytes == 0 {
		t.Errorf("expected DiskTotalBytes > 0, got %d", report.Stats.DiskTotalBytes)
	}
}

// hostHasMemorySource reports whether readHostMemory has something to read on this host:
// GlobalMemoryStatusEx on Windows, /proc/meminfo elsewhere.
func hostHasMemorySource() bool {
	if runtime.GOOS == "windows" {
		return true
	}
	_, err := os.Stat(defaultMeminfoPath)
	return err == nil
}

func reportsRAMUnavailable(report *HostReport) bool {
	for _, v := range report.ViolatedInvariants {
		if strings.Contains(v, "RAM unavailable") {
			return true
		}
	}
	return false
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

// =========================================================================
// Unmeasured memory is reported, never invented (praetor#89)
// =========================================================================

// TestSentinel_Negative_UnmeasuredRAMIsNotInvented is the regression.
//
// readHostMemory returned a hardcoded 16 GB total and 8 GB free wherever /proc/meminfo was absent
// -- every macOS and Windows host -- with a nil error, so no caller could tell. The free figure was
// derived from runtime.MemStats, which measures this process's Go heap rather than the machine.
// RAM pressure, the reserve ratio and model admission were all decided from it.
func TestSentinel_Negative_UnmeasuredRAMIsNotInvented(t *testing.T) {
	report := EvaluateHostStats(HostStats{
		DiskTotalBytes: 500 * 1024 * 1024 * 1024,
		DiskFreeBytes:  400 * 1024 * 1024 * 1024,
	})
	var found bool
	for _, v := range report.ViolatedInvariants {
		if strings.Contains(v, "RAM unavailable") {
			found = true
		}
	}
	if !found {
		t.Errorf("unmeasured memory must be reported, got %v", report.ViolatedInvariants)
	}
	if report.RAMPressure {
		t.Error("no pressure verdict may be reached without a reading")
	}
	if report.RAMUtilizationPercent != 0 {
		t.Errorf("utilisation computed from no reading: %v", report.RAMUtilizationPercent)
	}
}

// TestSentinel_Negative_AdmissionRefusedWithoutAReading pins behaviour that already held: the
// existing zero-total guard in CanAllocateModel makes the unmeasured case safe. An added second
// guard was removed as duplication once a mutation showed it changed nothing.
func TestSentinel_Negative_AdmissionRefusedWithoutAReading(t *testing.T) {
	if CanAllocateModel(&HostStats{}, 8) {
		t.Error("model admission must refuse where memory was never measured")
	}
	if CanAllocateModel(nil, 8) {
		t.Error("a nil host must not admit")
	}
}

// TestSentinel_Positive_MeasuredRAMStillDecides confirms the guard did not disable the real path.
func TestSentinel_Positive_MeasuredRAMStillDecides(t *testing.T) {
	report := EvaluateHostStats(HostStats{
		RAMTotalBytes:  64 * 1024 * 1024 * 1024,
		RAMFreeBytes:   2 * 1024 * 1024 * 1024,
		DiskTotalBytes: 500 * 1024 * 1024 * 1024,
		DiskFreeBytes:  400 * 1024 * 1024 * 1024,
	})
	if !report.RAMPressure {
		t.Error("a measured host at 97% must still report pressure")
	}
	for _, v := range report.ViolatedInvariants {
		if strings.Contains(v, "RAM unavailable") {
			t.Errorf("a measured host must not report unavailability: %v", v)
		}
	}
}

// TestReadMeminfoMemory_Boundary_AbsentSourceReportsZeroNotAnError is the test that actually defends
// the regression, and it runs everywhere.
//
// An earlier version skipped on any host exposing /proc/meminfo, which is every machine the tests
// are run on -- so restoring the fabricated 16 GB fallback passed it. A test that cannot run where
// the bug lives does not defend against it. Pointing meminfoPath at a missing file reaches the
// absent-source path on Linux.
func TestReadMeminfoMemory_Boundary_AbsentSourceReportsZeroNotAnError(t *testing.T) {
	original := meminfoPath
	t.Cleanup(func() { meminfoPath = original })
	meminfoPath = filepath.Join(t.TempDir(), "absent-meminfo")

	total, free, err := readMeminfoMemory()
	if err != nil {
		t.Fatalf("an absent source must not error; the sentinel still has good disk and CPU readings: %v", err)
	}
	if total != 0 || free != 0 {
		t.Errorf("an absent source must report zero, not an invented baseline; got total=%d free=%d",
			total, free)
	}
}

// TestReadMeminfoMemory_Positive_RealSourceStillParses confirms injecting the path did not break the
// ordinary case.
func TestReadMeminfoMemory_Positive_RealSourceStillParses(t *testing.T) {
	if _, err := os.Stat(defaultMeminfoPath); err != nil {
		t.Skip("no /proc/meminfo on this host")
	}
	total, _, err := readMeminfoMemory()
	if err != nil {
		t.Fatalf("reading the real source failed: %v", err)
	}
	if total == 0 {
		t.Error("a readable /proc/meminfo must report a nonzero total")
	}
}

// withLoadavg points loadavgPath at content, or at a missing file when content is empty.
func withLoadavg(t *testing.T, content string) {
	t.Helper()
	original := loadavgPath
	t.Cleanup(func() { loadavgPath = original })
	loadavgPath = filepath.Join(t.TempDir(), "loadavg")
	if content == "" {
		return
	}
	if err := os.WriteFile(loadavgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReadHostCPULoad_Positive_ReadingIsMarkedMeasured: an idle host's zero load is a reading.
func TestReadHostCPULoad_Positive_ReadingIsMarkedMeasured(t *testing.T) {
	withLoadavg(t, "0.00 0.00 0.00 1/100 42\n")
	l1, l5, l15, measured, err := readHostCPULoad()
	if err != nil || !measured || l1 != 0 || l5 != 0 || l15 != 0 {
		t.Fatalf("an idle reading was not reported as measured: %v %v %v measured=%v err=%v", l1, l5, l15, measured, err)
	}
}

// TestReadHostCPULoad_Negative_MalformedSourceIsNotMeasured: a source that exists but cannot be
// parsed is an error and never a measurement.
func TestReadHostCPULoad_Negative_MalformedSourceIsNotMeasured(t *testing.T) {
	withLoadavg(t, "not a load average\n")
	if _, _, _, measured, err := readHostCPULoad(); err == nil || measured {
		t.Fatalf("a malformed source was accepted: measured=%v err=%v", measured, err)
	}
}

// TestReadHostCPULoad_Boundary_AbsentSourceIsUnmeasuredNotIdle is the case macOS and Windows hit:
// no source is not an error, and it is not reported as an idle machine either.
func TestReadHostCPULoad_Boundary_AbsentSourceIsUnmeasuredNotIdle(t *testing.T) {
	withLoadavg(t, "")
	l1, l5, l15, measured, err := readHostCPULoad()
	if err != nil || measured || l1 != 0 || l5 != 0 || l15 != 0 {
		t.Fatalf("an absent source was not reported as unmeasured: %v %v %v measured=%v err=%v", l1, l5, l15, measured, err)
	}
}

const gib = uint64(1024 * 1024 * 1024)

// TestCanAllocateModel_Boundary_ExactReservation: a load that leaves exactly the reservation
// free is admitted, one byte less is refused, on both sides of the 4 GB floor.
func TestCanAllocateModel_Boundary_ExactReservation(t *testing.T) {
	for _, total := range []uint64{16 * gib, 20 * gib, 64 * gib} {
		reserve := CalculateRAMReservation(total)
		exact := HostStats{RAMTotalBytes: total, RAMFreeBytes: reserve + 2*gib}
		if !CanAllocateModel(&exact, 2) {
			t.Fatalf("total %d: a load leaving exactly the reservation was refused", total)
		}
		short := HostStats{RAMTotalBytes: total, RAMFreeBytes: reserve + 2*gib - 1}
		if CanAllocateModel(&short, 2) {
			t.Fatalf("total %d: a load leaving one byte under the reservation was admitted", total)
		}
	}
}

// TestCanAllocateModel_Negative_InconsistentReading: more free than total RAM is refused
// explicitly; before, only an unsigned underflow in the utilization arithmetic refused it.
func TestCanAllocateModel_Negative_InconsistentReading(t *testing.T) {
	stats := HostStats{RAMTotalBytes: 8 * gib, RAMFreeBytes: 64 * gib}
	if CanAllocateModel(&stats, 1) {
		t.Fatal("a reading with more free than total RAM was admitted")
	}
}

// TestCanAllocateModel_Positive_ReservationBoundsUtilization pins why no separate utilization
// guard exists: every load the reservation admits leaves utilization at or under
// 1-RAMReserveRatio, below RAMPressureThreshold, across the ratio and the 4 GB floor regimes.
func TestCanAllocateModel_Positive_ReservationBoundsUtilization(t *testing.T) {
	ceiling := 1 - RAMReserveRatio
	if ceiling >= RAMPressureThreshold {
		t.Fatalf("reservation ceiling %.2f no longer sits below the pressure threshold %.2f", ceiling, RAMPressureThreshold)
	}
	admitted := 0
	for _, totalGiB := range []uint64{6, 8, 16, 20, 21, 64, 256} {
		total := totalGiB * gib
		for freeGiB := uint64(0); freeGiB <= totalGiB; freeGiB++ {
			stats := HostStats{RAMTotalBytes: total, RAMFreeBytes: freeGiB * gib}
			for reqGiB := uint64(1); reqGiB <= freeGiB && reqGiB <= 64; reqGiB++ {
				if !CanAllocateModel(&stats, float64(reqGiB)) {
					continue
				}
				admitted++
				util := float64(total-(freeGiB-reqGiB)*gib) / float64(total)
				if util > ceiling+1e-9 {
					t.Fatalf("total %d GiB, free %d, load %d: admitted at utilization %.4f", totalGiB, freeGiB, reqGiB, util)
				}
			}
		}
	}
	if admitted == 0 {
		t.Fatal("the grid admitted no load, so it proves nothing")
	}
}
