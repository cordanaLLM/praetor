package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/docdistill"
	"github.com/cordanaLLM/praetor/internal/hindsight"
)

// ---- compile_context -------------------------------------------------------------------------

func TestServer_Positive_CompileContextVerifyAndWrite(t *testing.T) {
	srv, root := newFixtureServer(t)

	verify := callTool(t, srv, "standards_compile_context", map[string]any{"verify_only": true})
	expectText(t, "verify", verify, "100% in sync")

	target := filepath.Join(root, "out")
	written := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "out"})
	expectText(t, "write", written, "[COMPILED] CLAUDE.md")
	for _, rel := range []string{"CLAUDE.md", ".cursor/rules/hiss-invariants.mdc", ".github/copilot-instructions.md", ".windsurfrules", ".gemini/GEMINI.md", ".codex/rules.md"} {
		if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(rel))); err != nil {
			t.Errorf("target %s not written: %v", rel, err)
		}
	}
	reverify := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": "out", "verify_only": true})
	expectText(t, "re-verify", reverify, "100% in sync")
}

// A verify-only call reports a stale register block and never writes the source; a
// writing call repairs it, after which both the tool and the audit agree.
func TestServer_CompileContextRegisterBlock(t *testing.T) {
	srv, root := newFixtureServer(t)
	agents := filepath.Join(root, "AGENTS.md")
	synced, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	marker := config.RegisterBlockStart + "\n"
	if strings.Count(string(synced), marker) != 1 {
		t.Fatalf("fixture AGENTS.md must carry the register block once:\n%s", synced)
	}
	stale := strings.Replace(string(synced), marker, marker+"Write however you like.\n", 1)
	writeFixtureFile(t, root, "AGENTS.md", stale)

	verify := callTool(t, srv, "standards_compile_context", map[string]any{"verify_only": true})
	expectError(t, "stale block", verify, "text register block is out of sync")
	if got, err := os.ReadFile(agents); err != nil || string(got) != stale {
		t.Fatalf("verify_only must never write the source: %v", err)
	}
	audit := callTool(t, srv, "standards_audit", nil)
	expectError(t, "audit sees the stale block", audit, "Agent context text register")

	written := callTool(t, srv, "standards_compile_context", nil)
	expectText(t, "write", written, "[COMPILED] CLAUDE.md")
	if got, err := os.ReadFile(agents); err != nil || string(got) != string(synced) {
		t.Fatalf("a writing call must restore the rendered block: %v", err)
	}
	reverify := callTool(t, srv, "standards_compile_context", map[string]any{"verify_only": true})
	expectText(t, "re-verify", reverify, "100% in sync")
}

// The MCP verify and audit mirror the CLI caveman gate: a terse AGENTS.md passes with its
// counts, prose written below the harness fails both, and a writing compile still succeeds.
func TestServer_CompileContextCavemanLint(t *testing.T) {
	srv, root := newFixtureServer(t)
	verify := callTool(t, srv, "standards_compile_context", map[string]any{"verify_only": true})
	expectText(t, "terse verify", verify, "caveman lint passed")
	expectText(t, "terse audit", callTool(t, srv, "standards_audit", nil), "[PASS] Agent context caveman lint passed")

	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	prose := "\nSearch for an existing implementation before adding one. Grep the repository for the capability " +
		"and extend the code that is already there. Two implementations of one behavior are a defect: they " +
		"drift, and the second one stops matching the first.\n"
	writeFixtureFile(t, root, "AGENTS.md", string(agents)+prose)
	expectText(t, "prose compile", callTool(t, srv, "standards_compile_context", nil), "[COMPILED] CLAUDE.md")
	expectError(t, "prose verify", callTool(t, srv, "standards_compile_context", map[string]any{"verify_only": true}), "fails the caveman lint")
	expectError(t, "prose audit", callTool(t, srv, "standards_audit", nil), "fails the caveman lint")
}

func TestServer_HindsightRejectsEscapingCacheSymlinks(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory=%t", directory), func(t *testing.T) {
			srv, root := newFixtureServer(t)
			outside := t.TempDir()
			marker := filepath.Join(outside, "distilled.json")
			if err := os.WriteFile(marker, []byte("protected\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			link, target := filepath.Join(root, hindsight.MemoryFileRel), marker
			if directory {
				link, target = filepath.Join(root, hindsight.MemoryDirRel), outside
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			res := callTool(t, srv, "standards_hindsight_optimize", nil)
			expectError(t, "escaping cache", res, "outside the server root")
			got, err := os.ReadFile(marker)
			if err != nil || string(got) != "protected\n" {
				t.Fatalf("outside cache changed: %q %v", got, err)
			}
			recall := callTool(t, srv, "standards_memory_recall", map[string]any{"query": "protected"})
			expectError(t, "escaping recall", recall, "outside the server root")
		})
	}
}

