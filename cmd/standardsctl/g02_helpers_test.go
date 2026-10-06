package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// fixtureAgentsMD is a minimal canonical AGENTS.md for hermetic fixtures.
const fixtureAgentsMD = "# Fixture Repository\n\n## Rules\n\n- Keep functions small.\n- Handle every error.\n"

// captureStdout runs fn with os.Stdout redirected into a buffer and returns what fn
// printed together with its error, so command tests can assert on the report lines.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	return captureStream(t, &os.Stdout, fn)
}

// captureStderr is captureStdout for the warnings a command prints to os.Stderr.
func captureStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	return captureStream(t, &os.Stderr, fn)
}

// captureStream runs fn with *stream redirected into a buffer and restores it afterwards.
func captureStream(t *testing.T, stream **os.File, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := *stream
	*stream = w

	var buf bytes.Buffer
	var copyErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, copyErr = io.Copy(&buf, r)
	}()

	runErr := fn()
	*stream = orig
	if cerr := w.Close(); cerr != nil {
		t.Fatalf("close pipe writer: %v", cerr)
	}
	<-done
	if cerr := r.Close(); cerr != nil {
		t.Fatalf("close pipe reader: %v", cerr)
	}
	if copyErr != nil {
		t.Fatalf("capture output: %v", copyErr)
	}
	return buf.String(), runErr
}

// writeFixtureFile writes content to rel under dir, creating parent directories, and
// returns the absolute path.
func writeFixtureFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return path
}

// readFixtureFile returns the content of rel under dir.
func readFixtureFile(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// initGitFixture turns dir into a repository with every file committed on main and a
// lefthook.yml, so the audit's hook gate has its configuration. initArgs are appended to
// git init. It skips the test when git is unavailable and returns the environment for
// further fixture git calls.
func initGitFixture(t *testing.T, dir string, initArgs ...string) []string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	env := testsupport.HermeticGitEnv(t)
	writeFixtureFile(t, dir, "lefthook.yml", "pre-commit:\n  commands: {}\n")
	initCmd := append([]string{"init", "-q", "-b", "main"}, initArgs...)
	for _, args := range [][]string{initCmd, {"add", "-A"}, {"commit", "-q", "-m", "fixture"}} {
		if out, err := runFixtureGit(t, dir, env, args...); err != nil {
			t.Skipf("git %v failed in sandbox: %v (%s)", args, err, out)
		}
	}
	return env
}

// gitCommitAll stages and commits everything in dir.
func gitCommitAll(t *testing.T, dir string, env []string, msg string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", msg}} {
		if out, err := runFixtureGit(t, dir, env, args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
}

// auditFixture is a hermetic repository that passes every governance gate.
type auditFixture struct {
	dir          string
	manifestPath string
	baselinePath string
	gitEnv       []string
}

const fixtureHarnessJSON = `{"version":1,"platform":"acme/widgets","operating_contract":["Rule 0: terminal disposition required."],"agit_push_format":"reviewed fixture push","invariants":["fixture invariant"]}` + "\n"

// fixtureManifest renders a .standards.yaml for owner/name; signed adds the
// require_signed_commits override.
func fixtureManifest(owner, name string, signed bool) string {
	return fixtureProfileManifest(owner, name, "framework", signed)
}

// fixtureProfileManifest is fixtureManifest declaring profile instead of framework.
func fixtureProfileManifest(owner, name, profile string, signed bool) string {
	m := "version: 1\nrepository:\n  owner: \"" + owner + "\"\n  name: \"" + name + "\"\n  visibility: \"public\"\n" +
		"profiles:\n  - \"" + profile + "\"\nfacets:\n  - \"security:high\"\n"
	if signed {
		m += "overrides:\n  branch_protection:\n    require_signed_commits: true\n"
	}
	return m + fixtureRegisterSources()
}

func fixtureRegisterSources() string {
	inputs := []config.RegisterSourceInput{
		{Path: ".paperclip/harness.json", Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "operating_contract.*"},
		{Path: ".paperclip/harness.json", Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "invariants.*"},
	}
	coverage, err := cavemansource.CoverageFromDocuments(context.Background(), inputs, map[string][]byte{".paperclip/harness.json": []byte(fixtureHarnessJSON)})
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("register:\n  sources:\n    expected: %d\n    sha256: %q\n    inputs:\n"+
		"      - {path: .paperclip/harness.json, surface: prompts, kind: message, format: json, selector: 'operating_contract.*'}\n"+
		"      - {path: .paperclip/harness.json, surface: prompts, kind: message, format: json, selector: 'invariants.*'}\n",
		coverage.Applicable, coverage.SHA256)
}

// newAuditFixture builds the fixture: pinned lockfile, zero-debt baseline, compiled agent
// context, label taxonomy, ruleset and paperclip harness.
func newAuditFixture(t *testing.T) *auditFixture {
	t.Helper()
	lf := newLockFixture(t)
	lf.writeLock(t,
		lf.digestOf(t, ".config/archetypes/framework.yaml"),
		lf.digestOf(t, ".config/archetypes/facets/security-high.yaml"),
		"")
	dir := lf.dir
	f := &auditFixture{
		dir:          dir,
		manifestPath: writeFixtureFile(t, dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)),
		baselinePath: filepath.Join(dir, ".standards-baseline.json"),
	}
	f.writeBaseline(t, nil, "")

	agentsPath := writeFixtureFile(t, dir, "AGENTS.md", fixtureAgentsMD)
	if _, err := compiler.SyncRegisterBlock(context.Background(), dir, agentsPath, true); err != nil {
		t.Fatalf("splice fixture text register: %v", err)
	}
	tr := compiler.NewTranspiler()
	res, err := tr.Compile(agentsPath)
	if err != nil {
		t.Fatalf("compile fixture AGENTS.md: %v", err)
	}
	if err := tr.WriteOutputs(res, dir); err != nil {
		t.Fatalf("write fixture context: %v", err)
	}

	writeFixtureFile(t, dir, ".config/labels.yaml", "version: 1\nlabels: []\n")
	writeDeclaredRuleset(t, dir, config.DefaultPolicy().BranchProtection)
	writeFixtureFile(t, dir, ".paperclip/harness.json", fixtureHarnessJSON)
	// The text register block sends agent evidence under .workingdir/; audit fails unless Git
	// ignores it (compiler.CheckEvidenceIgnored).
	writeFixtureFile(t, dir, ".gitignore", "/.workingdir/\n")
	f.gitEnv = initGitFixture(t, dir)
	writeFixtureHook(t, dir, ".git/hooks/pre-commit", fixtureLefthookHook)
	return f
}

