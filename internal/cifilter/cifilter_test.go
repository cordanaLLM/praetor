package cifilter_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/cifilter"
	"github.com/cordanaLLM/praetor/internal/config"
)

func TestAnalyzeChangesRejectsMissingOrCancelledContext(t *testing.T) {
	var absent context.Context
	if decision, err := cifilter.AnalyzeChanges(absent, cifilter.FilterOptions{}); err == nil || decision != nil {
		t.Fatalf("nil context must fail: %+v, %v", decision, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := cifilter.AnalyzeChanges(ctx, cifilter.FilterOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context must fail: %v", err)
	}
}

func TestClassifyChangesOverflowRunsHeavyGates(t *testing.T) {
	files := make([]string, 5001)
	for i := range files {
		files[i] = "docs/example.md"
	}
	files[len(files)-1] = "late.go"
	decision := cifilter.MakeDecision(cifilter.ClassifyChanges(files), false)
	if !decision.RunLinters || !decision.RunTests || !decision.RunSecurity || decision.SkipHeavyGates {
		t.Fatalf("source past inspection bound must not skip gates: %+v", decision)
	}
}

func TestClassifyChanges_DocsOnly(t *testing.T) {
	files := []string{
		"docs/guides/getting-started.md",
		"README.md",
		"LICENSE",
		"assets/logo.png",
	}

	cs := cifilter.ClassifyChanges(files)
	if !cs.DocsOnly {
		t.Errorf("expected DocsOnly=true, got false")
	}
	if !cs.DocsChanged {
		t.Errorf("expected DocsChanged=true, got false")
	}
	if cs.CodeChanged {
		t.Errorf("expected CodeChanged=false, got true")
	}
	if cs.ConfigChanged {
		t.Errorf("expected ConfigChanged=false, got true")
	}
}

func TestClassifyChanges_CodeAndTests(t *testing.T) {
	files := []string{
		"internal/cifilter/filter.go",
		"internal/cifilter/cifilter_test.go",
		".github/workflows/ci.yml",
	}

	cs := cifilter.ClassifyChanges(files)
	if cs.DocsOnly {
		t.Errorf("expected DocsOnly=false, got true")
	}
	if !cs.CodeChanged {
		t.Errorf("expected CodeChanged=true, got false")
	}
	if !cs.TestsChanged {
		t.Errorf("expected TestsChanged=true, got false")
	}
	if !cs.ConfigChanged {
		t.Errorf("expected ConfigChanged=true, got false")
	}
}

func TestClassifyChanges_StateOnly(t *testing.T) {
	files := []string{
		".workingdir/STATE.md",
		".workingdir/OPEN.md",
		".workingdir/BUGS.md",
		".workingdir2/evidence/result.md",
	}

	cs := cifilter.ClassifyChanges(files)
	if !cs.StateOnly {
		t.Errorf("expected StateOnly=true, got false")
	}
	if cs.CodeChanged || cs.ConfigChanged || cs.DocsOnly {
		t.Errorf("unexpected flag set on StateOnly change")
	}
}

func TestClassifyChanges_Boundary_Empty(t *testing.T) {
	cs := cifilter.ClassifyChanges([]string{})
	if cs.TotalFiles != 0 {
		t.Errorf("expected TotalFiles=0, got %d", cs.TotalFiles)
	}
	if cs.DocsOnly || cs.StateOnly || cs.CodeChanged {
		t.Errorf("expected all false on empty change set")
	}
}

func TestMakeDecision_DocsOnly(t *testing.T) {
	cs := &cifilter.ChangeSet{
		TotalFiles:  2,
		DocsChanged: true,
		DocsOnly:    true,
	}

	dec := cifilter.MakeDecision(cs, false)
	if dec.RunTests {
		t.Errorf("expected RunTests=false for docs-only, got true")
	}
	if dec.RunLinters {
		t.Errorf("expected RunLinters=false for docs-only, got true")
	}
	if dec.RunSecurity {
		t.Errorf("expected RunSecurity=false for docs-only, got true")
	}
	if !dec.RunAudit {
		t.Errorf("expected RunAudit=true for docs-only, got false")
	}
	if !dec.RunDocsOnly {
		t.Errorf("expected RunDocsOnly=true for docs-only, got false")
	}
	if !dec.RunDocs {
		t.Errorf("expected RunDocs=true for docs-only, got false")
	}
	if !dec.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=true for docs-only, got false")
	}
	const wantReason = "pure documentation change; running audit and documentation governance; skipping heavy race and security gates"
	if dec.Reason != wantReason {
		t.Errorf("docs-only evidence reason = %q, want %q", dec.Reason, wantReason)
	}
}

func TestMakeDecision_CodeChanged(t *testing.T) {
	cs := &cifilter.ChangeSet{
		TotalFiles:  1,
		CodeChanged: true,
	}

	dec := cifilter.MakeDecision(cs, false)
	if !dec.RunTests {
		t.Errorf("expected RunTests=true for code changes, got false")
	}
	if !dec.RunLinters {
		t.Errorf("expected RunLinters=true for code changes, got false")
	}
	if !dec.RunSecurity {
		t.Errorf("expected RunSecurity=true for code changes, got false")
	}
	if dec.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=false for code changes, got true")
	}
}

