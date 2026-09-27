package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// lefthookRowClaim is the HISS-17 cell of a harness that credits praetor's lefthook.yml.
const lefthookRowClaim = "`praetorctl state sync .` in lefthook post-commit"

// rowFor returns the harness table row of one invariant.
func rowFor(t *testing.T, harness, id string) string {
	t.Helper()
	for _, row := range invariantRows(harness) {
		if strings.HasPrefix(row, "| **"+id+"** ") {
			return row
		}
	}
	t.Fatalf("harness has no %s row", id)
	return ""
}

func renderPipelineHarness(t *testing.T, pipelines hisscatalog.Pipeline, targets []agentcontext.VendorTarget) string {
	t.Helper()
	facts := adoptedFacts("acme", "widget", "framework", truthPlan)
	facts.pipelines, facts.targets, facts.hooks = pipelines, targets, hooksNone
	if pipelines&hisscatalog.PipelineLefthook != 0 {
		facts.hooks = hooksLefthook
	}
	harness, err := buildAgentHarness(facts)
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

// TestHarnessTableFollowsGeneratedPipelines: a row credits only the pipelines the run
// generates. Positive: lefthook alone keeps the hook stages. Negative: without praetor's
// lefthook.yml the hook-only rules read not enforced and rule 5 claims no hooks. Boundary:
// with no pipeline every row reads not enforced and the legend says so.
func TestHarnessTableFollowsGeneratedPipelines(t *testing.T) {
	hooksOnly := renderPipelineHarness(t, hisscatalog.PipelineLefthook, agentcontext.AllVendorTargets())
	if row := rowFor(t, hooksOnly, "HISS-16"); !strings.Contains(row, "`praetorctl compile-context --verify` in lefthook pre-commit | drift blocks commit |") ||
		strings.Contains(row, "verify-all") {
		t.Errorf("preserved verify-all still credited: %s", row)
	}
	if !strings.Contains(hooksOnly, "(praetor `lefthook.yml`)") {
		t.Error("legend does not name lefthook.yml alone")
	}

	makeOnly := renderPipelineHarness(t, hisscatalog.PipelineVerifyAll, agentcontext.AllVendorTargets())
	for _, id := range []string{"HISS-10", "HISS-17"} {
		if row := rowFor(t, makeOnly, id); !strings.HasSuffix(row, "| not enforced | advisory |") {
			t.Errorf("%s credited to a lefthook.yml praetor did not write: %s", id, row)
		}
	}
	if !strings.Contains(makeOnly, "Adoption installed no hooks here") || strings.Contains(makeOnly, "Hooks = local gate adoption installs") {
		t.Error("rule 5 claims hooks adoption did not install")
	}

	none := renderPipelineHarness(t, 0, agentcontext.AllVendorTargets())
	for _, row := range invariantRows(none) {
		if !strings.HasSuffix(row, "| not enforced | advisory |") {
			t.Errorf("row credited without any generated pipeline: %s", row)
		}
	}
	if !strings.Contains(none, "(none: no generated `make verify-all`, no praetor `lefthook.yml`)") {
		t.Error("legend does not say adoption generated no pipeline")
	}
}

// TestHarnessRule3FollowsAgentClients: rule 3 names the files compile-context writes under
// agent_clients. Positive: a subset names its files only. Boundary: an empty selection names
// none and still points updates at AGENTS.md.
func TestHarnessRule3FollowsAgentClients(t *testing.T) {
	selected, err := agentcontext.VendorTargets([]string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	harness := renderPipelineHarness(t, hisscatalog.AllPipelines, selected)
	if !strings.Contains(harness, "Never edit `CLAUDE.md` manually.") || strings.Contains(harness, "`.codex/rules.md`") {
		t.Errorf("rule 3 ignores agent_clients:\n%s", harness)
	}
	empty := renderPipelineHarness(t, hisscatalog.AllPipelines, nil)
	if !strings.Contains(empty, "`agent_clients` selects no compiled vendor file. All agent instruction updates -> `AGENTS.md`") {
		t.Error("rule 3 for an empty selection")
	}
}

// lefthookBinary is how a fixture's PATH provides lefthook.
type lefthookBinary uint8

const (
	// lefthookFails is newTestRepo's stub: on PATH, every call exits 1.
	lefthookFails lefthookBinary = iota
	// lefthookRuns answers `version` and installs a pre-commit hook, as real lefthook does.
	lefthookRuns
	// lefthookMissing leaves lefthook off PATH.
	lefthookMissing
)

// lefthookStubMarker is the line lefthookRuns writes into the hook it installs.
const lefthookStubMarker = "# lefthook stub"

// useLefthook replaces newTestRepo's failing lefthook stub on PATH with the given binary.
func useLefthook(t *testing.T, binary lefthookBinary) {
	t.Helper()
	stubDir := t.TempDir()
	switch binary {
	case lefthookRuns:
		writeStub(t, stubDir, "lefthook", "case \"$1\" in\nversion) echo 2.1.14 ;;\n"+
			"install) d=$(git rev-parse --git-path hooks) && mkdir -p \"$d\" && printf '#!/bin/sh\\n"+lefthookStubMarker+"\\n' > \"$d/pre-commit\" ;;\n"+
			"*) exit 1 ;;\nesac\n")
	case lefthookFails:
		writeStub(t, stubDir, "lefthook", "exit 1\n")
	}
	hermeticPath(t, stubDir)
}

// installedHook names the pre-commit hook adoption left: "lefthook", "fallback" or "none".
func installedHook(t *testing.T, repo string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, ".git", "hooks", preCommitHook))
	switch {
	case err != nil:
		return "none"
	case strings.Contains(string(data), lefthookStubMarker):
		return "lefthook"
	case strings.Contains(string(data), fallbackPreCommitMarker):
		return "fallback"
	}
	return "other"
}

