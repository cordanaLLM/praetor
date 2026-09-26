package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/config"
)

func TestCavemanSourceExtensionsPositive(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\n")
	writeFixtureFile(t, root, "hooks/check.sh", "echo \"result: pass.\"\n")
	writeFixtureFile(t, root, "hooks/check.py", "print(\"block: path outside root.\")\n")
	hooks := filepath.Join(root, "hooks")
	out, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", "--ext=.sh,.py", hooks)
	if err != nil || strings.Count(out, ": PASS ") != 2 || !strings.Contains(out, "extraction=shell") || !strings.Contains(out, "extraction=python") {
		t.Fatalf("script directory: err=%v\n%s", err, out)
	}

	jsonPath := writeFixtureFile(t, root, "prompts/agent.json", `{"agent":{"review":{"prompt":"result: pass.\\nnext: stop."}}}`)
	out, err = runCavemanCLI(t, "", "check", "--root="+root, "--surface=prompts", "--selector=agent.*.prompt", jsonPath)
	if err != nil || !strings.Contains(out, ": PASS ") || !strings.Contains(out, "source_selector=$.agent.review.prompt") {
		t.Fatalf("JSON selector: err=%v\n%s", err, out)
	}
}

func TestCavemanHookSurfaceDirectorySelectsHookExtensions(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\n")
	writeFixtureFile(t, root, "hooks/check.sh", "echo \"result: pass.\" >&2\n")
	writeFixtureFile(t, root, "hooks/check.py", "print(\"next: stop.\", file=sys.stderr)\n")
	out, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", filepath.Join(root, "hooks"))
	if err != nil || strings.Count(out, ": PASS ") != 2 {
		t.Fatalf("hook surface directory: err=%v\n%s", err, out)
	}
}

func TestCavemanSourceExtensionsNegative(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\n")
	computed := writeFixtureFile(t, root, "hooks/computed.py", "print(render(reason))\n")
	if _, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", computed); err == nil || !strings.Contains(err.Error(), "output unverified") {
		t.Fatalf("computed Python output accepted: %v", err)
	}

	emptyDir := filepath.Join(root, "empty")
	writeFixtureFile(t, emptyDir, "notes.txt", "not source")
	if _, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", "--ext=.py", emptyDir); err == nil || !strings.Contains(err.Error(), "matched zero files") {
		t.Fatalf("zero-match directory accepted: %v", err)
	}

	unknown := writeFixtureFile(t, root, "hooks/check.rb", `puts "result: pass."`)
	if _, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", unknown); err == nil || !strings.Contains(err.Error(), "unsupported source extension") {
		t.Fatalf("unknown source format accepted: %v", err)
	}

	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nregister:\n  surfaces:\n    hooks: docs\n")
	static := writeFixtureFile(t, root, "hooks/static.py", `print("result: pass.")`+"\n")
	if _, err := runCavemanCLI(t, "", "check", "--root="+root, "--surface=hooks", static); err == nil || !strings.Contains(err.Error(), "has no Caveman verdict") {
		t.Fatalf("non-internal surface reported a green skip: %v", err)
	}
}

func TestCavemanConfiguredSourcesCoverage(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "prompts/agent.json", `{"messages":["result: pass.","next: stop."]}`)
	input := config.RegisterSourceInput{Path: "prompts/agent.json", Surface: config.SurfacePrompts,
		Kind: "message", Format: config.SourceFormatJSON, Selector: "messages.*"}
	coverage, err := cavemansource.ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	writeConfiguredSourceManifest(t, root, coverage, "messages.*", "")
	initGitFixture(t, root)

	out, err := runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources")
	if err != nil || strings.Count(out, ": PASS ") != 2 {
		t.Fatalf("configured sources: err=%v\n%s", err, out)
	}

	writeConfiguredSourceManifest(t, root, coverage, "messages.0", "")
	if _, err = runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources"); err == nil || !strings.Contains(err.Error(), "expected 2 applicable values, extracted 1") {
		t.Fatalf("omitted declared value accepted: %v", err)
	}

	writeConfiguredSourceManifest(t, root, coverage, "messages.*", "        unknown: true\n")
	if _, err = runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown source schema field accepted: %v", err)
	}
}