func TestMakeDecision_ForceAll(t *testing.T) {
	cs := &cifilter.ChangeSet{
		TotalFiles:  1,
		DocsChanged: true,
		DocsOnly:    true,
	}

	dec := cifilter.MakeDecision(cs, true)
	if !dec.RunTests || !dec.RunLinters || !dec.RunSecurity {
		t.Errorf("expected all gates enabled when ForceAll=true")
	}
	if dec.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=false when ForceAll=true")
	}
}

func TestMakeDecision_StateOnly(t *testing.T) {
	cs := &cifilter.ChangeSet{
		TotalFiles: 1,
		StateOnly:  true,
	}

	dec := cifilter.MakeDecision(cs, false)
	if dec.RunTests || dec.RunLinters || dec.RunSecurity || dec.RunAudit || dec.RunDocs {
		t.Errorf("expected all gates skipped when StateOnly=true")
	}
	if !dec.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=true when StateOnly=true")
	}
}

func TestFilterDecision_Formatting(t *testing.T) {
	dec := &cifilter.FilterDecision{
		RunTests:       true,
		RunLinters:     false,
		RunSecurity:    true,
		RunAudit:       true,
		RunDocsOnly:    false,
		SkipHeavyGates: false,
		Reason:         "test run",
	}

	out := dec.FormatGitHubOutput()
	if !strings.Contains(out, "run_tests=true") {
		t.Errorf("expected run_tests=true in GitHub output, got: %s", out)
	}
	if !strings.Contains(out, "run_linters=false") {
		t.Errorf("expected run_linters=false in GitHub output, got: %s", out)
	}
	if !strings.Contains(out, "run_docs=false") {
		t.Errorf("expected run_docs=false in GitHub output, got: %s", out)
	}

	jsonBytes, err := dec.ToJSON()
	if err != nil {
		t.Fatalf("failed serializing to JSON: %v", err)
	}
	if len(jsonBytes) == 0 {
		t.Errorf("expected non-empty JSON output")
	}
}

func TestAnalyzeChanges_FallbackOnInvalidDir(t *testing.T) {
	ctx := context.Background()
	opts := cifilter.FilterOptions{
		RepoDir: "/nonexistent/invalid/directory/xyz",
	}

	dec, err := cifilter.AnalyzeChanges(ctx, opts)
	if err != nil {
		t.Fatalf("expected fallback decision, got error: %v", err)
	}
	if !dec.RunTests || !dec.RunLinters || !dec.RunSecurity {
		t.Errorf("expected fail-safe execution (all true) on git diff error")
	}
}

