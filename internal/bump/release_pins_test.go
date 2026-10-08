// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/supplychain"
	"github.com/cordanaLLM/praetor/internal/util"
)

// releasePipelineActions are the signing, SBOM, image and chart actions the engine's
// release workflows install. knownActionLatest is the baseline the workflow scanner compares
// against, so a registry entry that lags these workflows makes the scanner report a
// downgrade as drift.
var releasePipelineActions = [...]string{
	"goreleaser/goreleaser-action",
	"anchore/sbom-action/download-syft",
	"sigstore/cosign-installer",
	"docker/setup-buildx-action",
	"docker/login-action",
	"azure/setup-helm",
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
		"docker/setup-buildx-action@v3",
		"docker/login-action@v3",
		"azure/setup-helm@v4",
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

// The REUSE action's registry tag is the pin the emitted REUSE gate runs
// (supplychain.ReuseActionVersion). Positive: the registry reads it, and every engine workflow
// running the action pins it there, at least one doing so. Negative: an older tag reports drift
// towards the pin. Boundary: the pinned tag reports none.
func TestReuseActionPinMatchesRegistryAndEngine(t *testing.T) {
	if got := knownActionLatest[supplychain.ReuseAction]; got != supplychain.ReuseActionVersion {
		t.Fatalf("registry names %s@%s, pin is %s", supplychain.ReuseAction, got, supplychain.ReuseActionVersion)
	}
	actions, _, err := ScanWorkflowActions(t.Context(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("scan engine workflows: %v", err)
	}
	assertEngineRunsReusePin(t, actions)
	fixture, _, err := ScanWorkflowActions(t.Context(), writeReleaseWorkflow(t, []string{supplychain.ReuseAction + "@v5", supplychain.ReuseActionRef()}))
	if err != nil || len(fixture) != 2 {
		t.Fatalf("scan fixture: %v (%d actions)", err, len(fixture))
	}
	if fixture[0].CurrentVersion == fixture[0].LatestVersion || fixture[1].CurrentVersion != fixture[1].LatestVersion {
		t.Errorf("want drift for v5 only: %+v", fixture)
	}
}

func assertEngineRunsReusePin(t *testing.T, actions []ActionCandidate) {
	t.Helper()
	engine := 0
	for _, a := range actions {
		if a.Action == supplychain.ReuseAction {
			engine++
			if a.CurrentVersion != supplychain.ReuseActionVersion {
				t.Errorf("%s pins %s@%s, not the REUSE pin %s", a.WorkflowFile, a.Action, a.CurrentVersion, supplychain.ReuseActionVersion)
			}
		}
	}
	if engine == 0 {
		t.Error("no engine workflow runs the REUSE action; praetor's own CI must run reuse lint")
	}
}

// Positive: ReuseActionPinnedRef combines the registry tag with ReuseActionCommit, and
// VerifyActionPins confirms PinVerified when the upstream tag resolves to that commit.
// Negative: when ReuseActionVersion and ReuseActionCommit disagree with the registry tag,
// VerifyActionPins refuses the pin as a release mismatch and clears UpToDate.
func TestReuseActionPinnedRefPairsRegistryTagAndCommit(t *testing.T) {
	assertPinnedRefStructure(t)
	workflow, _, err := ScanWorkflowActions(t.Context(), writeReleaseWorkflow(t, []string{supplychain.ReuseActionPinnedRef()}))
	if err != nil {
		t.Fatalf("scan pinned workflow: %v", err)
	}
	if len(workflow) != 1 {
		t.Fatalf("scanned %d actions, want 1", len(workflow))
	}
	assertMatchingPinVerifies(t, workflow)
	assertMismatchedPinRefused(t, workflow)
}

func assertPinnedRefStructure(t *testing.T) {
	t.Helper()
	pin, ok := util.ParseSHAPin(supplychain.ReuseActionPinnedRef())
	if !ok {
		t.Fatalf("ReuseActionPinnedRef %q is not a valid SHA-pinned action", supplychain.ReuseActionPinnedRef())
	}
	if pin.Action != supplychain.ReuseAction {
		t.Errorf("action = %s, want %s", pin.Action, supplychain.ReuseAction)
	}
	if pin.SHA != supplychain.ReuseActionCommit {
		t.Errorf("SHA = %s, want %s", pin.SHA, supplychain.ReuseActionCommit)
	}
	if pin.Release != supplychain.ReuseActionVersion {
		t.Errorf("release = %s, want %s", pin.Release, supplychain.ReuseActionVersion)
	}
	if pin.Release != knownActionLatest[supplychain.ReuseAction] {
		t.Fatalf("pinned release %s disagrees with registry %s", pin.Release, knownActionLatest[supplychain.ReuseAction])
	}
}

func assertMatchingPinVerifies(t *testing.T, workflow []ActionCandidate) {
	t.Helper()
	upstream := pinUpstream{
		tags: map[string]string{
			supplychain.ReuseAction + " " + supplychain.ReuseActionVersion: supplychain.ReuseActionCommit,
		},
	}
	lookup := &fakePinLookup{upstream: upstream}
	verified, findings := VerifyActionPins(t.Context(), workflow, lookup)
	if len(findings) != 0 {
		t.Errorf("unexpected findings: %+v", findings)
	}
	if verified[0].Pin != PinVerified {
		t.Errorf("Pin = %v, want PinVerified", verified[0].Pin)
	}
	if !verified[0].UpToDate {
		t.Error("want UpToDate true")
	}
}

func assertMismatchedPinRefused(t *testing.T, workflow []ActionCandidate) {
	t.Helper()
	staleUpstream := pinUpstream{
		tags: map[string]string{
			supplychain.ReuseAction + " " + supplychain.ReuseActionVersion: "1111111111111111111111111111111111111111",
		},
		extra: map[string]bool{
			supplychain.ReuseAction + " " + supplychain.ReuseActionCommit: true,
		},
	}
	staleLookup := &fakePinLookup{upstream: staleUpstream}
	mismatched, mismatchFindings := VerifyActionPins(t.Context(), workflow, staleLookup)
	if len(mismatchFindings) == 0 {
		t.Error("want mismatch findings, got none")
	}
	if mismatched[0].Pin != PinReleaseMismatch {
		t.Errorf("Pin = %v, want PinReleaseMismatch", mismatched[0].Pin)
	}
	if mismatched[0].UpToDate {
		t.Error("want UpToDate false for mismatched pin")
	}
}
