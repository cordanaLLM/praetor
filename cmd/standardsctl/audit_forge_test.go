// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { stub.serve(t, w, r) }))
	t.Cleanup(server.Close)
	pointLiveChecksAt(t, server.URL)
}

// serve records the request and answers it (answer).
func (s *actionsStub) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.EscapedPath())
	s.mu.Unlock()
	status, body := s.answer(r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encode stub answer: %v", err)
	}
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
	writeFixtureFile(t, f.dir, ".standards.yaml", actionsManifest(permissions, approve))
	f.addGitHubOrigin(t)
	gitCommitAll(t, f.dir, f.gitEnv, "declare actions policy")
}

// actionsManifest is the fixture manifest of acme/widgets with an overrides.actions block.
func actionsManifest(permissions string, approve bool) string {
	block := "overrides:\n  actions:\n    default_workflow_permissions: " + permissions +
		"\n    allow_create_and_approve_pull_requests: " + strconv.FormatBool(approve) + "\n"
	return strings.Replace(fixtureManifest("acme", "widgets", false), "register:\n", block+"register:\n", 1)
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
		mustErrContain(t, err, wantReconcileHint)
		if strings.Contains(out, "Audit Summary: configured governance gates passed") {
			t.Fatalf("%s: drifted live protection reported a passing audit:\n%s", name, out)
		}
	}
}

// wantReconcileHint is the remedy a drift failure on main names (#159 review): plan and sync
// --remote from an up-to-date main only, since both require the checks of the checkout they run
// in and sync never drops one; what else sync writes; and which token both read.
const wantReconcileHint = "To reconcile it, run 'praetorctl plan --remote' and then 'praetorctl sync --remote' from an up-to-date checkout of main, not from another branch:\n" +
	"  both require the status checks of the workflows of the checkout they run in, and sync keeps every check it finds required, " +
	"so a sync from another branch requires its jobs that main does not run, and they block every pull request until removed by hand.\n" +
	"  sync --remote also writes the labels in .config/labels.yaml and the repository description, homepage and topics; " +
	"plan --remote previews only the branch protection, so review those first.\n" +
	"  Both read the token from --token, GITHUB_TOKEN or GH_TOKEN, never from the gh session."

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

// prJobWorkflow is a workflow whose one job, named name, reports on every pull request.
func prJobWorkflow(name string) string {
	return strings.Replace(ciWorkflow, "name: CI", "name: "+name, 1)
}

// recordOriginMain points refs/remotes/origin/main at the fixture's HEAD, as a fetch records the
// default branch of origin.
func (f *auditFixture) recordOriginMain(t *testing.T) {
	t.Helper()
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "update-ref", "refs/remotes/origin/main", "HEAD"); err != nil {
		t.Fatalf("record origin/main: %v (%s)", err, out)
	}
}

// commitWorkflows writes each of workflows, a file name under .github/workflows mapped to its
// content or to "" to remove it, renders the committed ruleset for the result and commits it.
func (f *auditFixture) commitWorkflows(t *testing.T, workflows map[string]string) {
	t.Helper()
	for name, content := range workflows {
		if content == "" {
			if err := os.Remove(filepath.Join(f.dir, ".github", "workflows", name)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		writeFixtureFile(t, f.dir, ".github/workflows/"+name, content)
	}
	writeDeclaredRuleset(t, f.dir, declaredProtection(false))
	gitCommitAll(t, f.dir, f.gitEnv, "change the workflows")
}

// Positive (#159 review): a job this checkout adds is not on origin/main yet, so the live
// protection cannot require it: the audit compares the checks origin/main reports, names the added
// one as not compared yet, and passes. A protection that requires only the default branch's checks
// is no drift.
func TestAuditLiveBranchProtection_Positive_CheckAddedOffTheDefaultBranchIsNotCompared(t *testing.T) {
	stub := &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), []string{"CI"}, "active")}
	f := protectedFixture(t, false, "", stub)
	f.recordOriginMain(t)
	f.commitWorkflows(t, map[string]string{"lint.yml": prJobWorkflow("Lint")})
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("a check this branch adds failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out, "[INFO] Live branch protection of main: status checks compared with the ones origin/main (",
		"[INFO] Live branch protection of main: not compared yet, as origin/main does not report them: Lint. "+
			"Once they are on main, 'praetorctl sync --remote' requires them.",
		"[PASS] Live branch protection of main compared with the forge")
}