func TestConfigClassificationCoversEditorLockAndTypeScriptConfigs(t *testing.T) {
	for _, path := range []string{"package-lock.json", "tsconfig.json", "tsconfig.editor.json", "TSConfig.custom.JSON"} {
		decision := cifilter.MakeDecision(cifilter.ClassifyChanges([]string{path}), false)
		if !decision.RunTests || !decision.RunLinters || !decision.RunSecurity || !decision.RunDocs {
			t.Fatalf("%s did not select editor/config validation: %+v", path, decision)
		}
	}
}

func TestDocsOnlyStillSkipsHeavyGates(t *testing.T) {
	decision := cifilter.MakeDecision(cifilter.ClassifyChanges([]string{"docs/editor.md"}), false)
	if decision.RunTests || !decision.RunAudit || !decision.RunDocs || !decision.SkipHeavyGates || !decision.RunDocsOnly {
		t.Fatalf("documentation change changed policy: %+v", decision)
	}
}

func TestMarkdownAndGateRunnerExtensionsSelectDocumentation(t *testing.T) {
	for _, path := range []string{
		"GUIDE.MD", "docs/reference.markdown", "docs/reference.MARKDOWN",
		"docs/page.mdx", "content/PAGE.MDX", "templates/README.md.tmpl",
		"templates/reference.MARKDOWN.TMPL", "templates/Card.MDX.TMPL",
	} {
		decision := cifilter.MakeDecision(cifilter.ClassifyChanges([]string{path}), false)
		if !decision.RunDocs || !decision.RunDocsOnly || !decision.SkipHeavyGates {
			t.Fatalf("%s did not select the lightweight documentation gate: %+v", path, decision)
		}
	}
	for _, path := range []string{
		"tools/markdownlint/verify.mjs", "tools/markdownlint/rule.cjs",
		"tools/docsurface/catalog.mjs", "tools/docsurface/verify.mjs",
	} {
		decision := cifilter.MakeDecision(cifilter.ClassifyChanges([]string{path}), false)
		if !decision.RunDocs || !decision.RunTests || decision.SkipHeavyGates {
			t.Fatalf("%s did not select full verification: %+v", path, decision)
		}
	}
}

// CI runs race, lint and security checks through make verify-all only when heavy
// gates are selected. Any new selective decision must preserve this contract.
func TestLightweightDecisionsNeverRequireHeavyChecks(t *testing.T) {
	for flags := range 128 {
		changes := &cifilter.ChangeSet{
			TotalFiles:    1,
			CodeChanged:   flags&1 != 0,
			TestsChanged:  flags&2 != 0,
			DocsChanged:   flags&4 != 0,
			ConfigChanged: flags&8 != 0,
			AgentChanged:  flags&16 != 0,
			StateOnly:     flags&32 != 0,
			DocsOnly:      flags&64 != 0,
		}
		decision := cifilter.MakeDecision(changes, false)
		if decision.SkipHeavyGates && (decision.RunTests || decision.RunLinters || decision.RunSecurity) {
			t.Fatalf("CI would omit requested checks for flags %d: %+v", flags, decision)
		}
	}
}

// Issue #256: an AGENTS.md-only change set was classified DocsOnly, so
// ci.yml's run_context_sync gate never fired and the HISS-16
// compile-context --verify step silently skipped.
func TestClassifyChanges_AgentOnlyRunsContextSync(t *testing.T) {
	cs := cifilter.ClassifyChanges([]string{"AGENTS.md"})
	if cs.DocsOnly {
		t.Errorf("expected DocsOnly=false for an agent-only change, got true")
	}
	if !cs.AgentChanged {
		t.Errorf("expected AgentChanged=true, got false")
	}

	decision := cifilter.MakeDecision(cs, false)
	if !decision.RunContextSync {
		t.Errorf("expected RunContextSync=true for AGENTS.md-only, got false")
	}
	if decision.RunDocsOnly {
		t.Errorf("expected RunDocsOnly=false for AGENTS.md-only, got true")
	}
	if !decision.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=true for AGENTS.md-only (no code/config), got false")
	}
}

