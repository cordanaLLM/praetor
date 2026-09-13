package flavor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if err != nil || len(report.Errors) == 0 {
		t.Fatalf("symlink template must produce a reported failure: %+v, %v", report, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "private content" {
		t.Fatalf("external source changed: %q, %v", data, err)
	}
}
