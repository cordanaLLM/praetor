// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// actionsStub is a stand-in forge for the live Actions checks: the workflow permissions of
// acme/widgets and of the acme organisation, and one runs page per workflow file name.
type actionsStub struct {
	repoStatus, orgStatus int
	repo, org             map[string]any
	runs                  map[string]map[string]any
	mu                    sync.Mutex
	requests              []string
}

// serveActions points the live Actions checks at stub with a token, for the rest of the test.
func serveActions(t *testing.T, stub *actionsStub) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.requests = append(stub.requests, r.Method+" "+r.URL.EscapedPath())
		stub.mu.Unlock()
		status, body := stub.answer(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode stub answer: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	pointLiveChecksAt(t, server.URL)
}

// pointLiveChecksAt points the live forge checks of audit and plan at the forge serving url,
// with a token, for the rest of the test.
func pointLiveChecksAt(t *testing.T, url string) {
	t.Helper()
	endpoint, token := actionsForgeEndpoint, resolveActionsToken
	actionsForgeEndpoint = url
	resolveActionsToken = func(context.Context) string { return "stub-token" }
	t.Cleanup(func() { actionsForgeEndpoint, resolveActionsToken = endpoint, token })
}

func (s *actionsStub) answer(path string) (int, any) {
	switch {
	case path == "/repos/acme/widgets/actions/permissions/workflow":
		return orOK(s.repoStatus), s.repo
	case path == "/orgs/acme/actions/permissions/workflow":
		return orOK(s.orgStatus), s.org
	case strings.HasPrefix(path, "/repos/acme/widgets/actions/workflows/"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/repos/acme/widgets/actions/workflows/"), "/runs")
		if body, ok := s.runs[name]; ok {
			return http.StatusOK, body
		}
		return http.StatusNotFound, map[string]any{"message": "Not Found"}
	default:
		return http.StatusTeapot, nil
	}
}

func (s *actionsStub) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func orOK(status int) int {
	if status == 0 {
		return http.StatusOK
	}
	return status
}

func workflowPermissions(permissions string, approve bool) map[string]any {
	return map[string]any{"default_workflow_permissions": permissions, "can_approve_pull_request_reviews": approve}
}

// declareActions rewrites the fixture manifest with an overrides.actions block, gives the
// checkout an origin remote naming acme/widgets on github.com, and commits both.
func (f *auditFixture) declareActions(t *testing.T, permissions string, approve bool) {
	t.Helper()
	block := "overrides:\n  actions:\n    default_workflow_permissions: " + permissions +
		"\n    allow_create_and_approve_pull_requests: " + strconv.FormatBool(approve) + "\n"
	manifest := strings.Replace(fixtureManifest("acme", "widgets", false), "register:\n", block+"register:\n", 1)
	writeFixtureFile(t, f.dir, ".standards.yaml", manifest)
	f.addGitHubOrigin(t)
	gitCommitAll(t, f.dir, f.gitEnv, "declare actions policy")
}

func (f *auditFixture) addGitHubOrigin(t *testing.T) {
	t.Helper()
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "remote", "add", "origin", "https://github.com/acme/widgets.git"); err != nil {
		t.Fatalf("add origin: %v (%s)", err, out)
	}
}

// Positive (#611): a repository whose live workflow permissions match its declaration audits as
// compliant, and the report says the comparison ran; the run check reads the forge too.
func TestAuditLiveActions_Positive_CompliantRepositoryPasses(t *testing.T) {
	f := newAuditFixture(t)
	f.declareActions(t, "read", false)
	stub := &actionsStub{repo: workflowPermissions("read", false), org: workflowPermissions("read", true)}
	serveActions(t, stub)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("a compliant repository failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out,
		"[PASS] Actions workflow permissions compared with the forge: live workflow permissions match the declaration.",
		"[INFO] Workflow runs on main: no workflow under .github/workflows that runs on its own; nothing read.",
		"Audit Summary: configured governance gates passed")
	if got := strings.Join(stub.seen(), "\n"); !strings.Contains(got, "GET /repos/acme/widgets/actions/permissions/workflow") ||
		!strings.Contains(got, "GET /orgs/acme/actions/permissions/workflow") {
		t.Fatalf("requests = %s", got)
	}
}

