package adopt

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// planDriftRepo writes a governed repository with every baseline file and the ruleset,
// plus the extra files given, and returns its root.
func planDriftRepo(t *testing.T, extra map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		".standards.lock":     "{}\n",
		"AGENTS.md":           "# Agents\n",
		".config/labels.yaml": "labels: []\n",
		rulesetFile:           "{}\n",
	}
	for rel, content := range extra {
		files[rel] = content
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// sbomPolicy is a resolved policy that requires an SBOM and a branch ruleset.
func sbomPolicy() *config.ResolvedPolicy {
	policy := config.DefaultPolicy()
	policy.SupplyChain.RequireSBOM = true
	policy.BranchProtection.EnforceLinearHistory = true
	return policy
}

const releaseWorkflowWithSBOM = "on: [push]\njobs:\n  release:\n    runs-on: ubuntu-latest\n    steps:\n" +
	"      - uses: goreleaser/goreleaser-action@v7\n        with:\n          args: release --clean\n"

// Positive: an SBOM produced by the release workflow satisfies require_sbom without a file
// named sbom.yml, and so does a dedicated sbom.yml that runs a generator (#43).
func TestPlanDriftAcceptsReleaseOrDedicatedSBOMWorkflow(t *testing.T) {
	repos := map[string]map[string]string{
		"release workflow": {
			".github/workflows/release.yml": releaseWorkflowWithSBOM,
			".goreleaser.yaml":              "sboms:\n  - artifacts: archive\n",
		},
		"dedicated sbom.yml": {
			".github/workflows/sbom.yml": "on: [push]\njobs:\n  sbom:\n    steps:\n      - run: syft dir:dist -o cyclonedx-json=sbom.json\n",
		},
	}
	for name, files := range repos {
		t.Run(name, func(t *testing.T) {
			missing, drift, err := PlanDrift(context.Background(), planDriftRepo(t, files), sbomPolicy())
			if err != nil || len(missing) != 0 || len(drift) != 0 {
				t.Fatalf("missing %v, drift %v, err %v; want a clean plan", missing, drift, err)
			}
		})
	}
}

// Negative: no generating workflow is drift even when a file named sbom.yml exists, and
// missing baseline files and ruleset are still reported.
func TestPlanDriftReportsMissingSBOMAndBaseline(t *testing.T) {
	root := planDriftRepo(t, map[string]string{
		".github/workflows/sbom.yml": "on: [push]\njobs:\n  sbom:\n    steps:\n      - run: echo nothing\n",
	})
	for _, rel := range []string{".standards.lock", rulesetFile} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	missing, drift, err := PlanDrift(context.Background(), root, sbomPolicy())
	if err != nil {
		t.Fatalf("PlanDrift: %v", err)
	}
	if !slices.Equal(missing, []string{".standards.lock"}) {
		t.Errorf("missing = %v", missing)
	}
	want := []string{rulesetFile + " (Branch protection ruleset missing)", SBOMWorkflowDrift}
	if !slices.Equal(drift, want) {
		t.Errorf("drift = %v, want %v", drift, want)
	}
}

// Positive, negative and boundary: a clean plan says so; drift lists every line and asks
// for sync; missing files alone still ask for sync without a drift heading.
func TestFormatPlanStatus(t *testing.T) {
	if got := FormatPlanStatus(nil, nil); got != "\nStatus: Local state matches declared policy. No changes required." {
		t.Errorf("clean plan = %q", got)
	}
	got := FormatPlanStatus([]string{"AGENTS.md"}, []string{SBOMWorkflowDrift})
	want := "\n[DRIFT] Missing baseline files: AGENTS.md\n\n[DRIFT] Policy drift detected:\n  - " + SBOMWorkflowDrift +
		"\n\nAction: Run 'praetorctl sync' to reconcile repository configuration."
	if got != want {
		t.Errorf("drifted plan = %q, want %q", got, want)
	}
	onlyMissing := FormatPlanStatus([]string{".standards.lock"}, nil)
	if want := "\n[DRIFT] Missing baseline files: .standards.lock\n\nAction: Run 'praetorctl sync' to reconcile repository configuration."; onlyMissing != want {
		t.Errorf("missing-only plan = %q, want %q", onlyMissing, want)
	}
}

// Boundary: a policy that requires no SBOM never inspects workflows, so an unparseable
// one is not reported; once it is required the same file is an error, not a pass.
func TestPlanDriftBoundaries(t *testing.T) {
	root := planDriftRepo(t, map[string]string{".github/workflows/broken.yml": "jobs: [\n"})
	policy := config.DefaultPolicy()
	policy.SupplyChain.RequireSBOM = false
	if _, drift, err := PlanDrift(context.Background(), root, policy); err != nil || len(drift) != 0 {
		t.Errorf("SBOM not required: drift %v, err %v", drift, err)
	}
	if _, _, err := PlanDrift(context.Background(), root, sbomPolicy()); err == nil {
		t.Error("malformed workflow accepted while an SBOM is required")
	}
	var absent context.Context
	if _, _, err := PlanDrift(absent, root, policy); err == nil {
		t.Error("nil context accepted")
	}
	if _, _, err := PlanDrift(context.Background(), root, nil); err == nil {
		t.Error("nil policy accepted")
	}
}