func TestServer_InspectionRejectsEscapingChildrenAndOverflow(t *testing.T) {
	srv, root := newFixtureServer(t)
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\nfunc HiddenOutsideSymbol() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.go")); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "."})
	expectError(t, "escaping child", res, "outside the server root")
	if strings.Contains(res.Content[0].Text, "HiddenOutsideSymbol") {
		t.Fatal("outside source content leaked")
	}
	if err := os.Remove(filepath.Join(root, "escape.go")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "many")
	for i := 0; i < maxFilesToScan; i++ {
		writeFixtureFile(t, dir, fmt.Sprintf("f%03d.go", i), "package fixture\n")
	}
	res = callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "many"})
	expectText(t, "exact limit", res, "AST Symbol")
	writeFixtureFile(t, dir, "overflow.go", "package fixture\nfunc OmittedSymbol() {}\n")
	res = callTool(t, srv, "standards_inspect_symbols", map[string]any{"path": "many"})
	expectError(t, "overflow", res, "exceeds maximum")
}

func TestFormatAdoptionIncludesSafetyWarnings(t *testing.T) {
	message := "hooks not activated because their configuration was not written by praetor"
	got := formatAdoptMCPResult(&adopt.AdoptReport{Warnings: []string{message}}, false)
	if !strings.Contains(got, message) {
		t.Fatalf("adoption safety warning omitted: %s", got)
	}
}

func TestFormatAdoptionBaselineStatesAndDryRunLabels(t *testing.T) {
	tests := []struct {
		name   string
		report adopt.AdoptReport
		dryRun bool
		want   string
		avoid  string
	}{
		{name: "scanned zero", report: adopt.AdoptReport{BaselineStatus: "scanned"}, want: "Legacy Debt Baselined: 0"},
		{name: "scanned nonzero dry run", report: adopt.AdoptReport{BaselineStatus: "scanned", LegacyDebtCount: 2, CreatedFiles: []string{"x"}}, dryRun: true, want: "Legacy Debt Scanned (dry-run; not written): 2", avoid: "Created Files"},
		{name: "dry run reconciliation", report: adopt.AdoptReport{ReconciledFiles: []string{"x"}}, dryRun: true, want: "Planned Reconciliations: 1", avoid: "Reconciled Files"},
		{name: "not run", report: adopt.AdoptReport{}, want: "Legacy Debt Baseline: not_run; no usable result", avoid: "0 infractions"},
		{name: "existing", report: adopt.AdoptReport{BaselineStatus: "existing", LegacyDebtCount: 3}, want: "Existing Legacy Debt Baseline: 3"},
		{name: "skipped", report: adopt.AdoptReport{BaselineStatus: "skipped"}, want: "Legacy Debt Scan: skipped"},
		{name: "failed", report: adopt.AdoptReport{BaselineStatus: "failed"}, want: "Legacy Debt Baseline: failed; no usable result"},
		{name: "failed apply", report: adopt.AdoptReport{Errors: []string{"planning failed"}}, want: "[INCOMPLETE]", avoid: "[APPLIED]"},
		{name: "unknown", report: adopt.AdoptReport{BaselineStatus: "future"}, want: "Legacy Debt Baseline: future; no usable result"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatAdoptMCPResult(&tc.report, tc.dryRun)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("missing %q in %s", tc.want, got)
			}
			if tc.avoid != "" && strings.Contains(got, tc.avoid) {
				t.Fatalf("unexpected %q in %s", tc.avoid, got)
			}
		})
	}
}

func TestServer_MemoryRecallRejectsCorruptCache(t *testing.T) {
	srv, root := newFixtureServer(t)
	empty := callTool(t, srv, "standards_memory_recall", map[string]any{"query": "cache"})
	expectText(t, "missing cache", empty, "No local memory facts")
	writeFixtureFile(t, root, hindsight.MemoryFileRel, "{invalid json\n")
	corrupt := callTool(t, srv, "standards_memory_recall", map[string]any{"query": "cache"})
	expectError(t, "corrupt cache", corrupt, "memory recall failed")
	if err := os.Remove(filepath.Join(root, hindsight.MemoryFileRel)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, hindsight.MemoryFileRel), 0o700); err != nil {
		t.Fatal(err)
	}
	directory := callTool(t, srv, "standards_memory_recall", map[string]any{"query": "cache"})
	expectError(t, "directory cache", directory, "memory recall failed")
}

