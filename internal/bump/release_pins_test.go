// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releasePipelineActions are the signing and SBOM actions the engine's release workflows
// install. knownActionLatest is the baseline the workflow scanner compares against, so a
// registry entry that lags these workflows makes the scanner report a downgrade as drift.
var releasePipelineActions = [...]string{
	"goreleaser/goreleaser-action",
	"anchore/sbom-action/download-syft",
	"sigstore/cosign-installer",
}

// maxReleaseFixtureActions bounds the fixture scans below (HISS-02).
const maxReleaseFixtureActions = 16

func isReleasePipelineAction(name string) bool {
	for i := 0; i < len(releasePipelineActions); i++ {
		if releasePipelineActions[i] == name {
			return true
		}
	}
	return false
}

// writeReleaseWorkflow writes one workflow that uses each action@version in uses and
// returns the repository root to scan.
func writeReleaseWorkflow(t *testing.T, uses []string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create workflow dir: %v", err)
	}
	var body strings.Builder
	body.WriteString("name: release\njobs:\n  release:\n    runs-on: ubuntu-latest\n    steps:\n")
	for i := 0; i < len(uses) && i < maxReleaseFixtureActions; i++ {
		body.WriteString("      - uses: " + uses[i] + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "release.yml"), []byte(body.String()), 0o600); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	return root
}

// Positive: every release-pipeline action the engine's own workflows use is pinned at the
// registry version, and each one is used at least once, so the check is not vacuous.
func TestReleasePipelinePinsMatchRegistry(t *testing.T) {
	actions, _, err := ScanWorkflowActions(t.Context(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("scan engine workflows: %v", err)
	}
	seen := make(map[string]int, len(releasePipelineActions))
	for _, a := range actions {
		if !isReleasePipelineAction(a.Action) {
			continue
		}
		seen[a.Action]++
		if a.CurrentVersion != a.LatestVersion {
			t.Errorf("%s pins %s@%s but the registry names %s", a.WorkflowFile, a.Action, a.CurrentVersion, a.LatestVersion)
		}
	}
	for _, name := range releasePipelineActions {
		if seen[name] == 0 {
			t.Errorf("no engine workflow uses %s; the release step moved and this check covers nothing", name)
		}
	}
}

// Negative: the pins the release workflows carried before the upgrade are reported as
// drift towards the registry version, never as current.
func TestReleasePipelineStalePinsReportDrift(t *testing.T) {
	stale := []string{
		"goreleaser/goreleaser-action@v6",
		"anchore/sbom-action/download-syft@v0.18.0",
		"sigstore/cosign-installer@v3.8.1",
	}
	actions, _, err := ScanWorkflowActions(t.Context(), writeReleaseWorkflow(t, stale))
	if err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	if len(actions) != len(stale) {
		t.Fatalf("scanned %d actions, want %d", len(actions), len(stale))
	}
	for _, a := range actions {
		if a.CurrentVersion == a.LatestVersion {
			t.Errorf("%s@%s: scan reported no drift, but this stale pre-upgrade pin should have resolved behind the registry latest", a.Action, a.CurrentVersion)
		}
		if a.LatestVersion != knownActionLatest[a.Action] {
			t.Errorf("%s latest %s, want registry %s", a.Action, a.LatestVersion, knownActionLatest[a.Action])
		}
	}
}

// Boundary: a workflow already at the registry pins reports no drift, and an action the
// registry does not know keeps its own version as the latest instead of a guessed one.
func TestReleasePipelineRegistryBoundaries(t *testing.T) {
	uses := make([]string, 0, len(releasePipelineActions)+1)
	for _, name := range releasePipelineActions {
		uses = append(uses, name+"@"+knownActionLatest[name])
	}
	uses = append(uses, "example/unregistered-action@v0.0.1")
	actions, _, err := ScanWorkflowActions(t.Context(), writeReleaseWorkflow(t, uses))
	if err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	if len(actions) != len(uses) {
		t.Fatalf("scanned %d actions, want %d", len(actions), len(uses))
	}
	for _, a := range actions {
		if a.CurrentVersion != a.LatestVersion {
			t.Errorf("%s@%s reported drift to %s", a.Action, a.CurrentVersion, a.LatestVersion)
		}
	}
}
