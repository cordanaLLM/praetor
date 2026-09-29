package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
// ignores rulesets, fails the sync after the readback instead of reporting success; a legacy
// protection object the token may not read fails it before any write.
func TestSync_Remote_ReadsBackBranchProtection_Negative(t *testing.T) {
	stub := &forgeStub{rulesUnenforced: true}
	f, endpoint := remoteProtectionFixture(t, stub)
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint)
	mustErrContain(t, err, `ruleset "praetor-main-protection" converged, but main on GitHub still does not enforce the declared Pull requests, Approving reviews`)
	if strings.Contains(out, "[OK] Remote branch protection synchronized") {
		t.Fatalf("an unenforced ruleset was reported as synchronized:\n%s", out)
	}

	refused := &forgeStub{legacyStatus: http.StatusForbidden}
	f2, endpoint2 := remoteProtectionFixture(t, refused)
	_, err = runSyncCmd(t, "--config="+f2.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint2)
	mustErrContain(t, err, "read the live branch protection of main: read the legacy branch protection of main: unexpected status 403")
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
		`[LOWERED] Ruleset "praetor-main-protection" rule pull_request: require_last_push_approval was true on GitHub, now false as declared`,
		"[OK] Approving reviews: declared 1, live 1 by "+ruleset,
		"[STRICTER] Signed commits: declared not required, live required by "+ruleset,
		"[OK] Linear history: declared required, live required by "+ruleset+", branch protection",
		`Settings marked [STRICTER] exceed the declared policy. sync --remote lowers each parameter it renders into ruleset "praetor-main-protection"`)
	stored := stub.storedRuleset(t, 1)
	for _, want := range []string{`"required_approving_review_count":1`, `"require_last_push_approval":false`, `"required_signatures"`} {
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
	mustContain(t, out, "[INFO] Live branch protection not read: pass --remote")
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
		"Status: GitHub enforces every declared branch protection property.")
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