// Negative: a plain documentation change must stay on the docs-only path and
// keep skipping context sync.
func TestClassifyChanges_DocsOnlyStillSkipsContextSync(t *testing.T) {
	cs := cifilter.ClassifyChanges([]string{"docs/guide.md"})
	if !cs.DocsOnly {
		t.Errorf("expected DocsOnly=true for a plain docs change, got false")
	}
	if cs.AgentChanged {
		t.Errorf("expected AgentChanged=false for a plain docs change, got true")
	}

	decision := cifilter.MakeDecision(cs, false)
	if decision.RunContextSync {
		t.Errorf("expected RunContextSync=false for docs-only, got true")
	}
	if !decision.RunDocsOnly {
		t.Errorf("expected RunDocsOnly=true for docs-only, got false")
	}
}

// Boundary: a mix of an agent file and a plain doc must still run context
// sync — the agent path outweighs the docs path in the change set.
func TestClassifyChanges_AgentAndDocsMixRunsContextSync(t *testing.T) {
	cs := cifilter.ClassifyChanges([]string{"AGENTS.md", "docs/guide.md"})
	if cs.DocsOnly {
		t.Errorf("expected DocsOnly=false for a mixed agent+docs change, got true")
	}
	if !cs.AgentChanged || !cs.DocsChanged {
		t.Errorf("expected both AgentChanged and DocsChanged true, got AgentChanged=%t DocsChanged=%t", cs.AgentChanged, cs.DocsChanged)
	}

	decision := cifilter.MakeDecision(cs, false)
	if !decision.RunContextSync {
		t.Errorf("expected RunContextSync=true for a mixed agent+docs change, got false")
	}
}

// Boundary: a skill file under .agents/ is an agent path, not documentation,
// even though it ends in .md.
func TestClassifyChanges_AgentsDirSkillRunsContextSync(t *testing.T) {
	cs := cifilter.ClassifyChanges([]string{".agents/skills/x/SKILL.md"})
	if cs.DocsOnly {
		t.Errorf("expected DocsOnly=false for a .agents/ skill file, got true")
	}
	if !cs.AgentChanged {
		t.Errorf("expected AgentChanged=true for a .agents/ skill file, got false")
	}

	decision := cifilter.MakeDecision(cs, false)
	if !decision.RunContextSync {
		t.Errorf("expected RunContextSync=true for a .agents/ skill file, got false")
	}
}

// Boundary: isAgent matches CLAUDE.md by base name regardless of directory
// depth, so a subdirectory copy must also run context sync, not fall
// through to docs-only. Pins current isAgent semantics.
func TestClassifyChanges_ClaudeMdInSubdirectoryRunsContextSync(t *testing.T) {
	cs := cifilter.ClassifyChanges([]string{"services/worker/CLAUDE.md"})
	if cs.DocsOnly {
		t.Errorf("expected DocsOnly=false for a nested CLAUDE.md, got true")
	}
	if !cs.AgentChanged {
		t.Errorf("expected AgentChanged=true for a nested CLAUDE.md, got false")
	}

	decision := cifilter.MakeDecision(cs, false)
	if !decision.RunContextSync {
		t.Errorf("expected RunContextSync=true for a nested CLAUDE.md, got false")
	}
}

