// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// backlogLayers resolves a fixture whose profile, fleet document and repository manifest each
// declare the given backlog section body (empty: none).
func backlogLayers(t *testing.T, profile, fleet, repository string) *EffectivePolicy {
	t.Helper()
	body := func(caps string) string {
		if caps == "" {
			return ""
		}
		return "backlog:\n  caps:\n" + caps
	}
	root := policyFixture(t, body(profile), "", body(repository))
	opts := EffectiveOptions{Root: root, Audit: true}
	if fleet != "" {
		opts.FleetPath = writePolicyFile(t, t.TempDir(), "fleet.yaml", body(fleet))
	}
	policy, err := LoadEffectivePolicyContext(t.Context(), opts)
	if err != nil {
		t.Fatalf("resolve backlog fixture: %v", err)
	}
	return policy
}

func assertBacklogCap(t *testing.T, policy *EffectivePolicy, want BacklogCap, maxBy, actionBy []string) {
	t.Helper()
	if got := policy.Policy.Backlog.Defects; got != want {
		t.Fatalf("defects cap = %+v, want %+v", got, want)
	}
	gotMax, gotAction := policy.BacklogSources(BacklogDefects)
	if !reflect.DeepEqual(gotMax, maxBy) || !reflect.DeepEqual(gotAction, actionBy) {
		t.Fatalf("defects sources = max %v action %v, want max %v action %v", gotMax, gotAction, maxBy, actionBy)
	}
	if err := policy.VerifyDigest(); err != nil {
		t.Fatalf("resolved policy does not verify: %v", err)
	}
}

// Positive: a fleet default, a profile override and a repository override each win in turn
// when each tightens the one before, and the effective policy names the layer that set it.
func TestBacklogCaps_Positive_FleetProfileRepositoryEachWinInOrder(t *testing.T) {
	fleet := "    defects: {max: 50, action: report}\n"
	profile := "    defects: {max: 40, action: batch}\n"
	repository := "    defects: {max: 30, action: gate}\n"

	assertBacklogCap(t, backlogLayers(t, "", fleet, ""), BacklogCap{Max: 50, Action: BacklogReport},
		[]string{"fleet"}, []string{"fleet"})
	assertBacklogCap(t, backlogLayers(t, profile, fleet, ""), BacklogCap{Max: 40, Action: BacklogBatch},
		[]string{"profile:framework"}, []string{"profile:framework"})
	policy := backlogLayers(t, profile, fleet, repository)
	assertBacklogCap(t, policy, BacklogCap{Max: 30, Action: BacklogGate}, []string{"repository"}, []string{"repository"})
	if evidence := policy.Evidence(); !strings.Contains(evidence, "backlog.caps.defects: max=30 (repository) action=gate (repository)") {
		t.Fatalf("evidence does not name the cap and its source:\n%s", evidence)
	}
}

// Negative: a cap only tightens, like every other policy key: a looser repository max or a
// weaker repository action does not override the fleet's.
func TestBacklogCaps_Negative_LooserLayerNeverLoosens(t *testing.T) {
	policy := backlogLayers(t, "", "    defects: {max: 30, action: gate}\n", "    defects: {max: 50, action: report}\n")
	assertBacklogCap(t, policy, BacklogCap{Max: 30, Action: BacklogGate}, []string{"fleet"}, []string{"fleet"})
}

// Boundary: an equal value from a second layer names both layers, and a category only one
// layer caps keeps that layer's action-less cap with the action reported as the default.
func TestBacklogCaps_Boundary_TieNamesEveryContributorAndActionDefaults(t *testing.T) {
	policy := backlogLayers(t, "", "    defects: {max: 40}\n    tasks: {max: 5}\n", "    defects: {max: 40}\n")
	assertBacklogCap(t, policy, BacklogCap{Max: 40}, []string{"fleet", "repository"}, nil)
	if got := policy.Policy.Backlog.Defects.EffectiveAction(); got != BacklogReport {
		t.Fatalf("undeclared action = %q, want report", got)
	}
	if !strings.Contains(policy.Evidence(), "action=report (builtin default)") {
		t.Fatalf("evidence must name the default action:\n%s", policy.Evidence())
	}
	if policy.Policy.Backlog.Questions.Declared() || policy.Policy.Backlog.Tasks.Limit() != 5 {
		t.Fatalf("undeclared category must stay unbounded: %+v", policy.Policy.Backlog)
	}
}

