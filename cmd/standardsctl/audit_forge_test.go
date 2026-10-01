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
	endpoint, token := actionsForgeEndpoint, resolveActionsToken
	actionsForgeEndpoint = server.URL
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