// Negative: a path whose extension no classifier recognises is neither documentation nor
// session state, so it must never ride the docs-only or state-only path. It fails closed
// (BUG-236, BUG-241): alone or mixed with a doc or a state file it selects tests, linters,
// security and context sync, and the reason names the unclassified kind.
func TestClassifyChanges_UnrecognisedExtensionIsNotDocsOrState(t *testing.T) {
	for _, files := range [][]string{
		{"scripts/release.ps1"},
		{"internal/data/fixture.bin"},
		{"docs/guide.md", "scripts/release.ps1"},
		{".workingdir/STATE.md", "internal/data/fixture.bin"},
	} {
		cs := cifilter.ClassifyChanges(files)
		if cs.DocsOnly || cs.StateOnly {
			t.Errorf("%v: unrecognised path classified DocsOnly=%t StateOnly=%t", files, cs.DocsOnly, cs.StateOnly)
		}
		if !cs.UnclassifiedChanged || !cs.ConfigChanged {
			t.Errorf("%v: unrecognised path must fail closed as configuration, got %+v", files, cs)
		}
		decision := cifilter.MakeDecision(cs, false)
		if decision.RunDocsOnly || !decision.RunAudit || decision.SkipHeavyGates ||
			!decision.RunTests || !decision.RunLinters || !decision.RunSecurity || !decision.RunContextSync ||
			!strings.Contains(decision.Reason, "unclassified") {
			t.Errorf("%v: expected the fail-closed targeted matrix, got %+v", files, decision)
		}
	}
}

// Negative (BUG-236): each file kind that no classifier named used to switch every heavy
// gate off. Every one of them now fails closed. An extensionless script and a systemd unit stay
// here although the HISS scanner may read them: it claims them from their bytes, which a diff
// classifier never reads, so they never widen hiss.SupportsExtension (#182).
func TestUnclassifiedFileKindsRunHeavyGates(t *testing.T) {
	for _, path := range []string{
		"Dockerfile", "build/Dockerfile", "scripts/release.ps1", "templates/ci.yml.tmpl", "bin/provision", "units/app.service",
		"tools/tool.toml", "internal/data/fixture.json", ".gitignore",
		// The editor files `make editors-verify` checks (BUG-439): a change to one alone must
		// reach verify-all, which is where that gate runs.
		".editorconfig", ".vscode/extensions.json", ".vscode/settings.json",
	} {
		decision := cifilter.MakeDecision(cifilter.ClassifyChanges([]string{path}), false)
		if !decision.RunTests || !decision.RunLinters || !decision.RunSecurity || decision.SkipHeavyGates ||
			!decision.ChangeSet.UnclassifiedChanged {
			t.Errorf("%s did not fail closed: %+v", path, decision)
		}
	}
	// Boundary: a recognised source or config kind is not reported as unclassified.
	for _, path := range []string{"main.go", "go.mod", ".github/workflows/ci.yml", "package.json"} {
		if cs := cifilter.ClassifyChanges([]string{path}); cs.UnclassifiedChanged {
			t.Errorf("%s is a recognised kind but was reported unclassified", path)
		}
	}
}

// Negative (BUG-562): a dependency or build manifest ending in .txt changes what CI
// installs or builds, so it selects the security and test gates even under docs/.
func TestBuildManifestTextIsConfiguration(t *testing.T) {
	for _, path := range []string{
		"requirements.txt", ".config/semgrep/requirements.txt", "docs/presets/mkdocs/requirements.txt",
		"requirements-dev.txt", "constraints.txt", "CMakeLists.txt", "native/cmakelists.TXT",
	} {
		cs := cifilter.ClassifyChanges([]string{path})
		decision := cifilter.MakeDecision(cs, false)
		if cs.DocsOnly || !cs.ConfigChanged || cs.UnclassifiedChanged || !decision.RunTests || !decision.RunSecurity {
			t.Errorf("%s must classify as configuration, got %+v / %+v", path, cs, decision)
		}
	}
	// Boundary: a plain text document keeps the documentation path.
	for _, path := range []string{"docs/llms.txt", "docs/llms-full.txt", "LICENSES/EUPL-1.2.txt", "notes.txt"} {
		if cs := cifilter.ClassifyChanges([]string{path}); !cs.DocsOnly {
			t.Errorf("%s must stay documentation, got %+v", path, cs)
		}
	}
}