// Negative (#611): a declared false with a live true inherited from the organisation fails as
// drifted-by-organisation; a declared true the organisation denies fails as
// blocked-by-organisation and names the organisation.
func TestAuditLiveActions_Negative_DriftFailsTheAudit(t *testing.T) {
	f := newAuditFixture(t)
	f.declareActions(t, "read", false)
	serveActions(t, &actionsStub{repo: workflowPermissions("read", true), org: workflowPermissions("read", true)})
	out, err := f.audit(t)
	mustErrContain(t, err, "[FAIL] Actions workflow permissions drifted-by-organisation")
	if strings.Contains(out, "Audit Summary: configured governance gates passed") {
		t.Fatalf("a drifted repository reported a passing audit:\n%s", out)
	}

	blocked := newAuditFixture(t)
	blocked.declareActions(t, "read", true)
	serveActions(t, &actionsStub{repo: workflowPermissions("read", false), org: workflowPermissions("read", false)})
	_, err = blocked.audit(t)
	mustErrContain(t, err, "[FAIL] Actions workflow permissions blocked-by-organisation")
	mustErrContain(t, err, "must be changed at the organisation")
}

// Boundary (#611): without a token, with --offline, without an origin naming the repository,
// or with a token the forge refuses, the audit reports the comparison as not made and passes;
// a repository that declares no actions policy is not failed and asks nothing about it.
func TestAuditLiveActions_Boundary_ChecksNotMadeAreNamed(t *testing.T) {
	f := newAuditFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("an undeclared repository failed: %v\n%s", err, out)
	}
	mustContain(t, out, "[INFO] Actions workflow permissions: overrides.actions declares none; nothing compared with the forge.",
		"[SKIP] Workflow runs not read from the forge: cannot verify manifest repository acme/widgets")

	f.declareActions(t, "read", false)
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("a check without a token failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out, "[SKIP] Actions workflow permissions not compared with the forge: no forge token")

	stub := &actionsStub{repoStatus: http.StatusForbidden, repo: map[string]any{"message": "Resource not accessible"}}
	serveActions(t, stub)
	out, err = f.audit(t, "--offline")
	if err != nil || len(stub.seen()) != 0 {
		t.Fatalf("--offline failed or asked the forge (%v): %v\n%s", stub.seen(), err, out)
	}
	mustContain(t, out, "[SKIP] Actions workflow permissions not compared with the forge: --offline",
		"[SKIP] Workflow runs not read from the forge: --offline")

	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("a refused read failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out, "[SKIP] Actions workflow permissions not compared with the forge: read repository workflow permissions: status 403")
	if strings.Contains(out, "[PASS] Actions workflow permissions") {
		t.Fatalf("a refused read was reported as a pass:\n%s", out)
	}
}

// Positive, negative and boundary (#612) through the audit: each workflow's runs on main are
// read and reported, a failing one is warned about, and the audit still passes, since run
// health is reported and not enforced.
func TestAuditLiveActions_WorkflowRunsAreReportedNotEnforced(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".github/workflows/nightly.yml",
		"on:\n  schedule:\n    - cron: '0 2 * * *'\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make\n")
	f.addGitHubOrigin(t)
	gitCommitAll(t, f.dir, f.gitEnv, "add a scheduled workflow")
	run := func(conclusion string, number int) map[string]any {
		return map[string]any{"run_number": number, "event": "schedule", "head_branch": "main", "status": "completed",
			"conclusion": conclusion, "created_at": "2026-09-27T02:20:46Z", "html_url": "https://forge.test/r"}
	}
	serveActions(t, &actionsStub{runs: map[string]map[string]any{"nightly.yml": {"total_count": 4,
		"workflow_runs": []any{run("failure", 4), run("failure", 3), run("failure", 2), run("success", 1)}}}})
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("a failing workflow failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out,
		"[WARN] Workflow nightly.yml: failing on main, failed runs in a row: 3 (latest: run #4, schedule, failure",
		"[WARN] Workflow runs on main read from the forge: 1 of 1 workflows failing or never run (nightly.yml); reported, not enforced.",
		"Audit Summary: configured governance gates passed")
}

