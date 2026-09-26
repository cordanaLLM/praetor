package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/needs"
)

func TestDescribeEpicIssue_NamesTheOutcome(t *testing.T) {
	created := &forge.IssueUpsertResult{
		IssueResponse: forge.IssueResponse{Number: 4, URL: "https://forge.test/issues/4"},
		Outcome:       forge.IssueCreated,
	}
	if got, want := describeEpicIssue("o/r", created), "o/r#4 created https://forge.test/issues/4"; got != want {
		t.Errorf("created issue: got %q, want %q", got, want)
	}

	// An issue resolved from the inventory carries no URL and must not claim creation.
	reused := &forge.IssueUpsertResult{IssueResponse: forge.IssueResponse{Number: 9}, Outcome: forge.IssueUnchanged}
	if got, want := describeEpicIssue("o/r", reused), "o/r#9 already published (unchanged)"; got != want {
		t.Errorf("reused issue: got %q, want %q", got, want)
	}
}

func TestRunFleetNeedsEpic_PrintsSkippedDirectories(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "org", "not-a-repo")
	if err := os.MkdirAll(bare, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "go.mod"), []byte("module example.org/bare\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdout(t, func() error {
		return runFleetNeedsEpic(context.Background(), root, needs.FleetEpicOptions{DryRun: true})
	})
	if err != nil {
		t.Fatalf("fleet run failed: %v", err)
	}
	if !strings.Contains(out, "[SKIP] 1 discovered directories") || !strings.Contains(out, bare+": no repository marker") {
		t.Fatalf("skipped directory missing from the report:\n%s", out)
	}

	quiet, err := captureStdout(t, func() error {
		printFleetEpicSkips(nil)
		return nil
	})
	if err != nil || quiet != "" {
		t.Fatalf("no skips must print nothing, got %q (err %v)", quiet, err)
	}
}
