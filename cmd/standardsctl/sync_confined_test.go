package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: both synthesized files land below the repository root.
func TestSyncSynthesisConfined_Positive(t *testing.T) {
	dir := t.TempDir()
	if err := synthesizeDefaultLabels(dir); err != nil {
		t.Fatalf("synthesizeDefaultLabels: %v", err)
	}
	if err := synthesizeRuleset(dir, "main", config.DefaultPolicy().BranchProtection, nil); err != nil {
		t.Fatalf("synthesizeRuleset: %v", err)
	}
	for _, rel := range []string{syncLabelsRel, syncRulesetRel} {
		if info, err := os.Lstat(filepath.Join(dir, rel)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s = %v, %v; want a regular file", rel, info, err)
		}
	}
}

// Negative: a .github linked outside the repository is refused before the ruleset is written
// through it (BUG-826).
func TestSyncSynthesisConfined_Negative_EscapingGitHubLink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".github")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := synthesizeRuleset(dir, "main", config.DefaultPolicy().BranchProtection, nil); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Fatalf("escaping .github = %v, want ErrPathEscapesRoot", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside directory = %v, %v; want nothing written through the link", entries, err)
	}
}

// Boundary: a .config that is a relative link staying inside the repository is followed; the
// confinement edge is the repository root.
func TestSyncSynthesisConfined_Boundary_InRootLinkFollowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("settings", filepath.Join(dir, ".config")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := synthesizeDefaultLabels(dir); err != nil {
		t.Fatalf("in-repository linked .config = %v, want the write to follow it", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings", "labels.yaml")); err != nil {
		t.Fatalf("labels not written through the in-repository link: %v", err)
	}
}