func TestCavemanConfiguredSourcesPassAfterRealAdoption(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	stubDir := t.TempDir()
	stub := writeFixtureFile(t, stubDir, "lefthook", "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(stub, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+filepath.Dir(gitPath))

	repo := t.TempDir()
	writeFixtureFile(t, repo, "go.mod", "module example.invalid/adopted\n")
	env := initGitFixture(t, repo)
	// The harness platform names the repository, so adoption writes it, and binds
	// register.sources to it, only for a resolved identity (BUG-852).
	if output, err := runFixtureGit(t, repo, env, "remote", "add", "origin", "https://github.com/example/adopted.git"); err != nil {
		t.Fatalf("add origin remote: %v (%s)", err, output)
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve source checkout")
	}
	sourceRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	if _, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{Path: repo, Profile: "framework",
		LockSourceRoot: sourceRoot, RecordBaseline: true}); err != nil {
		t.Fatalf("adopt fixture: %v", err)
	}
	if output, err := runFixtureGit(t, repo, env, "add", "-A"); err != nil {
		t.Fatalf("stage adopted source contract: %v (%s)", err, output)
	}
	out, err := runCavemanCLI(t, "", "check", "--root="+repo, "--configured-sources")
	if err != nil || strings.Count(out, ": PASS ") != 13 {
		t.Fatalf("post-adopt configured gate: err=%v\n%s", err, out)
	}
}

func TestAuditCavemanConfiguredSources(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "prompts/agent.json", `{"messages":["result: pass."]}`)
	input := config.RegisterSourceInput{Path: "prompts/agent.json", Surface: config.SurfacePrompts,
		Kind: "message", Format: config.SourceFormatJSON, Selector: "messages.*"}
	coverage, err := cavemansource.ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	writeConfiguredSourceManifest(t, root, coverage, "messages.*", "")
	initGitFixture(t, root)
	manifest, err := config.LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auditCavemanConfiguredSources(context.Background(), manifest, root); err != nil {
		t.Fatalf("configured audit rejected passing source: %v", err)
	}
	writeFixtureFile(t, root, "prompts/agent.json", `{"messages":["We are ready and it is complete."]}`)
	updated, err := cavemansource.ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input})
	if err != nil {
		t.Fatal(err)
	}
	writeConfiguredSourceManifest(t, root, updated, "messages.*", "")
	manifest, err = config.LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auditCavemanConfiguredSources(context.Background(), manifest, root); err == nil || !strings.Contains(err.Error(), "C9 grammar") {
		t.Fatalf("audit accepted non-Caveman runtime source: %v", err)
	}
}

func TestAuditCavemanConfiguredSourcesSkipsClassifiedExclusions(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "hooks/check.py", "print(\"result: pass.\")\n"+
		"print(render(a, the_value))  # caveman:not-applicable untrusted-passthrough\n")
	input := config.RegisterSourceInput{Path: "hooks/check.py", Surface: config.SurfacePrompts,
		Kind: "message", Format: config.SourceFormatPython}
	coverage, err := cavemansource.ExtractInputs(context.Background(), root, []config.RegisterSourceInput{input})
	if err != nil || coverage.Applicable != 1 || coverage.NotApplicable != 1 {
		t.Fatalf("fixture coverage: %+v err=%v", coverage, err)
	}
	initGitFixture(t, root)
	manifest := &config.Manifest{Version: 1, Register: &config.RegisterPolicy{Sources: &config.RegisterSources{
		Expected: coverage.Applicable, NotApplicable: coverage.NotApplicable, SHA256: coverage.SHA256,
		Inputs: []config.RegisterSourceInput{input},
	}}}
	if err := auditCavemanConfiguredSources(context.Background(), manifest, root); err != nil {
		t.Fatalf("audit linted a classified exclusion: %v", err)
	}
	manifest.Register.Sources.NotApplicable++
	if err := auditCavemanConfiguredSources(context.Background(), manifest, root); err == nil {
		t.Fatal("audit accepted a classified-exclusion count mismatch")
	}
}

func TestAuditCavemanConfiguredSourcesDeclarationBoundary(t *testing.T) {
	root := t.TempDir()
	manifest := &config.Manifest{Version: 1}
	if err := auditCavemanConfiguredSources(context.Background(), manifest, root); err == nil ||
		!strings.Contains(err.Error(), "requires register.sources") {
		t.Fatalf("absent register section passed source coverage audit: %v", err)
	}
	manifest.Register = &config.RegisterPolicy{}
	err := auditCavemanConfiguredSources(context.Background(), manifest, root)
	if err == nil || !strings.Contains(err.Error(), "audit requires register.sources") {
		t.Fatalf("declared register omitted source contract: %v", err)
	}

	writeFixtureFile(t, root, ".standards.yaml", "version: 1\n")
	if _, err = runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources"); err == nil ||
		!strings.Contains(err.Error(), "register.sources is not configured") {
		t.Fatalf("configured source gate accepted absent register: %v", err)
	}
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nregister:\n  surfaces:\n    hooks: internal\n")
	if _, err = runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources"); err == nil ||
		!strings.Contains(err.Error(), "register.sources is not configured") {
		t.Fatalf("configured source gate accepted declared register omission: %v", err)
	}
}

func writeConfiguredSourceManifest(t *testing.T, root string, coverage cavemansource.Result, selector, extra string) {
	t.Helper()
	body := fmt.Sprintf(`version: 1
register:
  sources:
    expected: %d
    sha256: %s
    inputs:
      - path: prompts/agent.json
        surface: prompts
        kind: message
        format: json
        selector: %s
%s`, len(coverage.Sources), coverage.SHA256, selector, extra)
	writeFixtureFile(t, root, ".standards.yaml", body)
}

