package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
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
	mustContain(t, out, "Repository: acme/widgets", "Status: Local files match the declared policy.\n"+adopt.PlanLiveNotCompared,
		"Approving Reviewers:       0", "Configured Reviewer Minimum: 1", "Review Mode:               single_maintainer")
	// A plan that read local files only never says no change is required (#159).
	if strings.Contains(out, "No changes required") {
		t.Fatalf("plan without --remote claims no change is required:\n%s", out)
	}

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

// The heading names the tool, never a repository; the manifest identity follows it (#361).
func TestPrintPlanHeader_NamesTheManifestRepository(t *testing.T) {
	for _, repo := range []config.RepositoryMetadata{{Owner: "acme", Name: "kit"}, {}} {
		out, err := captureStdout(t, func() error {
			return printPlanHeader(&config.Manifest{Repository: repo}, config.DefaultPolicy())
		})
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitN(out, "\n", 3)
		if lines[0] != "=== Praetor Reconcile Plan (Dry Run) ===" || lines[1] != "Repository: "+repo.Owner+"/"+repo.Name {
			t.Errorf("header for %+v = %q", repo, lines[:2])
		}
		if strings.Contains(out, "cordanaLLM/praetor") {
			t.Errorf("header names this product's repository:\n%s", out)
		}
	}
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

// forgeStub is a stateful stand-in for the GitHub rulesets, labels and repository
// metadata REST API of acme/widgets: it stores what is written, lists and reads it back,
// and records every write. A writeStatus of 300 or above makes every write fail with that
// status. repo is the repository object; nil serves a public repository with no metadata.
//
// It also answers the two reads of the branch protection readback: the active rules of a
// branch, which it derives from the stored active rulesets whose refs include the branch
// unless rulesUnenforced is set, and the legacy protection object, which is legacy, or
// GitHub's "Branch not protected" 404 while legacy is nil, or its "Branch not found" 404 while
// branchMissing is set, or a legacyStatus of 300 or above.
type forgeStub struct {
	mu              sync.Mutex
	writes          []string
	reads           []string
	requests        int
	writeStatus     int
	rulesets        map[int]map[string]any
	labels          map[string]map[string]any
	repo            map[string]any
	legacy          map[string]any
	legacyStatus    int
	rulesUnenforced bool
	branchMissing   bool
}

const forgeStubRepo = "/repos/acme/widgets/"

func (s *forgeStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests++
		if r.Method == http.MethodGet {
			s.reads = append(s.reads, r.URL.Path)
		}
		if s.rulesets == nil {
			s.rulesets = map[int]map[string]any{}
		}
		if s.labels == nil {
			s.labels = map[string]map[string]any{}
		}
		if s.repo == nil {
			s.repo = map[string]any{"visibility": "public", "topics": []any{}}
		}
		var body map[string]any
		if r.Method != http.MethodGet {
			s.writes = append(s.writes, r.Method+" "+r.URL.Path)
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				stubRespond(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
				return
			}
			if s.writeStatus >= http.StatusMultipleChoices {
				stubRespond(w, s.writeStatus, map[string]any{"message": "stub rejection"})
				return
			}
		}
		if r.URL.Path == strings.TrimSuffix(forgeStubRepo, "/") || r.URL.Path == forgeStubRepo+"topics" {
			s.serveRepository(w, r.Method, body)
			return
		}
		resource := strings.TrimPrefix(r.URL.Path, forgeStubRepo)
		switch {
		case strings.HasPrefix(resource, "labels"):
			s.serveLabel(w, r.Method, strings.TrimPrefix(resource, "labels"), body)
		case strings.HasPrefix(resource, "rules/branches/"):
			s.serveBranchRules(w, strings.TrimPrefix(resource, "rules/branches/"))
		case strings.HasPrefix(resource, "branches/") && strings.HasSuffix(resource, "/protection"):
			s.serveLegacyProtection(w)
		default:
			s.serveRuleset(w, r.Method, strings.TrimPrefix(resource, "rulesets"), body)
		}
	}
}