// Negative: a dependency or build manifest under docs/ (a documentation preset's package.json,
// lock, tsconfig, a pip-compile input) changes what CI installs and is read by the credits gate,
// so a pull request touching one with the credits page selects the test and security gates; a
// root requirements.in is a recognised configuration kind, not an unclassified one.
func TestDependencyManifestUnderDocsIsConfiguration(t *testing.T) {
	for _, path := range []string{
		"docs/presets/starlight/package.json", "docs/presets/starlight/package-lock.json",
		"docs/presets/starlight/tsconfig.json", "docs/presets/mkdocs/requirements.in", "docs/site/Makefile",
		"docs/site/go.mod", "docs/site/pnpm-lock.yaml", "requirements.in", ".config/lint/requirements-dev.in",
	} {
		cs := cifilter.ClassifyChanges([]string{path})
		decision := cifilter.MakeDecision(cs, false)
		if cs.DocsOnly || !cs.ConfigChanged || cs.UnclassifiedChanged || !decision.RunTests || !decision.RunSecurity || decision.SkipHeavyGates {
			t.Errorf("%s must classify as configuration, got %+v / %+v", path, cs, decision)
		}
	}
	// Positive: the credits list, its page and a preset manifest together run the tests.
	cs := cifilter.ClassifyChanges([]string{"docs/credits.yaml", "docs/credits.md", "docs/presets/starlight/package.json"})
	if decision := cifilter.MakeDecision(cs, false); cs.DocsOnly || !decision.RunTests || decision.SkipHeavyGates {
		t.Errorf("a preset manifest beside the credits page took the documentation path: %+v / %+v", cs, decision)
	}
	// Boundary: a document that only names a manifest kind keeps the documentation path.
	for _, path := range []string{"docs/requirements.md", "docs/guides/tsconfig.md", "docs/package-json.md", "docs/credits.md"} {
		if cs := cifilter.ClassifyChanges([]string{path}); !cs.DocsOnly {
			t.Errorf("%s must stay documentation, got %+v", path, cs)
		}
	}
}

// The credits gate (make credits-check, the internal/supplychain tests) reads docs/credits.yaml,
// the credits page, THIRD-PARTY-NOTICES.md, every canonical persona and skill with its plugin copy,
// and the manifests of the dependency inventory. Its light CI step runs on run_docs
// (.github/workflows/ci.yml), and every heavy run reaches it through verify-all.
func creditsGateRuns(d *cifilter.FilterDecision) bool {
	return !d.SkipHeavyGates || d.RunDocs
}

// Negative: a pull request touching only a persona or skill was classed agent-only, which
// selected context sync but not the documentation gates, so a skill neither derived nor marked
// original first failed the credits gate on main. Every agent-only and docs-only change set of a
// credits input selects the gate; every manifest the inventory reads selects the heavy run.
func TestCreditsGateInputsSelectTheCreditsGate(t *testing.T) {
	for _, files := range [][]string{
		{".agents/skills/new-skill/SKILL.md", ".agents/plugins/praetor/skills/new-skill/SKILL.md"},
		{".agents/agents/praetor-new.md"}, {".agents/plugins/praetor/agents/praetor-new.md"},
		{"AGENTS.md"}, {"docs/credits.yaml"}, {"docs/credits.md"}, {"THIRD-PARTY-NOTICES.md"},
		{"docs/credits.yaml", "docs/credits.md", ".agents/skills/caveman/SKILL.md"},
	} {
		decision := cifilter.MakeDecision(cifilter.ClassifyChanges(files), false)
		if !decision.SkipHeavyGates || !creditsGateRuns(decision) {
			t.Errorf("%v must take the light run with the credits gate, got %+v", files, decision)
		}
	}
	for _, path := range []string{
		"go.mod", "tools/go/go.mod", "docs/presets/starlight/package.json", "docs/presets/mkdocs/requirements.in",
		".config/semgrep/requirements.txt", ".github/workflows/ci.yml", ".devcontainer/devcontainer.json", "docker/dev/Dockerfile",
	} {
		decision := cifilter.MakeDecision(cifilter.ClassifyChanges([]string{path}), false)
		if decision.SkipHeavyGates || !decision.RunTests || !creditsGateRuns(decision) {
			t.Errorf("%s feeds the dependency inventory and must take the heavy run, got %+v", path, decision)
		}
	}
	// Boundary: private session state alone feeds no gate, so it selects none.
	if decision := cifilter.MakeDecision(cifilter.ClassifyChanges([]string{".workingdir/OPEN.md"}), false); decision.RunDocs || decision.RunTests {
		t.Errorf("state-only change selected gates: %+v", decision)
	}
}

