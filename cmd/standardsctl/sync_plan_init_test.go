package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

func runInitCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand("init", args) })
}

func runPlanCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand("plan", args) })
}

func runSyncCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand("sync", args) })
}

func TestInit_Positive_CompanionsNextToManifest(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "AGENTS.md", fixtureAgentsMD)
	manifest := filepath.Join(dir, ".standards.yaml")

	out, err := runInitCmd(t, "--output="+manifest, "--profile=service", "--facets=security:high, docs:seo-portal")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	mustContain(t, out, "[CREATED] "+manifest, "[TRANSPILED]")
	for _, rel := range []string{".standards.yaml", ".standards-baseline.json", ".standards.lock", "CLAUDE.md"} {
		if !util.FileExists(filepath.Join(dir, rel)) {
			t.Fatalf("expected %s next to the manifest", rel)
		}
	}
	// init compiles the same way compile-context does, so the result verifies at once.
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("a freshly initialised repository must verify: %v\n%s", err, out)
	}
	// Nothing leaked into the process working directory.
	for _, rel := range []string{".standards.lock", ".standards-baseline.json"} {
		if util.FileExists(rel) {
			t.Fatalf("%s was written to the working directory", rel)
		}
	}
	m, err := config.LoadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Profiles) != 1 || m.Profiles[0] != "service" || strings.Join(m.Facets, ",") != "security:high,docs:seo-portal" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
}

func TestInit_Negative(t *testing.T) {
	dir := t.TempDir()
	manifest := writeFixtureFile(t, dir, ".standards.yaml", "version: 1\n")

	_, err := runInitCmd(t, "--output="+manifest)
	mustErrContain(t, err, "already exists")

	_, err = runInitCmd(t, "--output="+filepath.Join(dir, "missing-parent", ".standards.yaml"))
	mustErrContain(t, err, "failed to write")

	_, err = runInitCmd(t, "--output="+filepath.Join(dir, "other.yaml"), "extra")
	mustErrContain(t, err, "no positional arguments")

	// A path that cannot be inspected (a file used as a directory) is an error.
	blocker := writeFixtureFile(t, dir, "blocker", "x")
	if _, err := runInitCmd(t, "--output="+filepath.Join(blocker, ".standards.yaml")); err == nil {
		t.Fatal("expected an error when the manifest path is under a regular file")
	}
}

func TestInit_Boundary(t *testing.T) {
	// No AGENTS.md: nothing is transpiled; blank facets yield an empty list.
	dir := t.TempDir()
	manifest := filepath.Join(dir, ".standards.yaml")
	out, err := runInitCmd(t, "--output="+manifest, "--facets= , ,")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if strings.Contains(out, "[TRANSPILED]") {
		t.Fatal("no AGENTS.md must mean no transpilation")
	}
	m, err := config.LoadManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Facets) != 0 {
		t.Fatalf("expected no facets, got %v", m.Facets)
	}

	// Pre-existing companions are kept untouched.
	dir2 := t.TempDir()
	writeFixtureFile(t, dir2, ".standards.lock", "custom\n")
	if _, err := runInitCmd(t, "--output="+filepath.Join(dir2, ".standards.yaml")); err != nil {
		t.Fatalf("init with existing lock: %v", err)
	}
	if got := readFixtureFile(t, dir2, ".standards.lock"); got != "custom\n" {
		t.Fatalf("existing lockfile was overwritten: %q", got)
	}
}

func TestPlan_3D(t *testing.T) {
	// Positive: a complete fixture matches policy.
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+"overrides:\n  branch_protection:\n    review_mode: single_maintainer\n")
	out, err := runPlanCmd(t, "--config="+f.manifestPath)
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	mustContain(t, out, "Repository: acme/widgets", "Local state matches declared policy",
		"Approving Reviewers:       0", "Configured Reviewer Minimum: 1", "Review Mode:               single_maintainer")

	// Negative: companions resolve against the manifest directory, not the cwd.
	if err := os.Remove(filepath.Join(f.dir, ".standards.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.dir, ".github", "rulesets", "main.json")); err != nil {
		t.Fatal(err)
	}
	out, err = runPlanCmd(t, "--config="+f.manifestPath)
	if err != nil {
		t.Fatalf("plan with drift: %v", err)
	}
	mustContain(t, out, "[DRIFT] Missing baseline files: .standards.lock", ".github/rulesets/main.json (Branch protection ruleset missing)")

	_, err = runPlanCmd(t, "--config="+filepath.Join(f.dir, "none.yaml"))
	mustErrContain(t, err, "failed to load manifest")
	_, err = runPlanCmd(t, "--config="+f.manifestPath, "extra")
	mustErrContain(t, err, "no positional arguments")
}