// Boundary: a policy that declares no cap has no backlog provenance, so its digest encoding
// is the one it had before the dimension existed.
func TestBacklogCaps_Boundary_NoCapLeavesNoTrace(t *testing.T) {
	policy := backlogLayers(t, "", "", "")
	if policy.BacklogFields != nil || policy.Policy.Backlog != (BacklogCaps{}) {
		t.Fatalf("an uncapped policy carries backlog state: %+v / %+v", policy.BacklogFields, policy.Policy.Backlog)
	}
	if strings.Contains(policy.Evidence(), "backlog") {
		t.Fatalf("evidence names a cap no layer declared:\n%s", policy.Evidence())
	}
}

// Negative: an unknown category, key or action, and a max that is not a positive integer, fail
// the manifest validation and name the key.
func TestBacklogCaps_Negative_ManifestValidationNamesTheKey(t *testing.T) {
	for name, tc := range map[string]struct{ caps, want string }{
		"unknown category": {"    bugs: {max: 3}\n", `unknown category "bugs"`},
		"unknown action":   {"    defects: {max: 3, action: block}\n", `backlog.caps.defects.action "block"`},
		"unknown key":      {"    tasks: {maxx: 3}\n", `unknown key "maxx"`},
		"zero max":         {"    questions: {max: 0}\n", "backlog.caps.questions.max"},
		"text max":         {"    questions: {max: ten}\n", "backlog.caps.questions.max"},
		"above bound":      {"    defects: {max: 10001}\n", "backlog.caps.defects.max must be an integer from 1 to 10000"},
		"empty category":   {"    defects: {}\n", "backlog.caps.defects declares neither max nor action"},
	} {
		manifest := "version: 1\nrepository:\n  owner: example\n  name: demo\nbacklog:\n  caps:\n" + tc.caps
		_, err := ParseManifest(".standards.yaml", []byte(manifest))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %s", name, err, tc.want)
		}
	}
	atBound := "version: 1\nrepository:\n  owner: example\n  name: demo\nbacklog:\n  caps:\n    defects: {max: 10000}\n"
	if _, err := ParseManifest(".standards.yaml", []byte(atBound)); err != nil {
		t.Fatalf("a max at the bound must decode: %v", err)
	}
	if _, err := ParseManifest(".standards.yaml", []byte("version: 1\nbacklog:\n  limits: {}\n")); err == nil ||
		!strings.Contains(err.Error(), `unknown key "limits"`) {
		t.Fatalf("an unknown backlog section key must fail: %v", err)
	}
}

// Negative: the same decoder holds the fleet document and the catalog file to the same rules.
func TestBacklogCaps_Negative_FleetAndProfileUseTheSameDecoder(t *testing.T) {
	root := policyFixture(t, "", "", "")
	fleet := writePolicyFile(t, t.TempDir(), "fleet.yaml", "backlog:\n  caps:\n    alerts: {max: 3}\n")
	_, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, FleetPath: fleet})
	if err == nil || !strings.Contains(err.Error(), `fleet policy: backlog.caps: unknown category "alerts"`) {
		t.Fatalf("fleet document with an unknown category: %v", err)
	}
	profile := policyFixture(t, "backlog:\n  caps:\n    defects: {max: 2, action: hold}\n", "", "")
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: profile}); err == nil ||
		!strings.Contains(err.Error(), "backlog.caps.defects.action") {
		t.Fatalf("profile with an unknown action: %v", err)
	}
}

// Positive: an external document carrying only a backlog section is an owned document.
func TestBacklogCaps_Positive_BacklogAloneIsAnOwnedFleetSection(t *testing.T) {
	policy := backlogLayers(t, "", "    questions: {max: 9, action: batch}\n", "")
	if policy.Policy.Backlog.Questions != (BacklogCap{Max: 9, Action: BacklogBatch}) {
		t.Fatalf("fleet questions cap = %+v", policy.Policy.Backlog.Questions)
	}
}

// Negative: an action no layer gives a max to act on fails the resolution instead of reading
// as configured while bounding nothing.
func TestBacklogCaps_Negative_ActionWithoutMaxFails(t *testing.T) {
	root := policyFixture(t, "", "", "backlog:\n  caps:\n    tasks: {action: gate}\n")
	_, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if err == nil || !strings.Contains(err.Error(), "backlog.caps.tasks.action is set by repository, but no layer sets backlog.caps.tasks.max") {
		t.Fatalf("action without max: %v", err)
	}
	withMax := backlogLayers(t, "    tasks: {max: 4}\n", "", "    tasks: {action: gate}\n")
	if withMax.Policy.Backlog.Tasks != (BacklogCap{Max: 4, Action: BacklogGate}) {
		t.Fatalf("an action may tighten a max another layer set: %+v", withMax.Policy.Backlog.Tasks)
	}
}