func TestServer_Negative_CompileContext(t *testing.T) {
	srv, root := newFixtureServer(t)

	writeFixtureFile(t, root, "CLAUDE.md", "tampered\n")
	drift := callTool(t, srv, "standards_compile_context", map[string]any{"verify_only": true})
	expectError(t, "drifted verify", drift, "Context verification failed")

	missing := callTool(t, srv, "standards_compile_context", map[string]any{"source": "nope.md", "verify_only": true})
	expectError(t, "missing source", missing, "failed to read source")

	outside := callTool(t, srv, "standards_compile_context", map[string]any{"target_dir": t.TempDir()})
	expectError(t, "outside target", outside, "outside the server root")
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Errorf("fixture CLAUDE.md vanished: %v", err)
	}
}

// ---- needs_report ---------------------------------------------------------------------------

func TestServer_Positive_NeedsReport(t *testing.T) {
	srv, root := newFixtureServer(t)

	res := callTool(t, srv, "standards_needs_report", nil)
	expectText(t, "needs default", res, "Golusoris Migration Report")
	expectText(t, "needs default", res, "Mapping availability")

	// A framework checkout inside the root is inspected domain by domain.
	if err := os.MkdirAll(filepath.Join(root, "fw", "http"), 0o750); err != nil {
		t.Fatal(err)
	}
	withFW := callTool(t, srv, "standards_needs_report", map[string]any{"path": ".", "framework": "fw"})
	expectText(t, "needs framework", withFW, "Golusoris Migration Report")

	bad := callTool(t, srv, "standards_needs_report", map[string]any{"framework": t.TempDir()})
	expectError(t, "framework outside", bad, "outside the server root")
	missing := callTool(t, srv, "standards_needs_report", map[string]any{"path": "missing-dir"})
	if !missing.IsError && !strings.Contains(missing.Content[0].Text, "Migration Report") {
		t.Errorf("unexpected result for missing path: %+v", missing)
	}
}

// ---- adopt ----------------------------------------------------------------------------------

func TestServer_Positive_AdoptDryRunAndApply(t *testing.T) {
	srv, root := newFixtureServer(t)

	dry := callTool(t, srv, "standards_adopt", map[string]any{"dry_run": true})
	expectText(t, "adopt dry run", dry, "SIMULATED (DRY RUN)")
	if _, err := os.Stat(filepath.Join(root, ".paperclip", "harness.json")); err == nil {
		t.Error("dry run wrote the paperclip harness")
	}

	applied := callTool(t, srv, "standards_adopt", map[string]any{"record_baseline": false})
	expectText(t, "adopt apply", applied, "[APPLIED]")
	if strings.Contains(applied.Content[0].Text, "Created Files: 0\n") {
		t.Errorf("apply created nothing:\n%s", applied.Content[0].Text)
	}
	if _, err := os.Stat(filepath.Join(root, ".paperclip", "harness.json")); err != nil {
		t.Errorf("apply did not write the paperclip harness: %v", err)
	}
}