// Plan (#611): plan previews the permission verdict and never fails on it; --offline and an
// undeclared policy say what was not compared.
func TestPlanActionsPermissions_ReportsWithoutFailing(t *testing.T) {
	f := newAuditFixture(t)
	out, err := runPlanCmd(t, "--config="+f.manifestPath)
	if err != nil {
		t.Fatalf("plan failed: %v\n%s", err, out)
	}
	mustContain(t, out, "Live Actions workflow permissions (read-only; sync does not change them):",
		"  - overrides.actions declares none; nothing compared")

	f.declareActions(t, "read", false)
	serveActions(t, &actionsStub{repo: workflowPermissions("write", false), org: workflowPermissions("read", false)})
	out, err = runPlanCmd(t, "--config="+f.manifestPath)
	if err != nil {
		t.Fatalf("plan failed on drift: %v\n%s", err, out)
	}
	mustContain(t, out, "  - drifted-at-repository: live value {permissions=write, create/approve PRs=false}")

	out, err = runPlanCmd(t, "--config="+f.manifestPath, "--offline")
	if err != nil {
		t.Fatalf("plan --offline failed: %v\n%s", err, out)
	}
	mustContain(t, out, "  - not compared: --offline")
}

// ciWorkflow is a workflow whose job CI reports on every pull request, so the declared policy
// requires the status check CI.
const ciWorkflow = "on:\n  pull_request:\njobs:\n  ci:\n    name: CI\n    runs-on: ubuntu-latest\n    steps:\n      - run: make\n"

// declaredProtection is the branch protection the fixture manifest declares, signed commits
// included when signed.
func declaredProtection(signed bool) config.BranchProtectionPolicy {
	policy := config.DefaultPolicy().BranchProtection
	policy.RequireSignedCommits = signed
	return policy
}

// protectedFixture is an audit fixture for acme/widgets whose manifest declares signed commits
// when signed and declines decline, with an origin on github.com, the workflow CI and the
// committed ruleset for both; the live forge checks read stub.
func protectedFixture(t *testing.T, signed bool, decline string, stub *forgeStub) *auditFixture {
	t.Helper()
	f := newAuditFixture(t)
	manifest := fixtureManifest("acme", "widgets", signed)
	if decline != "" {
		manifest = strings.Replace(manifest, "register:\n", "adoption:\n  decline:\n    - "+decline+"\nregister:\n", 1)
	}
	writeFixtureFile(t, f.dir, ".standards.yaml", manifest)
	writeFixtureFile(t, f.dir, ".github/workflows/ci.yml", ciWorkflow)
	writeDeclaredRuleset(t, f.dir, declaredProtection(signed))
	f.addGitHubOrigin(t)
	gitCommitAll(t, f.dir, f.gitEnv, "declare the CI check")
	server := httptest.NewServer(stub.handler())
	t.Cleanup(server.Close)
	pointLiveChecksAt(t, server.URL)
	return f
}

// liveRuleset is the praetor ruleset for policy and contexts as GitHub stores it under id 1, in
// enforcement.
func liveRuleset(t *testing.T, policy config.BranchProtectionPolicy, contexts []string, enforcement string) map[int]map[string]any {
	t.Helper()
	raw, err := forge.RenderRepositoryRuleset("main", policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["id"], doc["enforcement"] = 1, enforcement
	return map[int]map[string]any{1: doc}
}

// classicProtection is a legacy protection object that enforces the default declared policy and
// requires checks.
func classicProtection(checks ...string) map[string]any {
	return map[string]any{
		"required_pull_request_reviews": map[string]any{"required_approving_review_count": 1, "dismiss_stale_reviews": true, "require_code_owner_reviews": true},
		"required_linear_history":       map[string]any{"enabled": true},
		"required_status_checks":        map[string]any{"contexts": checks},
	}
}

// Positive (#159): the live branch protection is compared with the declared policy, through a
// ruleset or the legacy protection object alone, and a branch that enforces every declared
// property passes, naming its mechanism; nothing is written.
func TestAuditLiveBranchProtection_Positive_MatchingProtectionPasses(t *testing.T) {
	stub := &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), []string{"CI"}, "active")}
	f := protectedFixture(t, false, "", stub)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("matching live protection failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out, `[PASS] Live branch protection of main compared with the forge: GitHub enforces every declared property (protected by ruleset "praetor-main-protection" #1).`,
		"Audit Summary: configured governance gates passed")

	classic := &forgeStub{legacy: classicProtection("CI")}
	f = protectedFixture(t, false, "", classic)
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("matching classic protection failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] Live branch protection of main compared with the forge: GitHub enforces every declared property (protected by branch protection).")
	if writes := append(stub.recorded(), classic.recorded()...); len(writes) != 0 {
		t.Fatalf("the audit wrote to the forge: %v", writes)
	}
}

