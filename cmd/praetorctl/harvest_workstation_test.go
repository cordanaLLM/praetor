package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

func TestHarvestWorkstationJSONRetainsIncompleteReport(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "broken", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error {
		return runHarvestWorkstation(t.Context(), []string{"--dir", root, "--json"})
	})
	if err == nil {
		t.Fatal("failed Git probe must return a nonzero CLI result")
	}
	var report harvester.WorkstationReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.RepositoryInventoryComplete || len(report.RepositoryObservations) != 1 {
		t.Fatalf("partial report was lost: %+v", report)
	}
	if report.RepositoryObservations[0].DirtyScope != "unknown" {
		t.Fatalf("failed identity must retain unknown dirty scope: %+v", report.RepositoryObservations[0])
	}
}

func TestHarvestWorkstationEmptyAndArgumentBoundary(t *testing.T) {
	root := t.TempDir()
	out, err := captureStdout(t, func() error {
		return runHarvestWorkstation(t.Context(), []string{"--dir", root, "--json"})
	})
	var report harvester.WorkstationReport
	if err != nil || json.Unmarshal([]byte(out), &report) != nil || !report.RepositoryInventoryComplete {
		t.Fatalf("empty directory must be complete: %q %v", out, err)
	}
	if err := runHarvestWorkstation(t.Context(), []string{"--dir", root, "ignored"}); err == nil {
		t.Fatal("extra positional argument must not be silently ignored")
	}
}
