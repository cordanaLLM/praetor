package lockdown

import (
	"errors"
	"strings"
	"testing"
)

// gateOutputWith renders gate output shaped like gating.PipelineReport.StageOutput, with the
// given worktree header lines in place of the one the gate writes.
func gateOutputWith(worktreeLines ...string) string {
	lines := append([]string{"praetor-gate-output/v2", "repository\tacme/widget", "commit_sha\tdeadbeef"}, worktreeLines...)
	lines = append(lines, "dry_run\tfalse", "stage\tHISS Invariant Scan\tpassed\t0 infractions")
	return strings.Join(lines, "\n") + "\n"
}

func TestRequireCleanWorktree_Positive_CleanHeaderPasses(t *testing.T) {
	if err := RequireCleanWorktree(gateOutputWith(WorktreeCleanLine(true))); err != nil {
		t.Fatalf("a clean header was refused: %v", err)
	}
	if got := WorktreeCleanLine(true); got != "worktree_clean\ttrue" {
		t.Fatalf("WorktreeCleanLine(true) = %q; the signed format must not drift", got)
	}
}

func TestRequireCleanWorktree_Negative_DirtyOrUnreadableHeaderIsRefused(t *testing.T) {
	if err := RequireCleanWorktree(gateOutputWith(WorktreeCleanLine(false))); !errors.Is(err, ErrWorktreeNotClean) {
		t.Fatalf("worktree_clean false must be refused as not clean, got %v", err)
	}
	for name, value := range map[string]string{"empty": "", "capitalised": "True", "padded": "true "} {
		if err := RequireCleanWorktree(gateOutputWith("worktree_clean\t" + value)); !errors.Is(err, ErrWorktreeNotClean) {
			t.Errorf("%s value %q must be refused, got %v", name, value, err)
		}
	}
}

// TestRequireCleanWorktree_Boundary_OnlyOneHeaderLineCounts: the field must appear exactly
// once, before the stage lines; a stage line that happens to carry it, or output that is not
// gate output at all, proves nothing.
func TestRequireCleanWorktree_Boundary_OnlyOneHeaderLineCounts(t *testing.T) {
	cases := map[string]string{
		"missing":     gateOutputWith(),
		"duplicate":   gateOutputWith(WorktreeCleanLine(true), WorktreeCleanLine(true)),
		"conflicting": gateOutputWith(WorktreeCleanLine(false), WorktreeCleanLine(true)),
		"after a stage line": "praetor-gate-output/v2\nstage\tHISS\tpassed\t\n" +
			WorktreeCleanLine(true) + "\n",
		"not gate output": "gates passed",
		"empty":           "",
	}
	for name, output := range cases {
		if err := RequireCleanWorktree(output); !errors.Is(err, ErrWorktreeUnrecorded) {
			t.Errorf("%s: want ErrWorktreeUnrecorded, got %v", name, err)
		}
	}
	past := strings.Repeat("filler\tx\n", maxGateOutputHeaderLines) + WorktreeCleanLine(true) + "\n"
	if err := RequireCleanWorktree(past); !errors.Is(err, ErrWorktreeUnrecorded) {
		t.Errorf("a field past the header bound must not be read, got %v", err)
	}
	within := strings.Repeat("filler\tx\n", maxGateOutputHeaderLines-1) + WorktreeCleanLine(true) + "\n"
	if err := RequireCleanWorktree(within); err != nil {
		t.Errorf("a field on the last header line must be read, got %v", err)
	}
}