// hookPredictionCase is one git-hooks fixture: the lefthook.yml present before adoption, the
// options, the lefthook binary on PATH, and what adoption must leave.
type hookPredictionCase struct {
	name, lefthook string
	force, skip    bool
	binary         lefthookBinary
	owned, claimed bool
	hook           string
}

// TestGeneratedPipelinesPredictGitHooks replays the harness's hook prediction against what the
// git-hooks step then does: the table credits praetor's hook jobs exactly when adoption leaves
// praetor's rendering in lefthook.yml and lefthook installs it. Positive: lefthook runs. Negative:
// lefthook fails or is missing, and the fallback hook runs instead. Boundary: skipped activation
// installs nothing, and a foreign configuration is never credited.
func TestGeneratedPipelinesPredictGitHooks(t *testing.T) {
	prior := string(readPriorLefthookFixtures(t)["root-go.lefthook.yml"])
	foreign := "pre-commit:\n  commands:\n    lint:\n      run: echo lint\n"
	superset := buildLefthookYAML() + "commit-msg:\n  commands:\n    conventional:\n      run: ./scripts/check-msg {1}\n"
	cases := []hookPredictionCase{
		{name: "absent", binary: lefthookRuns, owned: true, claimed: true, hook: "lefthook"},
		{name: "foreign kept", lefthook: foreign, binary: lefthookRuns, hook: "none"},
		{name: "foreign forced", lefthook: foreign, force: true, binary: lefthookRuns, owned: true, claimed: true, hook: "lefthook"},
		{name: "superset forced", lefthook: superset, force: true, binary: lefthookRuns, hook: "none"},
		{name: "canonical forced", lefthook: canonicalRootLefthook, force: true, binary: lefthookRuns, hook: "none"},
		{name: "prior migrated", lefthook: prior, binary: lefthookRuns, owned: true, claimed: true, hook: "lefthook"},
		{name: "lefthook fails", binary: lefthookFails, owned: true, hook: "fallback"},
		{name: "lefthook missing", binary: lefthookMissing, owned: true, hook: "fallback"},
		{name: "activation skipped", skip: true, binary: lefthookRuns, owned: true, hook: "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { replayHookPrediction(t, tc) })
	}
}

func replayHookPrediction(t *testing.T, tc hookPredictionCase) {
	t.Helper()
	repo := newTestRepo(t, "pipelines")
	useLefthook(t, tc.binary)
	if tc.lefthook != "" {
		mustWrite(t, filepath.Join(repo, lefthookFile), tc.lefthook)
	}
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, Force: tc.force, SkipHookActivation: tc.skip}
	if _, err := Adopt(context.Background(), opts); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	harness := mustRead(t, filepath.Join(repo, agentsFile))
	owned := isCurrentLefthookConfig([]byte(mustRead(t, filepath.Join(repo, lefthookFile))))
	claimed := strings.Contains(harness, lefthookRowClaim)
	if owned != tc.owned || claimed != tc.claimed {
		t.Fatalf("praetor rendering after adopt = %v, harness credits lefthook = %v; want %v, %v", owned, claimed, tc.owned, tc.claimed)
	}
	if hook := installedHook(t, repo); hook != tc.hook {
		t.Fatalf("installed pre-commit hook = %s, want %s", hook, tc.hook)
	}
	if inactive := strings.Contains(harness, "`lefthook` did not run at adoption"); inactive != (tc.owned && !tc.claimed) {
		t.Fatalf("rule 5 inactive-hook claim = %v for owned %v, claimed %v", inactive, tc.owned, tc.claimed)
	}
}

// TestAdoptHarnessDropsDeclinedAndPreservedPipelines: a declined makefile or git-hooks step,
// and a preserved custom verify-all, take their claims out of the adopted harness.
func TestAdoptHarnessDropsDeclinedAndPreservedPipelines(t *testing.T) {
	declined := newTestRepo(t, "declined-pipelines")
	mustWrite(t, filepath.Join(declined, manifestFile), "version: 1\nadoption:\n  decline:\n    - makefile\n    - git-hooks\n")
	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: declined, Profile: "framework"}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	harness := mustRead(t, filepath.Join(declined, agentsFile))
	for _, row := range invariantRows(harness) {
		if !strings.HasSuffix(row, "| not enforced | advisory |") {
			t.Errorf("declined pipelines still credited: %s", row)
		}
	}
	if !strings.Contains(harness, "`makefile` declined in `.standards.yaml`") || !strings.Contains(harness, "Adoption installed no hooks here") {
		t.Errorf("declined steps not stated:\n%s", harness)
	}

	preserved := newTestRepo(t, "preserved-pipeline")
	useLefthook(t, lefthookRuns)
	mustWrite(t, filepath.Join(preserved, "go.mod"), "module example.com/preserved\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(preserved, makefileName), ".PHONY: verify-all\nverify-all:\n\tgo vet ./...\n")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: preserved, Profile: "framework"})
	if err != nil || rep.Verification.Status != verificationPreserved {
		t.Fatalf("fixture precondition: custom verify-all must be preserved: %v %+v", err, rep.Verification)
	}
	if row := rowFor(t, mustRead(t, filepath.Join(preserved, agentsFile)), "HISS-16"); strings.Contains(row, "verify-all") ||
		!strings.Contains(row, "in lefthook pre-commit") {
		t.Errorf("preserved verify-all still credited: %s", row)
	}
}