func TestServer_Negative_AdoptConfinement(t *testing.T) {
	srv, _ := newFixtureServer(t)
	other := newFixtureRepo(t)

	blocked := callTool(t, srv, "standards_adopt", map[string]any{"path": other, "dry_run": true})
	expectError(t, "adopt outside", blocked, "outside the server root")

	typed := callTool(t, srv, "standards_adopt", map[string]any{"force": "yes"})
	expectError(t, "adopt typed force", typed, "force must be a boolean")

	notRepo := callTool(t, srv, "standards_adopt", map[string]any{"path": "out-of-tree", "dry_run": true})
	expectError(t, "adopt missing", notRepo, "Adoption failed")

	open, err := NewServerWithOptions(ServerOptions{RootDir: srv.rootDir, Version: "v", AllowOutsideRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	allowed := callTool(t, open, "standards_adopt", map[string]any{"path": other, "dry_run": true})
	expectText(t, "adopt outside allowed", allowed, "SIMULATED (DRY RUN)")

	unpinned := t.TempDir()
	initGitRepo(t, unpinned)
	missing := callTool(t, open, "standards_adopt", map[string]any{"path": unpinned, "dry_run": true})
	expectError(t, "default baseline requires pins", missing, "new lock pins require an explicit verified lock source root")
}

// ---- dogfood --------------------------------------------------------------------------------

func TestServer_Positive_DogfoodOnFixture(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	srv, root := newFixtureServer(t)

	targets := filepath.Join(root, "targets")
	repo := filepath.Join(targets, "leaf")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)

	res := callTool(t, srv, "standards_dogfood", map[string]any{"targets_dir": "targets"})
	expectText(t, "dogfood", res, "Context Sync: true | Invariants Audit: true")
	expectText(t, "dogfood", res, "Local Targets Evaluated: 1")
	expectText(t, "dogfood", res, "Overall Status: true")
}

func TestServer_Negative_DogfoodGuards(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	srv, _ := newFixtureServer(t)

	remote := callTool(t, srv, "standards_dogfood", map[string]any{"benchmark_popular": true})
	expectError(t, "dogfood remote", remote, "remote benchmark clones are disabled")

	outside := callTool(t, srv, "standards_dogfood", map[string]any{"targets_dir": t.TempDir()})
	expectError(t, "dogfood outside", outside, "outside the server root")

	typed := callTool(t, srv, "standards_dogfood", map[string]any{"benchmark_popular": "true"})
	expectError(t, "dogfood typed", typed, "benchmark_popular must be a boolean")

	// With the server flag the argument is accepted at parse time (no clone is run here).
	open, err := NewServerWithOptions(ServerOptions{RootDir: srv.rootDir, Version: "v", AllowRemoteBenchmarks: true})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := open.parseDogfoodOptions(map[string]any{"benchmark_popular": true})
	if err != nil || !opts.BenchmarkPopular {
		t.Errorf("parseDogfoodOptions with flag: opts=%+v err=%v", opts, err)
	}
	if _, err := srv.parseDogfoodOptions(map[string]any{"benchmark_popular": true}); !errors.Is(err, ErrRemoteBenchmarksDisabled) {
		t.Errorf("parseDogfoodOptions without flag: %v", err)
	}
}

// ---- harvest_workstation ----------------------------------------------------------------------

func TestServer_Positive_HarvestWorkstation(t *testing.T) {
	srv, root := newFixtureServer(t)
	dev := filepath.Join(root, "devroot")
	repo := filepath.Join(dev, "org", "leaf")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)

	res := callTool(t, srv, "standards_harvest_workstation", map[string]any{"dev_dir": "devroot"})
	expectText(t, "harvest", res, "Workstation Governance & Fleet Audit")
	expectText(t, "harvest", res, "Dev Repos Total: 1")
}