// Negative (#159 review): a check origin/main reports that the live protection does not require
// fails the audit, even when this checkout no longer has the job: the branch GitHub protects is the
// default branch, not this checkout.
func TestAuditLiveBranchProtection_Negative_DefaultBranchCheckNotRequiredDrifts(t *testing.T) {
	stub := &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), []string{"CI"}, "active")}
	f := protectedFixture(t, false, "", stub)
	f.commitWorkflows(t, map[string]string{"api.yml": prJobWorkflow("API")})
	f.recordOriginMain(t)
	f.commitWorkflows(t, map[string]string{"api.yml": ""})
	out, err := f.audit(t)
	mustErrContain(t, err, "[FAIL] Live branch protection of main on GitHub does not match the declared policy")
	mustErrContain(t, err, "  Required status checks: declared 2, live 1 of 2 required; missing: API")
	if strings.Contains(out, "not compared yet") {
		t.Fatalf("a check this checkout removed was reported as one it adds:\n%s", out)
	}
}

// Boundary (#159 review): without origin/main in the checkout the checks are this checkout's and a
// note names that substitution; a workflow on origin/main that does not parse fails the audit rather
// than comparing no checks.
func TestAuditLiveBranchProtection_Boundary_DefaultBranchNotFetched(t *testing.T) {
	stub := &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), []string{"CI"}, "active")}
	f := protectedFixture(t, false, "", stub)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("a checkout without origin/main failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out, "[INFO] Live branch protection of main: origin/main is not in this checkout, so the status checks "+
		"are compared with the ones this checkout's workflows report.",
		"[PASS] Live branch protection of main compared with the forge")

	broken := protectedFixture(t, false, "", &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), []string{"CI"}, "active")})
	writeFixtureFile(t, broken.dir, ".github/workflows/broken.yml", "on: [pull_request\n")
	gitCommitAll(t, broken.dir, broken.gitEnv, "break a workflow")
	broken.recordOriginMain(t)
	if err := os.Remove(filepath.Join(broken.dir, ".github", "workflows", "broken.yml")); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, broken.dir, broken.gitEnv, "repair the workflow")
	_, err = broken.audit(t)
	mustErrContain(t, err, "[FAIL] Live branch protection audit failed: discover the required status checks of origin/main: workflow broken.yml")
}

// recordUnadoptedOriginMain points refs/remotes/origin/main at a commit with an empty tree, as the
// default branch of origin reads before the adoption pull request lands its manifest.
func (f *auditFixture) recordUnadoptedOriginMain(t *testing.T) {
	t.Helper()
	const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	commit, err := runFixtureGit(t, f.dir, f.gitEnv, "commit-tree", emptyTree, "-m", "default branch before adoption")
	if err != nil {
		t.Fatalf("commit the unadopted default branch: %v (%s)", err, commit)
	}
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "update-ref", "refs/remotes/origin/main", strings.TrimSpace(commit)); err != nil {
		t.Fatalf("record origin/main: %v (%s)", err, out)
	}
}

// Boundary (#1040): the first push of an adoption finds a default branch with no manifest and no
// protection. The live comparison is not made and is named with the reason, so the audit passes
// instead of deadlocking the push that would add the manifest.
func TestAuditLiveBranchProtection_Boundary_DefaultBranchWithoutManifestIsNotCompared(t *testing.T) {
	f := protectedFixture(t, false, "", &forgeStub{})
	f.recordUnadoptedOriginMain(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("an unadopted default branch failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out, "[SKIP] Live branch protection of main not compared with the forge: main carries no .standards.yaml yet, "+
		"so the adoption that adds it has not reached it. The comparison starts with the first push after the manifest reaches main.")
	if strings.Contains(out, "does not match the declared policy") {
		t.Fatalf("an unadopted default branch was compared:\n%s", out)
	}
}

// Negative (#1040): a default branch that carries the manifest and no protection still fails the
// comparison; the manifest-presence skip does not hide a branch that should be protected.
func TestAuditLiveBranchProtection_Negative_DefaultBranchWithManifestUnprotectedFails(t *testing.T) {
	f := protectedFixture(t, false, "", &forgeStub{})
	f.recordOriginMain(t)
	_, err := f.audit(t)
	mustErrContain(t, err, "[FAIL] Live branch protection of main on GitHub does not match the declared policy")
}

// Negative (#1040): an error reading whether the default branch carries the manifest does not skip
// the comparison. origin/main absent from the checkout makes the presence unreadable; the
// comparison runs and fails on the unprotected branch, and the unreadable presence is named.
func TestAuditLiveBranchProtection_Negative_ManifestPresenceErrorDoesNotSkip(t *testing.T) {
	f := protectedFixture(t, false, "", &forgeStub{})
	out, err := f.audit(t)
	mustErrContain(t, err, "[FAIL] Live branch protection of main on GitHub does not match the declared policy")
	mustContain(t, out, "[INFO] Live branch protection of main: could not read whether origin/main carries .standards.yaml")
	if strings.Contains(out, "[SKIP] Live branch protection of main not compared") {
		t.Fatalf("an unreadable manifest presence skipped the comparison:\n%s", out)
	}
}

// serveForge points the live forge checks at one stand-in forge for the rest of the test: actions
// answers the Actions reads and protection every other request.
func serveForge(t *testing.T, actions *actionsStub, protection *forgeStub) {
	t.Helper()
	rest := protection.handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/actions/") {
			actions.serve(t, w, r)
			return
		}
		rest(w, r)
	}))
	t.Cleanup(server.Close)
	pointLiveChecksAt(t, server.URL)
}

