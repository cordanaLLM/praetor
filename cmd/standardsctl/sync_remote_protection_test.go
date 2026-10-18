package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
)

// remoteProtectionFixture is a sync fixture for acme/widgets with its origin on
// github.com/acme/widgets, and a stub forge serving it.
func remoteProtectionFixture(t *testing.T, stub *forgeStub) (*auditFixture, string) {
	t.Helper()
	f := newSyncValidationFixture(t)
	f.gitEnv = initGitFixture(t, f.dir)
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "remote", "add", "origin", "https://github.com/acme/widgets.git"); err != nil {
		t.Fatalf("remote add: %v (%s)", err, out)
	}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// stricterLiveRuleset is the praetor ruleset on GitHub as an operator tightened it: three
// approving reviews, last-push approval and signed commits, none of which the fixture declares.
func stricterLiveRuleset() map[string]any {
	return map[string]any{
		"id": 1, "name": "praetor-main-protection", "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/heads/main"}, "exclude": []any{}}},
		"rules": []any{
			map[string]any{"type": "required_signatures"},
			map[string]any{"type": "pull_request", "parameters": map[string]any{
				"required_approving_review_count": 3, "require_last_push_approval": true,
			}},
		},
	}
}

// sync --remote reads what the default branch enforces before and after it writes the ruleset,
// from rulesets and legacy branch protection alike, and names the mechanism (#154).
//
// Positive: an unprotected branch is reported as drifted on every declared property before the
// write, with no mechanism found, and as enforced by the praetor ruleset after it.
func TestSync_Remote_ReadsBackBranchProtection_Positive(t *testing.T) {
	stub := &forgeStub{}
	f, endpoint := remoteProtectionFixture(t, stub)
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	before, after, found := strings.Cut(out, "[SYNC] Reconciling branch protection ruleset")
	if !found {
		t.Fatalf("no ruleset reconciliation in:\n%s", out)
	}
	mustContain(t, before,
		"[INFO] Live branch protection of main on GitHub for acme/widgets, before this sync: no active ruleset rule applies to main and it has no branch protection object",
		"[DRIFT] Approving reviews: declared 1, live 0\n",
		"[DRIFT] Linear history: declared required, live not enforced\n")
	if strings.Contains(before, "Signed commits") {
		t.Errorf("a property declared off is not drift before the sync:\n%s", before)
	}
	const ruleset = `ruleset "praetor-main-protection" #1`
	mustContain(t, after,
		"read back: protected by "+ruleset,
		"[OK] Approving reviews: declared 1, live 1 by "+ruleset,
		"[OK] Linear history: declared required, live required by "+ruleset,
		"[OK] Signed commits: declared not required, live not enforced\n",
		"[OK] Remote branch protection synchronized on GitHub")
	if strings.Contains(after, "[DRIFT]") || strings.Contains(after, "[STRICTER]") {
		t.Errorf("a converged ruleset still reports drift:\n%s", after)
	}
}

// Negative: a forge that stores the ruleset but enforces none of its rules, such as a plan that
// ignores rulesets, fails the sync after the readback instead of reporting success; branch
// protection rules the token may not read fail it before any write.
func TestSync_Remote_ReadsBackBranchProtection_Negative(t *testing.T) {
	stub := &forgeStub{rulesUnenforced: true}
	f, endpoint := remoteProtectionFixture(t, stub)
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	mustErrContain(t, err, `ruleset "praetor-main-protection" converged, but main on GitHub still does not enforce the declared Pull requests, Approving reviews`)
	if strings.Contains(out, "[OK] Remote branch protection synchronized") {
		t.Fatalf("an unenforced ruleset was reported as synchronized:\n%s", out)
	}

	refused := &forgeStub{rulesStatus: http.StatusForbidden}
	f2, endpoint2 := remoteProtectionFixture(t, refused)
	_, err = runSyncCmd(t, "--config="+f2.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint2)
	mustErrContain(t, err, "read the live branch protection of main: read the active rules of main: unexpected status 403")
	if writes := refused.recorded(); len(writes) != 0 {
		t.Fatalf("an unreadable branch protection must stop the sync before any write, got %v", writes)
	}
}

