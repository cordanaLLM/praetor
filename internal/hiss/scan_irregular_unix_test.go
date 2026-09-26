//go:build !windows

package hiss

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// scanDeadline bounds the FIFO fixture: the scan is given a short deadline, and the test
// waits noticeably longer for it. Without the regular-file guard the scan never returns
// at all, because os.Open on a FIFO blocks until a writer appears and the scan context is
// only consulted between walk entries.
const (
	fifoScanTimeout = 2 * time.Second
	fifoWaitLimit   = 20 * time.Second
)

type scanResult struct {
	rep *ScanReport
	err error
}

// TestScan_FIFOIsRefusedAndCounted covers BUG-822 for the HISS scanner: a planted pipe in
// an untrusted tree must be skipped, not opened. Windows has no mkfifo and no FIFO in its
// filesystem namespace, so the fixture cannot exist there; the guard itself is
// platform-neutral and the bound test covers the rest of the walk on every platform.
func TestScan_FIFOIsRefusedAndCounted(t *testing.T) {
	root := t.TempDir()
	writePanicFiles(t, root, "ok")
	fifo := filepath.Join(root, "pipe.go")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable on %s: %v", root, err)
	}

	rep := scanWithinLimit(t, root, ScanOptions{Timeout: fifoScanTimeout})
	assertViolations(t, rep, []expectedViolation{{"HISS-07", "ok.go", 4}})
	if rep.Skips.Irregular != 1 {
		t.Errorf("the FIFO must be counted as an irregular skip, got %+v", rep.Skips)
	}
	if rep.Truncated {
		t.Error("refusing a FIFO is a skip, not a truncation")
	}
	if got := rep.CoverageEvidence(); !strings.Contains(got, "non-regular file(s)") {
		t.Errorf("coverage evidence must name the refused entry, got %q", got)
	}
}

// TestScan_FIFODoesNotStarveTheRestOfTheTree is the boundary case: the entries after the
// pipe are still scanned, so one hostile entry costs one file and not the whole report.
func TestScan_FIFODoesNotStarveTheRestOfTheTree(t *testing.T) {
	root := t.TempDir()
	writePanicFiles(t, root, "aaa", "zzz")
	if err := syscall.Mkfifo(filepath.Join(root, "mmm.go"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	rep := scanWithinLimit(t, root, ScanOptions{Timeout: fifoScanTimeout})
	if rep.TotalInfractions != 2 {
		t.Fatalf("both regular files must still be scanned, got %d infraction(s): %+v",
			rep.TotalInfractions, rep.Violations)
	}
	if rep.Skips.Irregular != 1 || rep.Coverage.FilesRead != 2 {
		t.Errorf("expected one irregular skip and two files read, got skips=%+v coverage=%+v",
			rep.Skips, rep.Coverage)
	}
}

// scanWithinLimit runs Scan off the test goroutine and fails the test when it does not
// return, so a scanner that blocks on a special file reports as a failure rather than as
// a suite-wide timeout.
func scanWithinLimit(t *testing.T, root string, opts ScanOptions) *ScanReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fifoWaitLimit)
	defer cancel()
	done := make(chan scanResult, 1)
	go func() {
		rep, err := Scan(ctx, root, opts)
		done <- scanResult{rep: rep, err: err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("Scan failed: %v", res.err)
		}
		return res.rep
	case <-time.After(fifoWaitLimit):
		t.Fatalf("Scan blocked past its %s deadline; a non-regular entry was opened", opts.Timeout)
		return nil
	}
}
