package adopt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// overlaidRepoRoot copies the engine's own checked-in workflow files, unchanged, into a fresh
// temp root beside a manifest shaped the way internal/operationalsync's owner overlay leaves
// .standards.yaml: owner rewritten to the fork's own identity, repository.source recording the
// public source. It reproduces the fork-side input to requiredStatusContexts without touching
// a real fork checkout (#255).
func overlaidRepoRoot(t *testing.T) string {
	t.Helper()
	engineRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(engineRoot, ".github", "workflows"))
	if err != nil {
		t.Fatalf("read engine workflows: %v", err)
	}
	root := t.TempDir()
	workflows := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflows, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(entries) && i < maxSnapshotEntries; i++ {
		if entries[i].IsDir() {
			continue
		}
		data := mustRead(t, filepath.Join(engineRoot, ".github", "workflows", entries[i].Name()))
		mustWrite(t, filepath.Join(workflows, entries[i].Name()), data)
	}
	mustWrite(t, filepath.Join(root, ".standards.yaml"),
		"version: 1\nrepository:\n  owner: \"lusoris\"\n  name: \"praetor\"\n  visibility: \"private\"\n  source: \"cordanaLLM/praetor\"\n")
	return root
}

// TestRulesetMatchesCheckedInFileUnderOverlaidManifest is issue #255's ruleset-side
// regression, run beside TestRulesetMatchesCheckedInFile: internal/operationalsync's owner
// overlay rewrites repository.owner to the fork's own identity and records the public source
// in repository.source. Required status contexts computed against that overlaid manifest --
// with the engine's own unmodified workflow files -- must equal the checked-in ruleset the
// canonical manifest already generates, or a fork's first `adopt` run drifts the moment the
// overlay is applied.
func TestRulesetMatchesCheckedInFileUnderOverlaidManifest(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	checkedIn, err := os.ReadFile(filepath.Join(repoRoot, ".github", "rulesets", "main.json"))
	if err != nil {
		t.Fatalf("read checked-in ruleset: %v", err)
	}
	manifest, err := config.LoadManifest(filepath.Join(repoRoot, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)

	contexts, err := requiredStatusContexts(overlaidRepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	generated, err := buildRulesetJSON(policy.BranchProtection, contexts)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := jsonUnmarshal(checkedIn, &want); err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshal([]byte(generated), &got); err != nil {
		t.Fatal(err)
	}
	if !deepEqual(want, got) {
		t.Fatalf("ruleset generated against the overlaid manifest drifted from .github/rulesets/main.json\nchecked in:\n%s\ngenerated:\n%s", checkedIn, generated)
	}
}