// Negative (BUG-242): every file compile-context writes is agent text, so a vendor-only
// change runs the HISS-16 compile-context --verify gate instead of the docs-only path.
func TestCompiledVendorFilesRunContextSync(t *testing.T) {
	for _, path := range []string{
		".cursor/rules/hiss-invariants.mdc", ".windsurfrules", ".github/copilot-instructions.md",
		".gemini/GEMINI.md", ".codex/rules.md", "CLAUDE.md",
	} {
		cs := cifilter.ClassifyChanges([]string{path})
		decision := cifilter.MakeDecision(cs, false)
		if cs.DocsOnly || !cs.AgentChanged || cs.UnclassifiedChanged || !decision.RunContextSync || decision.RunDocsOnly {
			t.Errorf("%s must classify as an agent file, got %+v / %+v", path, cs, decision)
		}
	}
	// Boundary: the vendor paths match exactly, so a same-named file elsewhere is not a
	// compiled target; a Markdown copy stays documentation.
	if cs := cifilter.ClassifyChanges([]string{"docs/examples/.gemini/GEMINI.md"}); cs.AgentChanged || !cs.DocsOnly {
		t.Errorf("a GEMINI.md outside .gemini/ must stay documentation, got %+v", cs)
	}
}

// overrides.ci switches (BUG-652) only ever add gates: each false switch forces the full
// matrix where the default would skip heavy gates, and leaves a code change unchanged.
func TestMakePolicyDecision(t *testing.T) {
	docs := cifilter.ClassifyChanges([]string{"docs/guide.md"})
	state := cifilter.ClassifyChanges([]string{".workingdir/STATE.md"})
	code := cifilter.ClassifyChanges([]string{"main.go"})

	// Positive: the default policy keeps the selective decisions.
	defaults := config.DefaultCIPolicy()
	if d := cifilter.MakePolicyDecision(docs, false, defaults); !d.RunDocsOnly || !d.SkipHeavyGates {
		t.Fatalf("default policy must keep docs-only, got %+v", d)
	}
	// Negative: diff-aware filtering off runs the full matrix for every change set.
	off := config.CIPolicy{DiffAwareFiltering: false, SkipHeavyGatesOnDocsOrState: true}
	for _, cs := range []*cifilter.ChangeSet{docs, state, code} {
		if d := cifilter.MakePolicyDecision(cs, false, off); d.SkipHeavyGates || !d.RunTests || !d.RunSecurity || !d.RunContextSync {
			t.Fatalf("diff_aware_filtering=false must run the full matrix, got %+v", d)
		}
	}
	// Negative: heavy-gate skipping off runs the full matrix for docs-only and state-only.
	noSkip := config.CIPolicy{DiffAwareFiltering: true, SkipHeavyGatesOnDocsOrState: false}
	for _, cs := range []*cifilter.ChangeSet{docs, state} {
		if d := cifilter.MakePolicyDecision(cs, false, noSkip); d.SkipHeavyGates || d.RunDocsOnly || !d.RunTests {
			t.Fatalf("skip_heavy_gates_on_docs_or_state=false must run the full matrix, got %+v", d)
		}
	}
	// Boundary: a code change is already a heavy decision; the switch leaves it targeted.
	if d := cifilter.MakePolicyDecision(code, false, noSkip); !strings.Contains(d.Reason, "targeted") || !d.RunTests {
		t.Fatalf("a code change must keep the targeted matrix, got %+v", d)
	}
}

