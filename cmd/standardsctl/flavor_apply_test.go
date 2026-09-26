package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// TestApplyFlavorCLI_Negative_PrintsReportBesideFailure asserts the report is printed when the
// library fails the apply, so an operator still sees what was written before the failure.
func TestApplyFlavorCLI_Negative_PrintsReportBesideFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Dockerfile"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return applyFlavor(t.Context(), dir, "go-service", false) })
	if !errors.Is(err, flavor.ErrApplyIncomplete) {
		t.Fatalf("a failed template must fail flavor apply with ErrApplyIncomplete, got %v", err)
	}
	if !strings.Contains(out, "Created Templates (4)") || !strings.Contains(out, "Errors (1):") {
		t.Fatalf("the report must be printed beside the failure, got:\n%s", out)
	}
}

// TestApplyFlavorCLI_Positive_CleanApply asserts a clean apply returns nil and prints the report.
func TestApplyFlavorCLI_Positive_CleanApply(t *testing.T) {
	out, err := captureStdout(t, func() error { return applyFlavor(t.Context(), t.TempDir(), "go-service", false) })
	if err != nil || !strings.Contains(out, "Created Templates (5)") || strings.Contains(out, "Errors") {
		t.Fatalf("clean apply: %v\n%s", err, out)
	}
}

// TestApplyFlavorCLI_Boundary_NoReportOnRefusal asserts a refusal before any write (an unknown
// flavor) prints no report and still fails.
func TestApplyFlavorCLI_Boundary_NoReportOnRefusal(t *testing.T) {
	out, err := captureStdout(t, func() error { return applyFlavor(t.Context(), t.TempDir(), "no-such-flavor", false) })
	if err == nil || strings.Contains(out, "Applied Flavor") {
		t.Fatalf("an unknown flavor must fail without a report: %v\n%s", err, out)
	}
}