// Negative (#159): a declared status check the branch does not require, signed commits or
// approving reviews the branch does not enforce, a classic-only protection missing a check, and
// the praetor ruleset left in evaluate enforcement each fail the audit naming the property.
func TestAuditLiveBranchProtection_Negative_DriftFailsNamingEachProperty(t *testing.T) {
	for name, tc := range map[string]struct {
		signed bool
		stub   *forgeStub
		want   []string
	}{
		"missing context": {stub: &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), nil, "active")},
			want: []string{"  Required status checks: declared 1, live 0 of 1 required; missing: CI"}},
		"signatures": {signed: true, stub: &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), []string{"CI"}, "active")},
			want: []string{"  Signed commits: declared required, live not enforced"}},
		"review count": {stub: &forgeStub{rulesets: liveRuleset(t, config.BranchProtectionPolicy{
			EnforceLinearHistory: true, DismissStaleReviews: true, ReviewMode: config.BranchReviewModeSingleMaintainer}, []string{"CI"}, "active")},
			want: []string{"  Approving reviews: declared 1, live 0", "  Code owner review: declared required, live not enforced"}},
		"classic only": {stub: &forgeStub{legacy: classicProtection("Lint")},
			want: []string{"(protected by branch protection)", "missing: CI"}},
		"enforcement": {stub: &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), []string{"CI"}, "evaluate")},
			want: []string{`  Ruleset enforcement: declared active, live evaluate (ruleset "praetor-main-protection" #1)`,
				"  Pull requests: declared required, live not enforced"}},
	} {
		f := protectedFixture(t, tc.signed, "", tc.stub)
		out, err := f.audit(t)
		mustErrContain(t, err, "[FAIL] Live branch protection of main on GitHub does not match the declared policy")
		for _, want := range tc.want {
			mustErrContain(t, err, want)
		}
		mustErrContain(t, err, "'praetorctl sync --remote' to reconcile it")
		if strings.Contains(out, "Audit Summary: configured governance gates passed") {
			t.Fatalf("%s: drifted live protection reported a passing audit:\n%s", name, out)
		}
	}
}

// Boundary (#159): --offline, a missing token and a forge that refuses the read report the
// comparison as not made with the reason, never as a pass, and the audit passes; a declined
// branch-ruleset step keeps its decline line and asks the forge nothing about it.
func TestAuditLiveBranchProtection_Boundary_NotComparedIsNamed(t *testing.T) {
	var reports []string
	audit := func(f *auditFixture, args ...string) string {
		t.Helper()
		out, err := f.audit(t, args...)
		if err != nil {
			t.Fatalf("a comparison not made failed the audit: %v\n%s", err, out)
		}
		reports = append(reports, out)
		return out
	}
	unprotected := &forgeStub{}
	f := protectedFixture(t, false, "", unprotected)
	mustContain(t, audit(f, "--offline"), "[SKIP] Live branch protection not compared with the forge: --offline")
	if n := unprotected.requestCount(); n != 0 {
		t.Fatalf("--offline asked the forge %d times", n)
	}
	resolveActionsToken = func(context.Context) string { return "" }
	mustContain(t, audit(f), "[SKIP] Live branch protection not compared with the forge: no forge token")

	refused := &forgeStub{legacyStatus: http.StatusForbidden}
	mustContain(t, audit(protectedFixture(t, false, "", refused)),
		"[SKIP] Live branch protection not compared with the forge: read the live branch protection of main: "+
			"read the legacy branch protection of main: unexpected status 403")

	declined := &forgeStub{}
	mustContain(t, audit(protectedFixture(t, false, "branch-ruleset", declined)),
		"[PASS] Branch protection ruleset declined by adoption.decline.",
		"[INFO] Live branch protection not compared with the forge: branch-ruleset declined by adoption.decline.")
	for _, path := range declined.readPaths() {
		if strings.Contains(path, "/rules/branches/") || strings.HasSuffix(path, "/protection") || strings.HasSuffix(path, "/rulesets") {
			t.Fatalf("a declined branch ruleset read %s from the forge", path)
		}
	}
	for _, report := range reports {
		if strings.Contains(report, "[PASS] Live branch protection") {
			t.Fatalf("a comparison not made was reported as a pass:\n%s", report)
		}
	}
}