// serveBranchRules lists the rules of every stored active ruleset whose ref_name includes
// refs/heads/branch, each with its ruleset id, as GET .../rules/branches/{branch} does.
func (s *forgeStub) serveBranchRules(w http.ResponseWriter, branch string) {
	rules := []map[string]any{}
	ids := make([]int, 0, len(s.rulesets))
	for id := range s.rulesets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		doc := s.rulesets[id]
		if s.rulesUnenforced || doc["enforcement"] != "active" || !stubRulesetTargets(doc, "refs/heads/"+branch) {
			continue
		}
		list, isList := doc["rules"].([]any)
		if !isList {
			continue
		}
		for _, raw := range list {
			rule, isObject := raw.(map[string]any)
			if !isObject {
				continue
			}
			entry := map[string]any{"type": rule["type"], "ruleset_id": id, "ruleset_source_type": "Repository", "ruleset_source": "acme/widgets"}
			if params, present := rule["parameters"]; present {
				entry["parameters"] = params
			}
			rules = append(rules, entry)
		}
	}
	stubRespond(w, http.StatusOK, rules)
}

// stubRulesetTargets reports whether the ruleset's ref_name condition includes ref, exactly or
// through a glob, and does not exclude it.
func stubRulesetTargets(doc map[string]any, ref string) bool {
	conditions, isObject := doc["conditions"].(map[string]any)
	if !isObject {
		return false
	}
	refName, isObject := conditions["ref_name"].(map[string]any)
	if !isObject {
		return false
	}
	matches := func(key string) bool {
		patterns, isList := refName[key].([]any)
		if !isList {
			return false
		}
		for _, pattern := range patterns {
			if text, isString := pattern.(string); isString {
				if matched, err := path.Match(text, ref); err == nil && matched {
					return true
				}
			}
		}
		return false
	}
	return matches("include") && !matches("exclude")
}

// serveLegacyProtection answers GET .../branches/{branch}/protection.
func (s *forgeStub) serveLegacyProtection(w http.ResponseWriter) {
	switch {
	case s.legacyStatus >= http.StatusMultipleChoices:
		stubRespond(w, s.legacyStatus, map[string]any{"message": "stub rejection"})
	case s.branchMissing:
		stubRespond(w, http.StatusNotFound, map[string]any{"message": "Branch not found"})
	case s.legacy == nil:
		stubRespond(w, http.StatusNotFound, map[string]any{"message": "Branch not protected"})
	default:
		stubRespond(w, http.StatusOK, s.legacy)
	}
}

func (s *forgeStub) serveRuleset(w http.ResponseWriter, method, rest string, body map[string]any) {
	id := 0
	if rest != "" {
		if _, err := fmt.Sscanf(rest, "/%d", &id); err != nil || s.rulesets[id] == nil {
			stubRespond(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return
		}
	}
	switch {
	case method == http.MethodGet && id == 0:
		list := make([]map[string]any, 0, len(s.rulesets))
		for rid, doc := range s.rulesets {
			list = append(list, map[string]any{"id": rid, "name": doc["name"], "enforcement": doc["enforcement"]})
		}
		stubRespond(w, http.StatusOK, list)
	case method == http.MethodGet:
		stubRespond(w, http.StatusOK, s.rulesets[id])
	case method == http.MethodPost:
		id = len(s.rulesets) + 1
		body["id"] = id
		s.rulesets[id] = body
		stubRespond(w, http.StatusCreated, body)
	default:
		body["id"] = id
		s.rulesets[id] = body
		stubRespond(w, http.StatusOK, body)
	}
}

func (s *forgeStub) serveLabel(w http.ResponseWriter, method, rest string, body map[string]any) {
	name := strings.TrimPrefix(rest, "/")
	switch {
	case method == http.MethodPatch && s.labels[name] == nil:
		stubRespond(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	case method == http.MethodPatch:
		s.labels[name] = body
		stubRespond(w, http.StatusOK, body)
	default:
		label, isString := body["name"].(string)
		if !isString || label == "" {
			stubRespond(w, http.StatusUnprocessableEntity, map[string]any{"message": "name is required"})
			return
		}
		s.labels[label] = body
		stubRespond(w, http.StatusCreated, body)
	}
}

// serveRepository answers the repository object (GET), a repository update (PATCH) and a
// topic replacement (PUT .../topics).
func (s *forgeStub) serveRepository(w http.ResponseWriter, method string, body map[string]any) {
	switch method {
	case http.MethodGet:
		stubRespond(w, http.StatusOK, s.repo)
	case http.MethodPatch:
		for k, v := range body {
			s.repo[k] = v
		}
		stubRespond(w, http.StatusOK, s.repo)
	default:
		s.repo["topics"] = body["names"]
		stubRespond(w, http.StatusOK, map[string]any{"names": body["names"]})
	}
}

func stubRespond(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		return
	}
}

func (s *forgeStub) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.writes...)
}