// Boundary: a live ruleset stricter than declared takes the declared review settings, and the
// sync prints each one it lowered as [LOWERED]; a rule the policy does not render stays and reads
// back as [STRICTER], beside a legacy protection object. A default branch GitHub does not have
// yet is reconciled like any other: no legacy object can exist on it, and the ruleset applies.
func TestSync_Remote_ReadsBackBranchProtection_Boundary(t *testing.T) {
	stub := &forgeStub{
		rulesets: map[int]map[string]any{1: stricterLiveRuleset()},
		legacy:   map[string]any{"required_linear_history": map[string]any{"enabled": true}},
	}
	f, endpoint := remoteProtectionFixture(t, stub)
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	const ruleset = `ruleset "praetor-main-protection" #1`
	mustContain(t, out,
		"before this sync: protected by "+ruleset+", branch protection",
		`[LOWERED] Ruleset "praetor-main-protection" rule pull_request: required_approving_review_count was 3 on GitHub, now 1 as declared`,
		`[KEPT] Ruleset "praetor-main-protection" rule pull_request: require_last_push_approval stays true on GitHub, stricter than the declared false`,
		"[OK] Approving reviews: declared 1, live 1 by "+ruleset,
		"[STRICTER] Signed commits: declared not required, live required by "+ruleset,
		"[OK] Linear history: declared required, live required by "+ruleset+", branch protection",
		`Settings marked [STRICTER] exceed the declared policy. sync --remote lowers each parameter it renders into ruleset "praetor-main-protection"`)
	stored := stub.storedRuleset(t, 1)
	for _, want := range []string{`"required_approving_review_count":1`, `"require_last_push_approval":true`, `"required_signatures"`} {
		if !strings.Contains(stored, want) {
			t.Errorf("the stored ruleset lacks %s: %s", want, stored)
		}
	}

	unpushed := &forgeStub{branchMissing: true}
	f2, endpoint2 := remoteProtectionFixture(t, unpushed)
	out, err = runSyncCmd(t, "--config="+f2.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint2)
	if err != nil {
		t.Fatalf("remote sync of an unpushed default branch: %v\n%s", err, out)
	}
	mustContain(t, out,
		"before this sync: no active ruleset rule applies to main and it has no branch protection object (main does not exist on GitHub yet",
		"read back: protected by "+ruleset+" (main does not exist on GitHub yet; a ruleset that targets it applies once it is pushed)",
		"[OK] Remote branch protection synchronized on GitHub")
	if strings.Contains(out, "[LOWERED]") {
		t.Errorf("a newly created ruleset lowered nothing:\n%s", out)
	}
}

// Boundary: sync --remote with a declaration looser than live refuses to lower status checks
// and unrendered rules, preserving them without calling the live forge.
func TestSync_Remote_Boundary_LooserDeclarationRefusesToLower(t *testing.T) {
	stub := &forgeStub{
		rulesets: map[int]map[string]any{1: {
			"id": 1, "name": "praetor-main-protection", "target": "branch", "enforcement": "active",
			"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/heads/main"}, "exclude": []any{}}},
			"rules": []any{
				map[string]any{"type": "required_signatures"},
				map[string]any{"type": "required_status_checks", "parameters": map[string]any{
					"required_status_checks": []any{
						map[string]any{"context": "CI"},
						map[string]any{"context": "integration"},
					},
				}},
			},
		}},
	}
	f, endpoint := remoteProtectionFixture(t, stub)
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	stored := stub.storedRuleset(t, 1)
	if !strings.Contains(stored, `"context":"integration"`) {
		t.Fatalf("sync --remote lowered status checks by removing integration: %s", stored)
	}
	if !strings.Contains(stored, `"required_signatures"`) {
		t.Fatalf("sync --remote lowered rules by removing required_signatures: %s", stored)
	}
}