func TestServer_Negative_HarvestWorkstation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	srv, _ := newFixtureServer(t)

	outside := callTool(t, srv, "standards_harvest_workstation", map[string]any{"dev_dir": t.TempDir()})
	expectError(t, "harvest outside", outside, "outside the server root")
	defaulted := callTool(t, srv, "standards_harvest_workstation", nil)
	expectError(t, "harvest default ~/dev", defaulted, "outside the server root")
	missing := callTool(t, srv, "standards_harvest_workstation", map[string]any{"dev_dir": "nowhere"})
	expectError(t, "harvest missing", missing, "Workstation harvest scan failed")

	open, err := NewServerWithOptions(ServerOptions{RootDir: srv.rootDir, Version: "v", AllowOutsideRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	allowed := callTool(t, open, "standards_harvest_workstation", map[string]any{"dev_dir": t.TempDir()})
	expectText(t, "harvest allowed", allowed, "Dev Repos Total: 0")
}

// ---- package_docs -------------------------------------------------------------------------------

func TestServer_PackageDocs(t *testing.T) {
	srv, root := newFixtureServer(t)

	cat, err := docdistill.LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	cat.Packages["gopkg.in/yaml.v3@v3"] = docdistill.DistilledDoc{PackageName: "gopkg.in/yaml.v3", Version: "v3", RawMarkdown: "# yaml.v3\nUse yaml.Unmarshal.\n", UpdatedAt: time.Now()}
	if err := docdistill.SaveCatalog(root, cat); err != nil {
		t.Fatalf("save catalog: %v", err)
	}

	// Positive: exact and suffix match.
	exact := callTool(t, srv, "standards_package_docs", map[string]any{"package": "gopkg.in/yaml.v3"})
	expectText(t, "docs exact", exact, "Use yaml.Unmarshal")
	suffix := callTool(t, srv, "standards_package_docs", map[string]any{"package": "yaml.v3"})
	expectText(t, "docs suffix", suffix, "Use yaml.Unmarshal")

	// Negative: unknown package, missing argument, typed argument.
	unknown := callTool(t, srv, "standards_package_docs", map[string]any{"package": "left-pad"})
	expectError(t, "docs unknown", unknown, "not found in local catalog")
	missing := callTool(t, srv, "standards_package_docs", nil)
	expectError(t, "docs missing", missing, "package argument is required")
	typed := callTool(t, srv, "standards_package_docs", map[string]any{"package": 1})
	expectError(t, "docs typed", typed, "package must be a string")

	// Boundary: a repository without a catalog answers not-found, not an error.
	empty, err := NewServer(t.TempDir(), "v")
	if err != nil {
		t.Fatal(err)
	}
	none := callTool(t, empty, "standards_package_docs", map[string]any{"package": "gopkg.in/yaml.v3"})
	expectError(t, "docs no catalog", none, "not found in local catalog")
}

// ---- version_audit ------------------------------------------------------------------------------

func TestServer_VersionAudit(t *testing.T) {
	// An empty root has no manifests, so the audit stays offline (toolchain probes only).
	empty, err := NewServer(t.TempDir(), "v")
	if err != nil {
		t.Fatal(err)
	}
	res := callTool(t, empty, "standards_version_audit", nil)
	expectText(t, "version audit", res, "=== Codebase Version Audit:")
	expectText(t, "version audit", res, "Scanned: 0")

	typed := callTool(t, empty, "standards_version_audit", map[string]any{"prerelease": "no"})
	expectError(t, "version audit typed", typed, "prerelease must be a boolean")
	outside := callTool(t, empty, "standards_version_audit", map[string]any{"path": t.TempDir()})
	expectError(t, "version audit outside", outside, "outside the server root")
}

// ---- memory_recall and hindsight_optimize --------------------------------------------------------

func TestServer_MemoryRecallAndHindsightOptimize(t *testing.T) {
	srv, root := newFixtureServer(t)

	none := callTool(t, srv, "standards_memory_recall", map[string]any{"query": "yaml"})
	expectText(t, "recall empty cache", none, "No local memory facts match 'yaml'")

	facts := []hindsight.MemoryFact{{ID: "f1", Category: hindsight.CategoryDependencyDoc, Subject: "gopkg.in/yaml.v3", Statement: "yaml.v3 decodes YAML.", Evidence: "catalog", Tags: []string{"yaml"}}}
	if err := hindsight.SaveLocalCache(root, facts); err != nil {
		t.Fatal(err)
	}
	hit := callTool(t, srv, "standards_memory_recall", map[string]any{"query": "yaml", "category": "dependency_doc"})
	expectText(t, "recall hit", hit, "gopkg.in/yaml.v3")

	missing := callTool(t, srv, "standards_memory_recall", nil)
	expectError(t, "recall missing query", missing, "query argument is required")
	typed := callTool(t, srv, "standards_memory_recall", map[string]any{"query": 3})
	expectError(t, "recall typed query", typed, "query must be a string")

	optimized := callTool(t, srv, "standards_hindsight_optimize", nil)
	expectText(t, "optimize", optimized, "Successfully distilled")
	if _, err := os.Stat(filepath.Join(root, hindsight.MemoryFileRel)); err != nil {
		t.Errorf("distilled cache not written: %v", err)
	}
	outside := callTool(t, srv, "standards_hindsight_optimize", map[string]any{"path": t.TempDir()})
	expectError(t, "optimize outside", outside, "outside the server root")
}

func TestServer_AdoptionStepErrorRetainsIncompleteReport(t *testing.T) {
	srv, root := newFixtureServer(t)
	initial := "# Repository rules\n" + strings.Repeat("Preserve this instruction.\n", 400)
	writeFixtureFile(t, root, "AGENTS.md", initial)
	writeFixtureFile(t, root, "CLAUDE.md", "incumbent context\n")
	result := callTool(t, srv, "standards_adopt", map[string]any{"record_baseline": false})
	expectError(t, "context overflow", result, "context composition cannot produce valid projections")
	if !strings.Contains(result.Content[0].Text, "[INCOMPLETE]") || strings.Contains(result.Content[0].Text, "[APPLIED]") {
		t.Fatalf("failed apply claimed success: %s", result.Content[0].Text)
	}
	content, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil || string(content) != initial {
		t.Fatalf("failed context composition mutated canonical content: %v", err)
	}
}