func (s *forgeStub) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// readPaths returns the path of every GET the stub answered, in order.
func (s *forgeStub) readPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.reads...)
}

// storedLabels returns the labels written to the stub, by name.
func (s *forgeStub) storedLabels() map[string]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]map[string]any, len(s.labels))
	for name, label := range s.labels {
		out[name] = label
	}
	return out
}

// TestSync_Remote_OriginHost covers BUG-893: the origin host is part of the repository
// identity, so a matching owner/name on another host or in a local path never
// authorizes a GitHub write, and no request of any kind reaches the forge.
func TestSync_Remote_OriginHost(t *testing.T) {
	f := newSyncValidationFixture(t)
	env := initGitFixture(t, f.dir)
	stub := &forgeStub{}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	setOrigin := func(t *testing.T, remote string) {
		t.Helper()
		if out, gerr := runFixtureGit(t, f.dir, env, "config", "remote.origin.url", remote); gerr != nil {
			t.Fatalf("set origin %q: %v (%s)", remote, gerr, out)
		}
	}
	runRemote := func(extra ...string) (string, error) {
		args := append([]string{"--config=" + f.manifestPath, "--remote", "--token=ghp_x", "--endpoint=" + srv.URL}, extra...)
		return runSyncCmd(t, args...)
	}

	// Negative: same owner/name, wrong host or no host at all.
	for remote, needle := range map[string]string{
		"https://gitlab.com/acme/widgets.git":          "origin points at host gitlab.com",
		"git@gitlab.com:acme/widgets.git":              "origin points at host gitlab.com",
		"https://evil.example/github.com/acme/widgets": "origin points at host evil.example",
		"/srv/git/acme/widgets":                        "origin is not a github.com remote",
		"file:///srv/git/acme/widgets":                 "origin is not a github.com remote",
		"https://github.com/acme/widgets/extra":        "origin points at acme/widgets/extra",
	} {
		setOrigin(t, remote)
		_, err := runRemote()
		mustErrContain(t, err, needle)
	}
	// Negative: a --forge-host that is not a bare host name is refused up front.
	setOrigin(t, "https://github.com/acme/widgets")
	_, err := runRemote("--forge-host=https://github.com")
	mustErrContain(t, err, "--forge-host must be a bare host name")
	if n := stub.requestCount(); n != 0 {
		t.Fatalf("a refused origin must not reach the forge, got %d requests", n)
	}

	// Positive: host and path compare case-insensitively.
	setOrigin(t, "https://GitHub.com/Acme/Widgets.git")
	if out, err := runRemote(); err != nil {
		t.Fatalf("matching origin refused: %v\n%s", err, out)
	}
	// Boundary: a GitHub Enterprise host is accepted only when named explicitly.
	setOrigin(t, "git@ghe.example.com:acme/widgets.git")
	_, err = runRemote()
	mustErrContain(t, err, "origin points at host ghe.example.com")
	if out, err := runRemote("--forge-host=ghe.example.com"); err != nil {
		t.Fatalf("explicit enterprise host refused: %v\n%s", err, out)
	}
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
	stub := &forgeStub{}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	out, err := runSyncCmd(t, "--config="+manifest, "--remote", "--token=ghp_x", "--endpoint="+srv.URL)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	mustContain(t, out,
		"[SYNC] Reconciling branch protection ruleset on GitHub for acme/widgets",
		"[OK] Remote branch protection synchronized on GitHub",
		"[OK] Remote labels synchronized on GitHub",
		"Local sync checks finished: labels and ruleset verified; 0 companion checks missing.")
	if writes := stub.recorded(); len(writes) == 0 || writes[0] != "POST /repos/acme/widgets/rulesets" {
		t.Fatalf("expected the ruleset create first, got %v", writes)
	}
	// The remote ruleset is the local one: same name, main and lts-*.
	if raw := stub.storedRuleset(t, 1); !strings.Contains(raw, `"name":"praetor-main-protection"`) ||
		!strings.Contains(raw, `"include":["refs/heads/main","refs/heads/lts-*"]`) {
		t.Fatalf("remote ruleset does not match the local declaration: %s", raw)
	}

	// Boundary: the environment token is accepted, positional arguments are not.
	t.Setenv("GITHUB_TOKEN", "ghp_env")
	if _, err := runSyncCmd(t, "--config="+manifest, "--remote", "--endpoint="+srv.URL); err != nil {
		t.Fatalf("env token sync: %v", err)
	}
	_, err = runSyncCmd(t, "--config="+manifest, "extra")
	mustErrContain(t, err, "no positional arguments")
}