func TestPrintPlanHeaderRejectsInvalidReviewMode(t *testing.T) {
	policy := config.DefaultPolicy()
	policy.BranchProtection.ReviewMode = "unreviewed"
	_, err := captureStdout(t, func() error { return printPlanHeader(&config.Manifest{}, policy) })
	mustErrContain(t, err, "unsupported branch protection review mode")
}

// rulesetTypes reads the synthesized ruleset under dir and returns its rule types.
func rulesetTypes(t *testing.T, dir string) []string {
	t.Helper()
	var ruleset struct {
		Rules []struct {
			Type string `json:"type"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(readFixtureFile(t, dir, ".github/rulesets/main.json")), &ruleset); err != nil {
		t.Fatalf("parse ruleset: %v", err)
	}
	types := make([]string, 0, len(ruleset.Rules))
	for _, r := range ruleset.Rules {
		types = append(types, r.Type)
	}
	return types
}

func hasType(types []string, want string) bool {
	for _, tp := range types {
		if tp == want {
			return true
		}
	}
	return false
}

func TestSync_Positive_LocalReconciliation(t *testing.T) {
	f := newSyncValidationFixture(t)
	dir, manifest := f.dir, f.manifestPath
	for _, path := range []string{".config/labels.yaml", ".github/rulesets/main.json"} {
		if err := os.Remove(filepath.Join(dir, path)); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runSyncCmd(t, "--config="+manifest)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	mustContain(t, out,
		"[FIX] Synthesizing missing .config/labels.yaml",
		"[FIX] Synthesizing declarative branch protection ruleset",
		"[OK] Lockfile .standards.lock verified",
		"[INFO] Remote forge untouched",
		"Local sync checks finished: labels and ruleset verified; 0 companion checks missing.")
	if !util.FileExists(filepath.Join(dir, ".config", "labels.yaml")) || util.FileExists(filepath.Join(".config", "labels.yaml")) {
		t.Fatal("labels must be synthesized next to the manifest, not in the cwd")
	}
	types := rulesetTypes(t, dir)
	if !hasType(types, "required_linear_history") || hasType(types, "required_signatures") {
		t.Fatalf("default policy must emit linear history but not signatures, got %v", types)
	}

	// A second run verifies instead of re-synthesizing.
	out, err = runSyncCmd(t, "--config="+manifest)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	mustContain(t, out, "[OK] Labels verified", "[OK] Branch protection ruleset verified")

	// A signed-commit override emits the signature rule.
	f2 := newSyncValidationFixture(t)
	dir2 := f2.dir
	manifest2 := writeFixtureFile(t, dir2, ".standards.yaml", fixtureManifest("acme", "widgets", true))
	if err := os.Remove(filepath.Join(dir2, ".github/rulesets/main.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := runSyncCmd(t, "--config="+manifest2); err != nil {
		t.Fatalf("signed sync: %v", err)
	}
	if !hasType(rulesetTypes(t, dir2), "required_signatures") {
		t.Fatal("signed policy must emit required_signatures")
	}
}

// TestSync_Positive_LabelDescriptionReconciledInPlace covers the #266 follow-up: a
// labels.yaml an adopter already has, carrying the pre-rename "HISS-16 invariants"
// wording, is rewritten to the canonical "HISS invariants" text in place. Every other
// byte -- an adopter's own label, its comment, its color -- survives untouched.
func TestSync_Positive_LabelDescriptionReconciledInPlace(t *testing.T) {
	f := newSyncValidationFixture(t)
	dir, manifest := f.dir, f.manifestPath
	stale := `# Canonical Repository Label Taxonomy
version: 1
labels:
  - name: "hiss-violation"
    color: "d73a4a"
    description: "Code introduces a regression against HISS-16 invariants"

  - name: "team:widgets"
    color: "00ff00"
    description: "Owned by the widgets team"
`
	writeFixtureFile(t, dir, ".config/labels.yaml", stale)

	out, err := runSyncCmd(t, "--config="+manifest)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	mustContain(t, out, "[FIX] Updated managed label description(s) in .config/labels.yaml", "[OK] Labels verified")

	got := readFixtureFile(t, dir, ".config/labels.yaml")
	if strings.Contains(got, "HISS-16 invariants") {
		t.Errorf("stale description survived reconciliation:\n%s", got)
	}
	if !strings.Contains(got, `description: "Code introduces a regression against HISS invariants"`) {
		t.Errorf("canonical description not written:\n%s", got)
	}
	if !strings.Contains(got, `name: "team:widgets"`) || !strings.Contains(got, "Owned by the widgets team") {
		t.Errorf("adopter's own label was not preserved:\n%s", got)
	}
	if !strings.Contains(got, "# Canonical Repository Label Taxonomy") {
		t.Errorf("file comment was not preserved:\n%s", got)
	}

	// A second run is a no-op: the canonical text is already present.
	out, err = runSyncCmd(t, "--config="+manifest)
	if err != nil {
		t.Fatalf("second sync: %v\n%s", err, out)
	}
	if strings.Contains(out, "[FIX] Updated managed label description") {
		t.Errorf("reconciliation is not idempotent:\n%s", out)
	}
	mustContain(t, out, "[OK] Labels verified")
}

// forgeStub records write requests and answers GET with an empty ruleset list.
type forgeStub struct {
	mu          sync.Mutex
	writes      []string
	writeStatus int
}

func (s *forgeStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			if _, err := w.Write([]byte("[]")); err != nil {
				return
			}
			return
		}
		s.mu.Lock()
		s.writes = append(s.writes, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		w.WriteHeader(s.writeStatus)
		if _, err := w.Write([]byte(`{"id":1,"message":"stub"}`)); err != nil {
			return
		}
	}
}

func (s *forgeStub) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.writes...)
}

func TestSync_Remote_Negative(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	f := newSyncValidationFixture(t)
	dir, manifest := f.dir, f.manifestPath

	// No credential: nothing is harvested from the gh CLI.
	_, err := runSyncCmd(t, "--config="+manifest, "--remote")
	if !errors.Is(err, ErrRemoteTokenMissing) {
		t.Fatalf("expected ErrRemoteTokenMissing, got %v", err)
	}

	// An unset identity is never defaulted.
	f2 := newSyncValidationFixture(t)
	unset := writeFixtureFile(t, f2.dir, ".standards.yaml", fixtureManifest("", "", false))
	_, err = runSyncCmd(t, "--config="+unset, "--remote", "--token=ghp_x")
	mustErrContain(t, err, "must be set")

	// No origin remote, then a foreign origin: both refuse before any HTTP request.
	_, err = runSyncCmd(t, "--config="+manifest, "--remote", "--token=ghp_x")
	mustErrContain(t, err, "no origin remote")
	env := initGitFixture(t, dir)
	if out, gerr := runFixtureGit(t, dir, env, "remote", "add", "origin", "https://github.com/victim/prod.git"); gerr != nil {
		t.Fatalf("remote add: %v (%s)", gerr, out)
	}
	_, err = runSyncCmd(t, "--config="+manifest, "--remote", "--token=ghp_x")
	mustErrContain(t, err, "foreign repository")
	if out, gerr := runFixtureGit(t, dir, env, "remote", "set-url", "origin", "git@github.com:acme/widgets.git"); gerr != nil {
		t.Fatalf("remote set-url: %v (%s)", gerr, out)
	}

	// An API rejection fails the command instead of printing a warning.
	stub := &forgeStub{writeStatus: http.StatusForbidden}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	out, err := runSyncCmd(t, "--config="+manifest, "--remote", "--token=test-fixture", "--endpoint="+srv.URL)
	mustErrContain(t, err, "403")
	if writes := stub.recorded(); len(writes) != 1 {
		t.Fatalf("a test-prefixed token must make a real request to the stub, got %v", writes)
	}
	if strings.Contains(out, "Synchronization complete.") {
		t.Fatalf("a failed remote sync must not report completion:\n%s", out)
	}
}

func TestSync_Remote_Positive(t *testing.T) {
	f := newSyncValidationFixture(t)
	dir, manifest := f.dir, f.manifestPath
	env := initGitFixture(t, dir)
	if out, gerr := runFixtureGit(t, dir, env, "remote", "add", "origin", "https://github.com/acme/widgets"); gerr != nil {
		t.Fatalf("remote add: %v (%s)", gerr, out)
	}
	stub := &forgeStub{writeStatus: http.StatusCreated}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	out, err := runSyncCmd(t, "--config="+manifest, "--remote", "--token=ghp_x", "--endpoint="+srv.URL)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	mustContain(t, out,
		"[SYNC] Reconciling branch protection ruleset on GitHub for acme/widgets",
		"[OK] Remote branch protection synchronized on GitHub",
		"Local sync checks finished: labels and ruleset verified; 0 companion checks missing.")
	if writes := stub.recorded(); len(writes) != 1 || writes[0] != "POST /repos/acme/widgets/rulesets" {
		t.Fatalf("expected one ruleset create, got %v", writes)
	}

	// Boundary: the environment token is accepted, positional arguments are not.
	t.Setenv("GITHUB_TOKEN", "ghp_env")
	if _, err := runSyncCmd(t, "--config="+manifest, "--remote", "--endpoint="+srv.URL); err != nil {
		t.Fatalf("env token sync: %v", err)
	}
	_, err = runSyncCmd(t, "--config="+manifest, "extra")
	mustErrContain(t, err, "no positional arguments")
}
