package flavor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/state"
)

func TestFlavorForcePreservesLedgerContent(t *testing.T) {
	dir := t.TempDir()
	if err := state.InitWorkingDirContext(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".workingdir", "STATE.md")
	const history = "# Retained history\nDo not replace during template refresh.\n"
	if err := os.WriteFile(path, []byte(history), 0o600); err != nil {
		t.Fatal(err)
	}
	if report, err := flavor.ApplyFlavor(t.Context(), dir, "go-service", true); err != nil || len(report.Errors) != 0 {
		t.Fatalf("apply failed: %+v, %v", report, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != history {
		t.Fatalf("forced template refresh destroyed ledger: %q, %v", data, err)
	}
}

func TestFlavorRejectsCancellationBeforeWrites(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := flavor.ApplyFlavor(ctx, dir, "go-service", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled apply should fail: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled apply created files: %v, %v", entries, err)
	}
}

func TestFlavorRejectsSymlinkTemplateTargets(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(outside, "target")
	if err := os.WriteFile(path, []byte("private content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(dir, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	report, err := flavor.ApplyFlavor(t.Context(), dir, "go-service", true)
	if !errors.Is(err, flavor.ErrApplyIncomplete) || report == nil || len(report.Errors) == 0 {
		t.Fatalf("symlink template must produce a reported failure: %+v, %v", report, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "private content" {
		t.Fatalf("external source changed: %q, %v", data, err)
	}
}

// goServiceTemplates is the go-service template set, in the order the flavor declares it.
var goServiceTemplates = []string{".golangci.yml", ".gosec.json", ".github/workflows/ci.yml", ".github/workflows/security.yml", "Dockerfile"}

// blockTemplates puts a directory at each template path, so reading it fails without touching
// the ledger that ApplyFlavor seeds first.
func blockTemplates(t *testing.T, dir string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestApplyFlavor_Negative_EveryTemplateFailedIsAnError pins BUG-188: a scaffold in which every
// write failed used to return (report, nil), so adoption, which checked only the error, reported
// success.
func TestApplyFlavor_Negative_EveryTemplateFailedIsAnError(t *testing.T) {
	dir := t.TempDir()
	blockTemplates(t, dir, goServiceTemplates...)
	report, err := flavor.ApplyFlavor(t.Context(), dir, "go-service", false)
	if !errors.Is(err, flavor.ErrApplyIncomplete) {
		t.Fatalf("a scaffold in which every template failed must return ErrApplyIncomplete, got %v", err)
	}
	if report == nil || len(report.Errors) != len(goServiceTemplates) || len(report.CreatedTemplates) != 0 {
		t.Fatalf("the report must come back beside the error and name every failure: %+v", report)
	}
}

// TestApplyFlavor_Boundary_PartialFailureKeepsCreatedTemplates asserts one failure is enough to
// fail the apply, and that the report still names what was written before it.
func TestApplyFlavor_Boundary_PartialFailureKeepsCreatedTemplates(t *testing.T) {
	dir := t.TempDir()
	blockTemplates(t, dir, "Dockerfile")
	report, err := flavor.ApplyFlavor(t.Context(), dir, "go-service", false)
	if !errors.Is(err, flavor.ErrApplyIncomplete) || report == nil {
		t.Fatalf("one failed template must fail the apply with a report: %+v, %v", report, err)
	}
	if len(report.Errors) != 1 || len(report.CreatedTemplates) != len(goServiceTemplates)-1 {
		t.Fatalf("expected 1 failure and %d created templates, got %+v", len(goServiceTemplates)-1, report)
	}
}

// TestApplyFlavor_Positive_ForceKeepsManifestAndLock pins BUG-027: go-library lists the manifest
// and lock as templates with no content of their own, so --force replaced a real declaration and
// its pinned digests with a one-line comment stub.
func TestApplyFlavor_Positive_ForceKeepsManifestAndLock(t *testing.T) {
	dir := t.TempDir()
	const manifest = "version: 1\nprofile: framework\nfacets: [security:high]\n"
	const lock = "{\"version\":1,\"digest\":\"sha256:0000\"}\n"
	mustWriteFile(t, filepath.Join(dir, ".standards.yaml"), manifest)
	mustWriteFile(t, filepath.Join(dir, ".standards.lock"), lock)
	mustWriteFile(t, filepath.Join(dir, ".golangci.yml"), "version: \"2\"\n# operator tuned\n")

	report, err := flavor.ApplyFlavor(t.Context(), dir, "go-library", true)
	if err != nil {
		t.Fatalf("forced apply failed: %+v, %v", report, err)
	}
	for rel, want := range map[string]string{".standards.yaml": manifest, ".standards.lock": lock} {
		data, readErr := os.ReadFile(filepath.Join(dir, rel))
		if readErr != nil || string(data) != want {
			t.Errorf("--force replaced %s: %q, %v", rel, data, readErr)
		}
	}
	// --force still refreshes a scaffold the flavor owns, so the guard is narrow.
	data, err := os.ReadFile(filepath.Join(dir, ".golangci.yml"))
	if err != nil || strings.Contains(string(data), "operator tuned") {
		t.Errorf("--force must still refresh flavor-owned scaffolds: %q, %v", data, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