// plan --remote compares the live branch protection with the declared policy and only reads.
//
// Positive: after a sync the branch enforces every declared property, and plan writes nothing.
// Boundary: without --remote plan says it did not read the forge and sends no request.
func TestPlan_Remote_ComparesLiveBranchProtection_Positive(t *testing.T) {
	stub := &forgeStub{}
	f, endpoint := remoteProtectionFixture(t, stub)
	out, err := runPlanCmd(t, "--config="+f.manifestPath)
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	mustContain(t, out, adopt.PlanLiveNotCompared)
	if n := stub.requestCount(); n != 0 {
		t.Fatalf("plan without --remote sent %d requests", n)
	}
	if out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint); err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	writes := len(stub.recorded())
	out, err = runPlanCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	if err != nil {
		t.Fatalf("plan --remote: %v\n%s", err, out)
	}
	mustContain(t, out, "compared with the declared policy: protected by ruleset \"praetor-main-protection\" #1",
		"[OK] Ruleset enforcement: declared active, live active by ruleset \"praetor-main-protection\" #1",
		"Status: GitHub enforces every declared branch protection property.")
	if strings.Contains(out, adopt.PlanLiveNotCompared) {
		t.Fatalf("plan --remote says it did not compare the live branch protection:\n%s", out)
	}
	if got := len(stub.recorded()); got != writes {
		t.Fatalf("plan --remote wrote to the forge: %v", stub.recorded()[writes:])
	}
}

// Negative: drift is reported with the reconciling command while the preview exits zero; a
// missing token and a foreign origin are refused before any request.
func TestPlan_Remote_ComparesLiveBranchProtection_Negative(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	stub := &forgeStub{legacy: map[string]any{"required_signatures": map[string]any{"enabled": false}}}
	f, endpoint := remoteProtectionFixture(t, stub)
	out, err := runPlanCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	if err != nil {
		t.Fatalf("plan --remote with drift: %v\n%s", err, out)
	}
	mustContain(t, out, "compared with the declared policy: protected by branch protection",
		"[DRIFT] Pull requests: declared required, live not enforced\n",
		"[OK] Deletion blocked: declared required, live required by branch protection",
		"[DRIFT] GitHub does not enforce the declared Pull requests, Approving reviews, Code owner review, Dismiss stale reviews, Linear history on main.",
		"Action: Run 'praetorctl sync --remote' to reconcile branch protection on GitHub.")
	if writes := stub.recorded(); len(writes) != 0 {
		t.Fatalf("plan --remote wrote to the forge: %v", writes)
	}

	requests := stub.requestCount()
	if _, err := runPlanCmd(t, "--config="+f.manifestPath, "--remote", "--endpoint="+endpoint); !errors.Is(err, ErrRemoteTokenMissing) {
		t.Fatalf("expected ErrRemoteTokenMissing, got %v", err)
	}
	if out, gerr := runFixtureGit(t, f.dir, f.gitEnv, "remote", "set-url", "origin", "https://github.com/victim/prod.git"); gerr != nil {
		t.Fatalf("remote set-url: %v (%s)", gerr, out)
	}
	_, err = runPlanCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	mustErrContain(t, err, "foreign repository")
	if got := stub.requestCount(); got != requests {
		t.Fatalf("a refused plan --remote reached the forge with %d requests", got-requests)
	}
}

// Positive: sync --remote keeps live squash-only merges, extra approval and last-push approval
// when the declaration is looser, reporting each as [KEPT], and audit after sync passes.
func TestSync_Remote_Positive_KeepsStricterMergeMethodsAndExtraApproval(t *testing.T) {
	live := map[string]any{
		"id": 1, "name": "praetor-main-protection", "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/heads/main"}, "exclude": []any{}}},
		"rules": []any{
			map[string]any{"type": "pull_request", "parameters": map[string]any{
				"required_approving_review_count":                 1,
				"dismiss_stale_reviews_on_push":                   true,
				"require_code_owner_review":                       false,
				"require_last_push_approval":                      true,
				"required_review_thread_resolution":               true,
				"allowed_merge_methods":                           []any{"squash"},
				"require_extra_approval_for_unattributed_changes": true,
			}},
		},
	}
	stub := &forgeStub{rulesets: map[int]map[string]any{1: live}}
	f, endpoint := remoteProtectionFixture(t, stub)
	pointLiveChecksAt(t, endpoint)
	policy := config.DefaultPolicy()
	policy.BranchProtection.AllowedMergeMethods = []string{"merge", "squash", "rebase"}
	disabled := false
	policy.BranchProtection.RequireExtraApprovalForUnattributedChanges = &disabled
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+
		"overrides:\n  branch_protection:\n    allowed_merge_methods:\n      - merge\n      - squash\n      - rebase\n    require_extra_approval_for_unattributed_changes: false\n")
	if err := synthesizeRuleset(f.dir, "main", policy.BranchProtection, nil); err != nil {
		t.Fatal(err)
	}
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	mustContain(t, out,
		`[KEPT] Ruleset "praetor-main-protection" rule pull_request: allowed_merge_methods stays [squash] on GitHub, stricter than the declared [merge squash rebase]`,
		`[KEPT] Ruleset "praetor-main-protection" rule pull_request: require_extra_approval_for_unattributed_changes stays true on GitHub, stricter than the declared false`,
		`[KEPT] Ruleset "praetor-main-protection" rule pull_request: require_last_push_approval stays true on GitHub, stricter than the declared false`)
	stored := stub.storedRuleset(t, 1)
	for _, want := range []string{`"allowed_merge_methods":["squash"]`, `"require_extra_approval_for_unattributed_changes":true`, `"require_last_push_approval":true`} {
		if !strings.Contains(stored, want) {
			t.Errorf("stored ruleset lacks %s: %s", want, stored)
		}
	}
	auditOut, err := liveProtectionAudit(t, f, policy)
	if err != nil {
		t.Fatalf("audit after sync: %v\n%s", err, auditOut)
	}
	if strings.Contains(auditOut, "[FAIL]") || strings.Contains(auditOut, "[DRIFT]") {
		t.Errorf("audit after sync reported drift:\n%s", auditOut)
	}
}