// TestSync_Remote_DefaultBranchMaster covers #71: in a checkout whose origin HEAD is master,
// sync refuses the main ruleset earlier Praetor versions wrote, synthesizes a missing one for
// master, and pushes that same ruleset, master and lts-*, to GitHub.
func TestSync_Remote_DefaultBranchMaster(t *testing.T) {
	f := newSyncValidationFixture(t)
	env := initGitFixture(t, f.dir)
	for _, args := range [][]string{
		{"remote", "add", "origin", "https://github.com/acme/widgets.git"},
		{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master"},
	} {
		if out, gerr := runFixtureGit(t, f.dir, env, args...); gerr != nil {
			t.Fatalf("git %v: %v (%s)", args, gerr, out)
		}
	}
	_, err := runSyncCmd(t, "--config="+f.manifestPath)
	mustErrContain(t, err, "validation failed for default branch master")

	rulesetPath := filepath.Join(f.dir, ".github", "rulesets", "main.json")
	if err := os.Remove(rulesetPath); err != nil {
		t.Fatal(err)
	}
	stub := &forgeStub{}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+srv.URL)
	if err != nil {
		t.Fatalf("remote sync of a master repository: %v\n%s", err, out)
	}
	mustContain(t, out, "matches declared policy for default branch master", "(master and lts-*, read back")
	local, err := os.ReadFile(rulesetPath)
	if err != nil || !strings.Contains(string(local), `"refs/heads/master"`) || strings.Contains(string(local), `"refs/heads/main"`) {
		t.Fatalf("the synthesized ruleset must protect master: %v\n%s", err, local)
	}
	if raw := stub.storedRuleset(t, 1); !strings.Contains(raw, `"include":["refs/heads/master","refs/heads/lts-*"]`) {
		t.Fatalf("the remote ruleset must protect master and lts-*: %s", raw)
	}
}