// Negative (#159 review): drifted Actions workflow permissions do not stop the branch protection
// comparison: both live checks run, and the audit fails naming both drifts.
func TestAuditLiveForge_Negative_EveryDriftIsReported(t *testing.T) {
	protection := &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), nil, "active")}
	f := protectedFixture(t, false, "", protection)
	writeFixtureFile(t, f.dir, ".standards.yaml", actionsManifest("read", false))
	gitCommitAll(t, f.dir, f.gitEnv, "declare actions policy")
	serveForge(t, &actionsStub{repo: workflowPermissions("write", false), org: workflowPermissions("read", false)}, protection)
	_, err := f.audit(t)
	mustErrContain(t, err, "[FAIL] Actions workflow permissions drifted-at-repository")
	mustErrContain(t, err, "[FAIL] Live branch protection of main on GitHub does not match the declared policy")
	mustErrContain(t, err, "  Required status checks: declared 1, live 0 of 1 required; missing: CI")
}

// liveProtectionAudit runs the live branch protection check of the fixture alone, with policy in
// place of the declared one, and returns what it printed and its error.
func liveProtectionAudit(t *testing.T, f *auditFixture, policy *config.ResolvedPolicy) (string, error) {
	t.Helper()
	manifest, err := config.LoadManifest(f.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	live := openActionsForge(t.Context(), f.dir, manifest.Repository, false)
	return captureStdout(t, func() error { return auditLiveBranchProtection(t.Context(), manifest, f.dir, policy, live) })
}

// Negative (#159 review): a comparison that cannot be evaluated is a local defect and fails the
// audit; only a read the forge did not answer is a check not made.
func TestAuditLiveBranchProtection_Negative_UnevaluableComparisonFails(t *testing.T) {
	stub := &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), []string{"CI"}, "active")}
	f := protectedFixture(t, false, "", stub)
	policy := config.DefaultPolicy()
	policy.BranchProtection.RequiredApprovingReviewers = -1
	out, err := liveProtectionAudit(t, f, policy)
	mustErrContain(t, err, "[FAIL] Live branch protection audit failed: compare the live branch protection of main: "+
		"evaluate branch protection: required approving review count cannot be negative")
	if strings.Contains(out, "[SKIP]") {
		t.Fatalf("an unevaluable comparison was reported as not made:\n%s", out)
	}
}

// Boundary (#159 review): a policy that requires neither linear history nor signed commits
// declares no ruleset, so nothing is compared or read, and the line says why.
func TestAuditLiveBranchProtection_Boundary_PolicyWithoutRulesetIsNotCompared(t *testing.T) {
	stub := &forgeStub{}
	f := protectedFixture(t, false, "", stub)
	policy := config.DefaultPolicy()
	policy.BranchProtection.EnforceLinearHistory = false
	out, err := liveProtectionAudit(t, f, policy)
	if err != nil {
		t.Fatalf("a policy without a ruleset failed: %v\n%s", err, out)
	}
	mustContain(t, out, "[INFO] Live branch protection not compared with the forge: policy requires neither linear history "+
		"nor signed commits, so it declares no branch protection ruleset.")
	if n := stub.requestCount(); n != 0 {
		t.Fatalf("a policy without a ruleset asked the forge %d times", n)
	}
}

// Boundary (#159 review): a default branch GitHub does not have yet enforces nothing, so the audit
// reports it as not compared in one line and passes, instead of a drift on every property that
// would refuse the first push of the branch.
func TestAuditLiveBranchProtection_Boundary_UnpushedDefaultBranchIsNotCompared(t *testing.T) {
	f := protectedFixture(t, false, "", &forgeStub{branchMissing: true})
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("an unpushed default branch failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out, "[SKIP] Live branch protection of main not compared with the forge: main does not exist on GitHub yet, "+
		"so nothing is enforced on it. The audit compares it once it is pushed; 'praetorctl plan --remote' shows what the "+
		"rulesets that target it require.",
		"Audit Summary: configured governance gates passed")
	if strings.Contains(out, "Live branch protection of main compared with the forge") {
		t.Fatalf("an unpushed branch was reported as compared:\n%s", out)
	}
}

