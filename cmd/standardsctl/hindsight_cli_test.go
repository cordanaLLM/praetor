// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/hindsight"
)

func runDistillCLI(t *testing.T, root string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return captureStdout(t, func() error { return runHindsightDistill(ctx, []string{root}) })
}

func TestHindsightDistillCLI_Positive_CleanRepositoryPrintsNoWarnings(t *testing.T) {
	root := t.TempDir()
	out, err := runDistillCLI(t, root)
	if err != nil {
		t.Fatalf("distill: %v\n%s", err, out)
	}
	if strings.Contains(out, "Warning:") {
		t.Errorf("clean repository printed warnings:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, hindsight.MemoryFileRel)); err != nil {
		t.Errorf("cache not written: %v", err)
	}
}

func TestHindsightDistillCLI_Negative_FailedSourcesArePrinted(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "broken.go", "package broken\n\nfunc (\n")
	writeFixtureFile(t, root, ".workingdir/docs/catalog.json", "{not json")
	out, err := runDistillCLI(t, root)
	if err != nil {
		t.Fatalf("partial distill must still succeed: %v\n%s", err, out)
	}
	for _, want := range []string{"Warning: distill workspace dedupe", "Warning: distill workspace package docs"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// A malformed ledger is a required source: the command fails and leaves no cache behind.
func TestHindsightDistillCLI_Boundary_RequiredSourceFailureWritesNoCache(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".workingdir/BUGS.md",
		"# Bug Ledger\n\n| ID | Title | Severity | Status | Location | Resolution |\n| --- | --- | --- | --- | --- | --- |\n| `BUG-002` | truncated row |\n")
	out, err := runDistillCLI(t, root)
	if err == nil || !strings.Contains(err.Error(), "distill workspace bug ledger") {
		t.Fatalf("malformed ledger must fail the command, got %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(root, hindsight.MemoryFileRel)); !os.IsNotExist(statErr) {
		t.Errorf("failed distillation wrote a cache: %v", statErr)
	}
}