// fixtureLefthookHook is the part of the pre-commit hook Lefthook 2.1.14 installs that the audit
// recognises (adopt.AuditInstalledGitHook): the LEFTHOOK fingerprint and the dispatch line of
// its hook template. call_lefthook is stubbed to succeed, so a fixture commit runs no lefthook.
const fixtureLefthookHook = "#!/bin/sh\n\nif [ \"$LEFTHOOK\" = \"0\" ]; then\n  exit 0\nfi\n\n" +
	"call_lefthook()\n{\n  exit 0\n}\n\ncall_lefthook run \"pre-commit\" \"$@\"\n"

// writeFixtureHook writes an executable hook at rel under dir, which the audit's hook gate runs.
func writeFixtureHook(t *testing.T, dir, rel, content string) string {
	t.Helper()
	hook := writeFixtureFile(t, dir, rel, content)
	if err := os.Chmod(hook, 0o700); err != nil {
		t.Fatal(err)
	}
	return hook
}

// writeDeclaredRuleset writes the ruleset the branch protection audit accepts for dir: the one
// policy renders for the status checks dir's workflows report. The audit compares content, so
// a placeholder such as "{}" no longer passes it.
func writeDeclaredRuleset(t *testing.T, dir string, policy config.BranchProtectionPolicy) {
	t.Helper()
	contexts, err := forge.RequiredStatusContexts(t.Context(), dir)
	if err != nil {
		t.Fatalf("discover fixture status checks: %v", err)
	}
	ruleset, err := forge.RenderRepositoryRuleset("main", policy, contexts)
	if err != nil {
		t.Fatalf("render fixture ruleset: %v", err)
	}
	writeFixtureFile(t, dir, ".github/rulesets/main.json", string(ruleset))
}

// writeBaseline records infractions (and an optional rationale) as the fixture baseline.
func (f *auditFixture) writeBaseline(t *testing.T, infractions []baseline.Infraction, rationale string) {
	t.Helper()
	b := &baseline.Baseline{
		Version:           1,
		Repository:        "acme/widgets",
		Infractions:       append([]baseline.Infraction{}, infractions...),
		IncreaseRationale: rationale,
	}
	if err := baseline.SaveBaseline(f.baselinePath, b); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
}

// legacyGoSource is a Go file carrying one HISS-07 infraction on line 4: a discarded call
// result. It previously discarded an integer literal, which HISS-07 no longer reports
// because a non-call cannot carry an error, so the fixture demonstrated a false positive
// rather than the rule it stands for.
const legacyGoSource = "package legacy\n\nfunc legacy() {\n\t" + "_" + " = fail()\n}\n\nfunc fail() error { return nil }\n"

// addViolation writes legacy.go into the fixture and returns its baseline infraction.
func (f *auditFixture) addViolation(t *testing.T) baseline.Infraction {
	t.Helper()
	writeFixtureFile(t, f.dir, "legacy.go", legacyGoSource)
	return baseline.Infraction{
		RuleID: "HISS-07", FilePath: "legacy.go", LineNumber: 4,
		Message: "Legacy unchecked error assignment", Fingerprint: "legacy.go:4:HISS-07",
	}
}

// audit runs the audit command against the fixture with extra flags.
func (f *auditFixture) audit(t *testing.T, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"--config=" + f.manifestPath}, extra...)
	return captureStdout(t, func() error { return dispatchCommand("audit", args) })
}

// mustContain fails unless every needle occurs in haystack.
func mustContain(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Fatalf("expected output to contain %q, got:\n%s", n, haystack)
		}
	}
}

// mustErrContain fails unless err is non-nil and mentions needle.
func mustErrContain(t *testing.T, err error, needle string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", needle)
	}
	if !strings.Contains(err.Error(), needle) {
		t.Fatalf("expected error containing %q, got: %v", needle, err)
	}
}