// storedRuleset returns the JSON of the ruleset the stub holds under id.
func (s *forgeStub) storedRuleset(t *testing.T, id int) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(s.rulesets[id])
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestSync_Remote_RulesetMergesLive covers BUG-761 end to end: a live ruleset that covers
// main alone and carries an operator's extra rule is widened to lts-* and keeps the rule.
func TestSync_Remote_RulesetMergesLive(t *testing.T) {
	f := newSyncValidationFixture(t)
	env := initGitFixture(t, f.dir)
	if out, gerr := runFixtureGit(t, f.dir, env, "remote", "add", "origin", "https://github.com/acme/widgets.git"); gerr != nil {
		t.Fatalf("remote add: %v (%s)", gerr, out)
	}
	stub := &forgeStub{rulesets: map[int]map[string]any{5: {
		"id": 5, "name": "praetor-main-protection", "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/heads/main"}, "exclude": []any{}}},
		"rules":      []any{map[string]any{"type": "code_scanning", "parameters": map[string]any{"code_scanning_tools": []any{}}}},
	}}}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+srv.URL)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	mustContain(t, strings.Join(stub.recorded(), "\n"), "PUT /repos/acme/widgets/rulesets/5")
	raw := stub.storedRuleset(t, 5)
	for _, want := range []string{`"include":["refs/heads/main","refs/heads/lts-*"]`, `"code_scanning"`, `"pull_request"`, `"required_linear_history"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("merged remote ruleset lacks %s: %s", want, raw)
		}
	}
}

// TestSync_Remote_Labels covers BUG-094: --remote writes the .config/labels.yaml taxonomy
// to GitHub (PATCH, then POST for a label GitHub does not have yet) instead of only
// validating the local file, and never deletes a label the taxonomy does not name.
func TestSync_Remote_Labels(t *testing.T) {
	f := newSyncValidationFixture(t)
	env := initGitFixture(t, f.dir)
	if out, gerr := runFixtureGit(t, f.dir, env, "remote", "add", "origin", "git@github.com:acme/widgets.git"); gerr != nil {
		t.Fatalf("remote add: %v (%s)", gerr, out)
	}
	stub := &forgeStub{labels: map[string]map[string]any{
		"hiss-violation": {"name": "hiss-violation", "color": "000000", "description": "stale"},
		"team:widgets":   {"name": "team:widgets", "color": "00ff00", "description": "operator label"},
	}}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	remote := []string{"--config=" + f.manifestPath, "--remote", "--token=ghp_x", "--endpoint=" + srv.URL}

	// Positive: every taxonomy label is converged; the existing one is patched in place.
	out, err := runSyncCmd(t, remote...)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	mustContain(t, out, "[SYNC] Reconciling 14 labels from .config/labels.yaml on GitHub")
	labels := stub.storedLabels()
	if len(labels) != 15 {
		t.Fatalf("expected the 14 taxonomy labels plus the operator's own, got %d: %v", len(labels), labels)
	}
	if got := labels["hiss-violation"]; got["color"] != "d73a4a" || got["description"] != "Code introduces a regression against HISS invariants" {
		t.Fatalf("existing label not converged onto the taxonomy: %v", got)
	}
	if got := labels["team:widgets"]; got["description"] != "operator label" {
		t.Fatalf("a label outside the taxonomy was modified: %v", got)
	}
	writes := strings.Join(stub.recorded(), "\n")
	mustContain(t, writes, "PATCH /repos/acme/widgets/labels/hiss-violation", "POST /repos/acme/widgets/labels")

	// Negative: a rejected label write fails the command.
	failing := &forgeStub{}
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/labels") {
			stubRespond(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
			return
		}
		failing.handler()(w, r)
	}))
	t.Cleanup(failSrv.Close)
	_, err = runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+failSrv.URL)
	mustErrContain(t, err, "reconcile labels")

	// Boundary: an empty taxonomy fails local validation before any forge request.
	empty := &forgeStub{}
	emptySrv := httptest.NewServer(empty.handler())
	t.Cleanup(emptySrv.Close)
	writeFixtureFile(t, f.dir, ".config/labels.yaml", "version: 1\nlabels: []\n")
	_, err = runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+emptySrv.URL)
	mustErrContain(t, err, ".config/labels.yaml validation failed")
	if n := empty.requestCount(); n != 0 {
		t.Fatalf("an invalid taxonomy reached the forge with %d requests", n)
	}
}

// storedRepo returns a copy of the repository object the stub holds.
func (s *forgeStub) storedRepo() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]any, len(s.repo))
	for k, v := range s.repo {
		out[k] = v
	}
	return out
}

// metadataManifest is the fixture manifest declaring private visibility, a description, a
// homepage and two topics.
func metadataManifest() string {
	return strings.Replace(fixtureManifest("acme", "widgets", false), "  visibility: \"public\"\n",
		"  visibility: \"private\"\n  description: \"Widget engine\"\n  homepage: \"https://widgets.example\"\n"+
			"  topics:\n    - \"widgets\"\n    - \"governance\"\n", 1)
}

// TestSync_Remote_RepositoryMetadata covers BUG-576 and BUG-906: --remote writes the
// description, homepage and topics .standards.yaml declares, keeps the topics it does not
// name, and reports visibility drift without changing the visibility.
func TestSync_Remote_RepositoryMetadata(t *testing.T) {
	f := newSyncValidationFixture(t)
	env := initGitFixture(t, f.dir)
	if out, gerr := runFixtureGit(t, f.dir, env, "remote", "add", "origin", "https://github.com/acme/widgets.git"); gerr != nil {
		t.Fatalf("remote add: %v (%s)", gerr, out)
	}
	writeFixtureFile(t, f.dir, ".standards.yaml", metadataManifest())
	stub := &forgeStub{repo: map[string]any{
		"description": "stale", "homepage": "", "visibility": "public", "topics": []any{"governance", "operator-topic"},
	}}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	remote := []string{"--config=" + f.manifestPath, "--remote", "--token=ghp_x", "--endpoint=" + srv.URL}

	// Positive: the drifted fields are written, the undeclared topic is kept, visibility is reported.
	out, err := runSyncCmd(t, remote...)
	if err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	mustContain(t, out,
		"[SYNC] Reconciling repository description, homepage and topics on GitHub",
		"[OK] Repository metadata synchronized on GitHub (updated description, homepage; topics added: widgets;",
		"[DRIFT] Repository visibility: manifest declares private, GitHub reports public; left unchanged")
	mustContain(t, strings.Join(stub.recorded(), "\n"), "PATCH /repos/acme/widgets", "PUT /repos/acme/widgets/topics")
	repo := stub.storedRepo()
	if repo["description"] != "Widget engine" || repo["homepage"] != "https://widgets.example" || repo["visibility"] != "public" {
		t.Fatalf("repository metadata not converged, or visibility changed: %v", repo)
	}
	if topics := fmt.Sprint(repo["topics"]); topics != "[governance operator-topic widgets]" {
		t.Fatalf("topics = %s; the operator's topic must be kept and the declared one added", topics)
	}

	// Boundary: a second run finds nothing to write.
	before := len(stub.recorded())
	out, err = runSyncCmd(t, remote...)
	if err != nil {
		t.Fatalf("second remote sync: %v\n%s", err, out)
	}
	mustContain(t, out, "[OK] Repository metadata synchronized on GitHub (already matched .standards.yaml;")
	for _, write := range stub.recorded()[before:] {
		if write == "PATCH /repos/acme/widgets" || write == "PUT /repos/acme/widgets/topics" {
			t.Fatalf("a converged repository was written again: %v", stub.recorded()[before:])
		}
	}

	// Negative: a rejected metadata write fails the command.
	failing := &forgeStub{}
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/widgets" {
			stubRespond(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
			return
		}
		failing.handler()(w, r)
	}))
	t.Cleanup(failSrv.Close)
	_, err = runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+failSrv.URL)
	mustErrContain(t, err, "reconcile repository metadata")
	mustErrContain(t, err, "422")

	// Negative: a topic GitHub would refuse fails before the first forge request, so neither
	// the ruleset nor the labels are written for a manifest the metadata step would reject.
	untouched := &forgeStub{}
	untouchedSrv := httptest.NewServer(untouched.handler())
	t.Cleanup(untouchedSrv.Close)
	writeFixtureFile(t, f.dir, ".standards.yaml", strings.Replace(metadataManifest(), `- "widgets"`, `- "go lang"`, 1))
	_, err = runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+untouchedSrv.URL)
	mustErrContain(t, err, "reconcile repository metadata")
	mustErrContain(t, err, `repository.topics[0] "go lang"`)
	if n := untouched.requestCount(); n != 0 {
		t.Fatalf("an invalid topic reached the forge with %d requests; the ruleset and labels must stay unwritten", n)
	}
}