// Boundary (#159 review): a workflow of origin/main the checkout does not hold, as in a blobless
// partial clone, is not fetched: the checks are this checkout's, and a note names that
// substitution and the workflow. With the object present the same checkout drifts
// (TestAuditLiveBranchProtection_Negative_DefaultBranchCheckNotRequiredDrifts).
func TestAuditLiveBranchProtection_Boundary_AbsentDefaultBranchWorkflowIsNamed(t *testing.T) {
	stub := &forgeStub{rulesets: liveRuleset(t, declaredProtection(false), []string{"CI"}, "active")}
	f := protectedFixture(t, false, "", stub)
	f.commitWorkflows(t, map[string]string{"api.yml": prJobWorkflow("API")})
	f.recordOriginMain(t)
	f.commitWorkflows(t, map[string]string{"api.yml": ""})
	object, err := runFixtureGit(t, f.dir, f.gitEnv, "rev-parse", "origin/main:.github/workflows/api.yml")
	if err != nil {
		t.Fatalf("resolve the workflow object: %v (%s)", err, object)
	}
	object = strings.TrimSpace(object)
	loose := filepath.Join(f.dir, ".git", "objects", object[:2], object[2:])
	if err := os.Chmod(loose, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(loose); err != nil {
		t.Fatal(err)
	}
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("an absent default branch workflow failed the audit: %v\n%s", err, out)
	}
	mustContain(t, out, "[INFO] Live branch protection of main: the workflows of origin/main (",
		") are not all in this checkout, as in a partial clone (workflow api.yml at commit ",
		"), so the status checks are compared with the ones this checkout's workflows report.",
		"[PASS] Live branch protection of main compared with the forge")
}

// withQueueRuleset adds the live merge queue ruleset over includes (id 2) to rulesets.
func withQueueRuleset(rulesets map[int]map[string]any, includes ...string) map[int]map[string]any {
	rulesets[2] = liveQueueRuleset(includes...)
	return rulesets
}

// Negative (rule 13, #893): auditLiveBranchProtection fails a branch whose live rulesets hold an
// active merge_queue rule while the policy does not declare one, naming the branch and the
// remedy; with the declaration it fails a required check whose workflow lacks merge_group, a
// declared queue the forge lacks, and a queue ruleset that gained a wildcard; and a branch with
// the queue ruleset on the default branch alone passes. Dropping the liveMergeQueueVerdict term
// makes the first two fail, dropping MergeQueueRulesetFindings the last two.
func TestAuditLiveBranchProtection_MergeQueueWiring(t *testing.T) {
	queued := declaredProtection(false)
	queued.MergeQueue = true
	declared := config.DefaultPolicy()
	declared.BranchProtection.MergeQueue = true
	audit := func(rulesets map[int]map[string]any, policy *config.ResolvedPolicy) (string, error) {
		return liveProtectionAudit(t, protectedFixture(t, false, "", &forgeStub{rulesets: rulesets}), policy)
	}

	_, err := audit(withQueueRuleset(liveRuleset(t, declaredProtection(false), []string{"CI"}, "active"), "refs/heads/main"), config.DefaultPolicy())
	mustErrContain(t, err, "carries an active merge_queue rule that the policy does not declare")
	mustErrContain(t, err, "overrides.branch_protection.merge_queue: true")
	mustErrContain(t, err, "main")

	_, err = audit(withQueueRuleset(liveRuleset(t, queued, []string{"CI"}, "active"), "refs/heads/main"), declared)
	mustErrContain(t, err, ".github/workflows/ci.yml: no merge_group trigger")

	_, err = audit(liveRuleset(t, queued, nil, "active"), declared)
	mustErrContain(t, err, "Merge queue")

	_, err = audit(withQueueRuleset(liveRuleset(t, queued, nil, "active"), "refs/heads/main", "refs/heads/lts-*"), declared)
	mustErrContain(t, err, "Merge queue ruleset refs")
	mustErrContain(t, err, "wildcard refs/heads/lts-*")

	if out, err := audit(withQueueRuleset(liveRuleset(t, queued, nil, "active"), "refs/heads/main"), declared); err != nil {
		t.Fatalf("a queue ruleset on the default branch alone failed: %v\n%s", err, out)
	}
	if out, err := audit(liveRuleset(t, declaredProtection(false), []string{"CI"}, "active"), config.DefaultPolicy()); err != nil {
		t.Fatalf("a branch without a queue failed: %v\n%s", err, out)
	}
}
