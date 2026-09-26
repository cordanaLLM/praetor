package main

import (
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestSyncSynthesis_Positive_TrackedModes pins BUG-839: the label taxonomy and the branch
// ruleset sync synthesizes are committed files, so they and the directories created for them
// carry the tracked-file modes instead of owner-only ones.
func TestSyncSynthesis_Positive_TrackedModes(t *testing.T) {
	dir := t.TempDir()
	labels := filepath.Join(dir, ".config", "labels.yaml")
	ruleset := filepath.Join(dir, ".github", "rulesets", "main.json")
	if err := synthesizeDefaultLabels(labels); err != nil {
		t.Fatalf("synthesizeDefaultLabels: %v", err)
	}
	if err := synthesizeRuleset(ruleset, config.DefaultPolicy().BranchProtection, nil); err != nil {
		t.Fatalf("synthesizeRuleset: %v", err)
	}
	for _, path := range []string{labels, ruleset} {
		testsupport.RequireCreatedMode(t, path, 0o644)
		testsupport.RequireCreatedMode(t, filepath.Dir(path), 0o755)
	}
}