// Negative: a retained snapshot whose backlog provenance was edited no longer verifies.
func TestBacklogCaps_Negative_TamperedProvenanceFailsVerification(t *testing.T) {
	policy := backlogLayers(t, "", "    defects: {max: 10}\n", "")
	policy.BacklogFields[backlogField(BacklogDefects, "max")] = []string{"repository-not-a-source"}
	if err := policy.VerifyDigest(); err == nil {
		t.Fatal("a contributor without a source verified")
	}
	stray := backlogLayers(t, "", "    defects: {max: 10}\n", "")
	stray.BacklogFields[backlogField(BacklogTasks, "max")] = []string{"fleet"}
	if err := stray.VerifyDigest(); err == nil {
		t.Fatal("provenance for an undeclared cap verified")
	}
}

// Negative and boundary: ResolvePolicy callers are held to the decoder's bounds.
func TestBacklogCaps_Negative_ResolvePolicyRejectsInvalidLayerCaps(t *testing.T) {
	layer := policyTestLayer("fleet", 60)
	layer.Backlog.Defects = BacklogCap{Max: -1}
	if _, err := ResolvePolicy(t.Context(), []PolicyLayer{layer}); err == nil {
		t.Fatal("negative max accepted")
	}
	layer.Backlog.Defects = BacklogCap{Max: 1, Action: "never"}
	if _, err := ResolvePolicy(t.Context(), []PolicyLayer{layer}); err == nil {
		t.Fatal("unknown action accepted")
	}
	layer.Backlog.Defects = BacklogCap{Max: MaxBacklogCap, Action: BacklogGate}
	if _, err := ResolvePolicy(t.Context(), []PolicyLayer{layer}); err != nil {
		t.Fatalf("the largest max must resolve: %v", err)
	}
	layer.Backlog.Defects = BacklogCap{Max: MaxBacklogCap + 1, Action: BacklogGate}
	if _, err := ResolvePolicy(t.Context(), []PolicyLayer{layer}); err == nil {
		t.Fatal("a max above the count bound resolved")
	}
}

// The inclusive cap: a count equal to max is not over it, one more is.
func TestBacklogCap_Boundary_InclusiveMax(t *testing.T) {
	limit := BacklogCap{Max: 80}
	if limit.Over(79) || limit.Over(80) || !limit.Over(81) {
		t.Fatalf("cap of 80: over(79)=%t over(80)=%t over(81)=%t", limit.Over(79), limit.Over(80), limit.Over(81))
	}
	if (BacklogCap{}).Over(1000) {
		t.Fatal("an undeclared cap bounds nothing")
	}
	if !BacklogGate.Includes(BacklogBatch) || BacklogReport.Includes(BacklogBatch) || !BacklogBatch.Includes(BacklogReport) {
		t.Fatal("action strictness: gate includes batch, batch includes report, report includes nothing stricter")
	}
}

// LoadUnadoptedEffectivePolicyContext: no manifest is no policy, a manifest without a lock
// resolves the repository's caps, and a lock is resolved as the audit resolves it.
func TestLoadUnadoptedEffectivePolicy_PositiveNegativeBoundary(t *testing.T) {
	policy, notice, err := LoadUnadoptedEffectivePolicyContext(t.Context(), EffectiveOptions{Root: t.TempDir()})
	if err != nil || policy != nil || !strings.Contains(notice, "no .standards.yaml") {
		t.Fatalf("no manifest: %+v %q %v", policy, notice, err)
	}
	unadopted := lockLessManifest(t, "backlog:\n  caps:\n    defects: {max: 3, action: gate}\n")
	policy, notice, err = LoadUnadoptedEffectivePolicyContext(t.Context(), EffectiveOptions{Root: unadopted})
	if err != nil || notice != NoLockNotice || policy.Policy.Backlog.Defects != (BacklogCap{Max: 3, Action: BacklogGate}) {
		t.Fatalf("lock-less manifest: %+v %q %v", policy, notice, err)
	}
	adopted := policyFixture(t, "", "", "backlog:\n  caps:\n    defects: {max: 3}\n")
	policy, notice, err = LoadUnadoptedEffectivePolicyContext(t.Context(), EffectiveOptions{Root: adopted})
	if err != nil || notice != "" || policy.Policy.Backlog.Defects.Limit() != 3 {
		t.Fatalf("adopted manifest: %+v %q %v", policy, notice, err)
	}
	writePolicyFile(t, adopted, LockFileName, "version: 1\n")
	if _, _, err := LoadUnadoptedEffectivePolicyContext(t.Context(), EffectiveOptions{Root: adopted}); err == nil {
		t.Fatal("an invalid lock must fail, not be skipped")
	}
	if _, _, err := LoadUnadoptedEffectivePolicyContext(t.Context(), EffectiveOptions{Root: filepath.Join(adopted, "absent")}); err != nil {
		t.Fatalf("an absent root has no manifest and no policy: %v", err)
	}
}

