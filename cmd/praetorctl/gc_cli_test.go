package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gc"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

func TestGCCLIRejectsUnsafeArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--path", t.TempDir(), "--max-age=0"},
		{"--path", t.TempDir(), "--artifact-max-age=0"},
		{"--path", t.TempDir(), "unexpected"},
		{"--path", t.TempDir(), "--released-path="},
	} {
		if err := runGC(args); err == nil {
			t.Fatalf("runGC(%q) unexpectedly succeeded", args)
		}
	}
}

func TestGCCLIRepeatedReleaseAndJSONReport(t *testing.T) {
	var paths repeatedStringFlag
	first := t.TempDir()
	second := t.TempDir()
	if err := paths.Set(first); err != nil {
		t.Fatal(err)
	}
	if err := paths.Set(second); err != nil {
		t.Fatal(err)
	}
	if got := paths.String(); got != first+","+second {
		t.Fatalf("repeated flag rendered as %q", got)
	}

	report := &gc.GCReport{
		DryRun:                true,
		Complete:              true,
		PlannedWorktrees:      []string{first},
		PlannedArtifacts:      []string{second},
		PlannedReclaimedBytes: 12,
	}
	out, err := captureStdout(t, func() error {
		printGCReport(report, true, nil)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\"dry_run\":true", "\"complete\":true", "planned_worktrees", "planned_artifacts"} {
		if !strings.Contains(out, want) {
			t.Fatalf("JSON report missing %q: %s", want, out)
		}
	}
}

func TestWorktreeStatusUsesRegisteredLabel(t *testing.T) {
	if got := worktreeStatus(worktree.WorktreeInfo{}); got != "registered" {
		t.Fatalf("worktree status = %q, want registered", got)
	}
}