// writeDocsHooksSourceManifest declares hooks: docs and binds coverage over inputs.
func writeDocsHooksSourceManifest(t *testing.T, root string, coverage cavemansource.Result) {
	t.Helper()
	writeFixtureFile(t, root, ".standards.yaml", fmt.Sprintf(`version: 1
register:
  surfaces:
    hooks: docs
  sources:
    expected: %d
    not_applicable: %d
    sha256: %s
    inputs:
      - {path: prompts/agent.json, surface: prompts, kind: message, format: json, selector: 'messages.*'}
      - {path: hooks/check.py, surface: hooks, kind: message, format: python}
`, coverage.Applicable, coverage.NotApplicable, coverage.SHA256))
}

// TestConfiguredSourceGatesShareOneChecker: the audit and `caveman check
// --configured-sources` reach one verdict. A hooks: docs surface that carries only classified
// exclusions needs no Caveman verdict in either; once it carries an applicable value, both
// refuse it. Before the shared checker the audit tested the verdict before the exclusion skip
// and failed the first case while the CLI passed it.
func TestConfiguredSourceGatesShareOneChecker(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "prompts/agent.json", `{"messages":["result: pass."]}`)
	writeFixtureFile(t, root, "hooks/check.py",
		"print(render(a, the_value))  # caveman:not-applicable untrusted-passthrough\n")
	inputs := []config.RegisterSourceInput{
		{Path: "prompts/agent.json", Surface: config.SurfacePrompts, Kind: "message", Format: config.SourceFormatJSON, Selector: "messages.*"},
		{Path: "hooks/check.py", Surface: config.SurfaceHooks, Kind: "message", Format: config.SourceFormatPython},
	}
	coverage, err := cavemansource.ExtractInputs(context.Background(), root, inputs)
	if err != nil || coverage.NotApplicable != 1 {
		t.Fatalf("fixture coverage: %+v err=%v", coverage, err)
	}
	writeDocsHooksSourceManifest(t, root, coverage)
	env := initGitFixture(t, root)
	gates := func() (error, error) {
		manifest, loadErr := config.LoadManifest(filepath.Join(root, ".standards.yaml"))
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		_, cliErr := runCavemanCLI(t, "", "check", "--root="+root, "--configured-sources")
		return auditCavemanConfiguredSources(context.Background(), manifest, root), cliErr
	}
	if auditErr, cliErr := gates(); auditErr != nil || cliErr != nil {
		t.Fatalf("exclusion-only docs surface: audit=%v cli=%v", auditErr, cliErr)
	}

	writeFixtureFile(t, root, "hooks/check.py", "print(\"result: pass.\")\n"+
		"print(render(a, the_value))  # caveman:not-applicable untrusted-passthrough\n")
	coverage, err = cavemansource.ExtractInputs(context.Background(), root, inputs)
	if err != nil {
		t.Fatal(err)
	}
	writeDocsHooksSourceManifest(t, root, coverage)
	if output, gitErr := runFixtureGit(t, root, env, "add", "-A"); gitErr != nil {
		t.Fatalf("stage fixture: %v (%s)", gitErr, output)
	}
	auditErr, cliErr := gates()
	for name, gateErr := range map[string]error{"audit": auditErr, "cli": cliErr} {
		if gateErr == nil || !strings.Contains(gateErr.Error(), "surfaces.hooks = docs has no Caveman verdict") {
			t.Fatalf("%s accepted an applicable value on a docs surface: %v", name, gateErr)
		}
	}
}

func TestBoundedSourceReport(t *testing.T) {
	lines := func(count int) string {
		rows := make([]string, count)
		for index := range rows {
			rows[index] = fmt.Sprintf("value-%d: FAIL", index)
		}
		return strings.Join(rows, "\n") + "\n"
	}
	if got := boundedSourceReport(lines(3)); got != strings.TrimSpace(lines(3)) {
		t.Fatalf("short report changed:\n%s", got)
	}
	if got := boundedSourceReport(lines(maxAuditSourceReportLines)); strings.Contains(got, "more lines") ||
		strings.Count(got, "\n") != maxAuditSourceReportLines-1 {
		t.Fatalf("report at the bound truncated:\n%s", got)
	}
	got := boundedSourceReport(lines(maxAuditSourceReportLines + 5))
	if !strings.Contains(got, "... 5 more lines") || strings.Contains(got, fmt.Sprintf("value-%d:", maxAuditSourceReportLines)) ||
		strings.Count(got, "\n") != maxAuditSourceReportLines {
		t.Fatalf("report over the bound not capped:\n%s", got)
	}
	if got := boundedSourceReport(""); got != "" {
		t.Fatalf("empty report rendered %q", got)
	}
}