// Positive: .tsx and .jsx are code extensions. Before they were, a vendored React source file
// fell through as unclassified: tests still ran (fail closed), but the change was reported as
// configuration. Each extension is checked on its own so neither can ride on the other.
func TestClassifyChanges_TsxJsxAreCode(t *testing.T) {
	for _, path := range []string{"tools/figures/third_party/interfig/upstream/src/index.tsx", "web/src/utils.jsx"} {
		cs := cifilter.ClassifyChanges([]string{path})
		if !cs.CodeChanged || cs.UnclassifiedChanged || cs.ConfigChanged || cs.DocsOnly {
			t.Errorf("%s: want code only, got code=%v unclassified=%v config=%v docsOnly=%v",
				path, cs.CodeChanged, cs.UnclassifiedChanged, cs.ConfigChanged, cs.DocsOnly)
		}
		decision := cifilter.MakeDecision(cs, false)
		if !decision.RunTests || decision.RunDocsOnly || decision.SkipHeavyGates {
			t.Errorf("%s: a code change must keep the targeted matrix, got %+v", path, decision)
		}
	}
}

// Positive (#589, #182): a Svelte component, the TypeScript module suffixes and shell scripts are
// code because the HISS scanner reads them; they come from its table rather than from a second
// list here.
func TestClassifyChanges_ScannedScriptKindsAreCode(t *testing.T) {
	for _, path := range []string{"ui/src/App.svelte", "lib/index.mts", "lib/index.cts", "scripts/release.sh", "tools/setup.bash"} {
		if cs := cifilter.ClassifyChanges([]string{path}); !cs.CodeChanged || cs.UnclassifiedChanged {
			t.Errorf("%s: want code, got code=%v unclassified=%v", path, cs.CodeChanged, cs.UnclassifiedChanged)
		}
	}
}

// Negative: .md only stays docs-only.
func TestClassifyChanges_MdStaysDocsOnly(t *testing.T) {
	cs := cifilter.ClassifyChanges([]string{"docs/guides/figures.md"})
	if !cs.DocsOnly {
		t.Errorf("expected DocsOnly=true for .md only, got false")
	}
	decision := cifilter.MakeDecision(cs, false)
	if decision.RunTests {
		t.Errorf("expected RunTests=false for docs-only, got true")
	}
	if !decision.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=true for docs-only, got false")
	}
}

// Boundary: a .tsx under docs/ keeps the docs/ prefix rule, and an extension that only starts
// with .tsx or .jsx is not code.
func TestClassifyChanges_TsxUnderDocsStaysDocsOnly(t *testing.T) {
	for _, path := range []string{"web/src/index.tsxx", "web/src/utils.jsxz"} {
		if cs := cifilter.ClassifyChanges([]string{path}); cs.CodeChanged || !cs.UnclassifiedChanged {
			t.Errorf("%s: want unclassified, got code=%v unclassified=%v", path, cs.CodeChanged, cs.UnclassifiedChanged)
		}
	}

	cs := cifilter.ClassifyChanges([]string{"docs/assets/example.tsx"})
	if !cs.DocsOnly {
		t.Errorf("expected DocsOnly=true for .tsx under docs/, got false")
	}
	if cs.CodeChanged {
		t.Errorf("expected CodeChanged=false for .tsx under docs/, got true")
	}
	if !cs.DocsChanged {
		t.Errorf("expected DocsChanged=true for .tsx under docs/, got false")
	}

	decision := cifilter.MakeDecision(cs, false)
	if decision.RunTests {
		t.Errorf("expected RunTests=false for .tsx under docs/, got true")
	}
	if !decision.SkipHeavyGates {
		t.Errorf("expected SkipHeavyGates=true for .tsx under docs/, got false")
	}
}