// Positive: sync --remote with review_mode single_maintainer (0 reviews declared) against live 1
// lowers the review count and reports it as [LOWERED].
func TestSync_Remote_Positive_ReviewCountStillLowered(t *testing.T) {
	live := map[string]any{
		"id": 1, "name": "praetor-main-protection", "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/heads/main"}, "exclude": []any{}}},
		"rules": []any{
			map[string]any{"type": "pull_request", "parameters": map[string]any{
				"required_approving_review_count": 1,
			}},
		},
	}
	stub := &forgeStub{rulesets: map[int]map[string]any{1: live}}
	f, endpoint := remoteProtectionFixture(t, stub)
	policy := config.DefaultPolicy().BranchProtection
	policy.ReviewMode = config.BranchReviewModeSingleMaintainer
	policy.RequiredApprovingReviewers = 0
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+
		"overrides:\n  branch_protection:\n    review_mode: single_maintainer\n")
	if err := synthesizeRuleset(f.dir, "main", policy, nil); err != nil {
		t.Fatal(err)
	}
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	mustContain(t, out,
		`[LOWERED] Ruleset "praetor-main-protection" rule pull_request: required_approving_review_count was 1 on GitHub, now 0 as declared`)
	stored := stub.storedRuleset(t, 1)
	if !strings.Contains(stored, `"required_approving_review_count":0`) {
		t.Errorf("stored ruleset did not lower review count: %s", stored)
	}
}

// Positive: sync --remote with live merge+squash+rebase and declared squash-only tightens
// allowed_merge_methods to squash-only without a kept or lowered report.
func TestSync_Remote_Positive_LooserLiveMergeMethodsTightened(t *testing.T) {
	live := map[string]any{
		"id": 1, "name": "praetor-main-protection", "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/heads/main"}, "exclude": []any{}}},
		"rules": []any{
			map[string]any{"type": "pull_request", "parameters": map[string]any{
				"required_approving_review_count": 1,
				"allowed_merge_methods":           []any{"merge", "squash", "rebase"},
			}},
		},
	}
	stub := &forgeStub{rulesets: map[int]map[string]any{1: live}}
	f, endpoint := remoteProtectionFixture(t, stub)
	policy := config.DefaultPolicy().BranchProtection
	policy.AllowedMergeMethods = []string{"squash"}
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+
		"overrides:\n  branch_protection:\n    allowed_merge_methods:\n      - squash\n")
	if err := synthesizeRuleset(f.dir, "main", policy, nil); err != nil {
		t.Fatal(err)
	}
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	if strings.Contains(out, "[KEPT]") || strings.Contains(out, "[LOWERED]") {
		t.Errorf("tightening allowed_merge_methods must not report kept or lowered:\n%s", out)
	}
	stored := stub.storedRuleset(t, 1)
	if !strings.Contains(stored, `"allowed_merge_methods":["squash"]`) {
		t.Errorf("stored ruleset lacked tightened squash-only methods: %s", stored)
	}
}