// The no-lock notice names every external document that still applies without a lock; with
// none it is NoLockNotice.
func TestLoadUnadoptedEffectivePolicy_NoLockNoticeNamesExternalLayers(t *testing.T) {
	root := lockLessManifest(t, "")
	external := t.TempDir()
	opts := EffectiveOptions{Root: root,
		FleetPath:       writePolicyFile(t, external, "fleet.yaml", "backlog:\n  caps:\n    defects: {max: 2}\n"),
		WorkstationPath: writePolicyFile(t, external, "workstation.yaml", "complexity:\n  max_func_loc: 70\n")}
	policy, notice, err := LoadUnadoptedEffectivePolicyContext(t.Context(), opts)
	want := "no .standards.lock: built-in defaults, repository overrides and the fleet, workstation policy only; no pinned profile or facet applies"
	if err != nil || notice != want || policy.Policy.Backlog.Defects.Limit() != 2 {
		t.Fatalf("external layers without a lock: %q %v", notice, err)
	}
	if _, notice, err = LoadUnadoptedEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root}); err != nil || notice != NoLockNotice {
		t.Fatalf("no external layer: %q %v", notice, err)
	}
}

// Positive: a backlog section in the manifest, an external document or a selected catalog
// profile is declared, the profile even when the lock no longer verifies.
func TestDeclaresBacklog_Positive_EveryLayerKind(t *testing.T) {
	section := "backlog:\n  caps:\n    defects: {max: 2}\n"
	manifest := lockLessManifest(t, section)
	fleet := EffectiveOptions{Root: lockLessManifest(t, ""), FleetPath: writePolicyFile(t, t.TempDir(), "fleet.yaml", section)}
	profile := policyFixture(t, section, "", "")
	writePolicyFile(t, profile, LockFileName, "version: 1\n")
	for name, opts := range map[string]EffectiveOptions{
		"manifest": {Root: manifest}, "fleet": fleet, "profile with a stale lock": {Root: profile},
	} {
		if declared, err := DeclaresBacklogContext(t.Context(), opts); err != nil || !declared {
			t.Errorf("%s: declared=%t err=%v", name, declared, err)
		}
	}
}

// Negative: no layer with a backlog section declares nothing even when the lock is invalid; a
// selected profile the catalog does not hold cannot be read and is an error.
func TestDeclaresBacklog_Negative_NothingDeclaredOrUnreadable(t *testing.T) {
	plain := policyFixture(t, "", "", "")
	writePolicyFile(t, plain, LockFileName, "version: 1\n")
	if declared, err := DeclaresBacklogContext(t.Context(), EffectiveOptions{Root: plain}); err != nil || declared {
		t.Fatalf("no backlog section: declared=%t err=%v", declared, err)
	}
	if err := os.Remove(filepath.Join(plain, ".config", "archetypes", "framework.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := DeclaresBacklogContext(t.Context(), EffectiveOptions{Root: plain}); err == nil ||
		!strings.Contains(err.Error(), `profile "framework" is not in the catalog`) {
		t.Fatalf("unreadable profile: %v", err)
	}
	if _, err := DeclaresBacklogContext(t.Context(), EffectiveOptions{Root: lockLessManifest(t, ""), FleetPath: filepath.Join(t.TempDir(), "absent.yaml")}); err == nil {
		t.Fatal("an absent fleet document declared nothing instead of failing")
	}
}

// Boundary: a root without a manifest declares nothing, and without a lock the selected
// profiles are not read, as resolution does not read them either.
func TestDeclaresBacklog_Boundary_NoManifestOrNoLock(t *testing.T) {
	if declared, err := DeclaresBacklogContext(t.Context(), EffectiveOptions{Root: t.TempDir()}); err != nil || declared {
		t.Fatalf("no manifest: declared=%t err=%v", declared, err)
	}
	unpinned := lockLessManifest(t, "profiles: [absent-profile]\n")
	if declared, err := DeclaresBacklogContext(t.Context(), EffectiveOptions{Root: unpinned}); err != nil || declared {
		t.Fatalf("no lock: declared=%t err=%v", declared, err)
	}
	if _, err := DeclaresBacklogContext(t.Context(), EffectiveOptions{}); err == nil {
		t.Fatal("an empty root was probed")
	}
}
