package compiler

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// fixtureSource is a canonical AGENTS.md without a text register section, so a write that gets
// as far as the splice changes it.
const fixtureSource = "# Policy\n"

// TestVerifyCompiledContext_IndependentTargetFailures checks that unreadable vendor output
// cannot mask another vendor's drift, or read-only drift combined with lint failure.
func TestVerifyCompiledContext_IndependentTargetFailures(t *testing.T) {
	root := t.TempDir()
	content := "# Policy\n\n" + strings.Repeat(lintProseAgents, 8)
	source := writeRegisterFixture(t, root, "AGENTS.md", content)
	tr := NewTranspiler()
	res, err := tr.CompileContext(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) < 2 {
		t.Fatal("fixture needs at least two vendor targets")
	}
	if err := tr.WriteOutputsContext(t.Context(), res, root); err != nil {
		t.Fatal(err)
	}
	missing, drift := res.Files[0].RelativePath, res.Files[1].RelativePath
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(missing))); err != nil {
		t.Fatal(err)
	}
	writeRegisterFixture(t, root, drift, "drifted\n")
	writeRegisterFixture(t, root, ReadOnlyFile, "drifted\n")
	err = VerifyCompiledContext(t.Context(), io.Discard, tr, source, root)
	if !errors.Is(err, ErrContextProse) || err == nil {
		t.Fatalf("want lint failure: %v", err)
	}
	for _, want := range []string{missing, "target " + drift + " is out of sync", "target " + ReadOnlyFile + " is out of sync"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing independent failure %s: %v", want, err)
		}
	}
}

// Each changed verifier's drift survives its canonical lint failure, before the lint text.
func TestVerifyCompiledContext_SiblingDriftAndLint(t *testing.T) {
	cases := []struct{ name, canonical, target, header string }{
		{"vendor", "AGENTS.md", "CLAUDE.md", "# Policy\n"},
		{"persona", CanonicalAgentsRel + "/reviewer.md", ".claude/agents/reviewer.md",
			"---\nname: reviewer\ndescription: Review.\nmodel: pro\n---\n# Reviewer\n"},
		{"plugin entry", CanonicalSkillRel("hiss-audit"), SkillEntryRel(PluginSkillsRel, "hiss-audit"),
			"---\nname: hiss-audit\ndescription: Audit.\n---\n# Audit\n"},
		{"plugin licence", CanonicalSkillRel("hiss-audit"), SkillLicenseRel(PluginSkillsRel, "hiss-audit"),
			"---\nname: hiss-audit\ndescription: Audit.\n---\n# Audit\n"},
		{"client skill", CanonicalSkillRel("caveman"), SkillEntryRel(".claude/skills", "caveman"),
			"---\nname: caveman\ndescription: Format.\n---\n# Format\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSiblingDriftAndLint(t, tc.canonical, tc.target, tc.header)
		})
	}
}

// runSiblingDriftAndLint runs one verifier over the four combinations of a drifted target and a failing lint.
func runSiblingDriftAndLint(t *testing.T, canonical, target, header string) {
	t.Helper()
	cases := []struct {
		name        string
		drift, lint bool
	}{
		{name: "clean"},
		{name: "drift only", drift: true},
		{name: "lint only", lint: true},
		{name: "drift and lint", drift: true, lint: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := skillFixture(t)
			writeRegisterSkill(t, root, "caveman")
			writeRegisterFixture(t, root, CanonicalSkillLicenseRel("hiss-audit"), "licence\n")
			writeRegisterFixture(t, root, "AGENTS.md", "# Policy\n")
			content := header
			if tc.lint {
				content += strings.Repeat(lintProseAgents, 8)
			}
			writeRegisterFixture(t, root, canonical, content)
			source := filepath.Join(root, "AGENTS.md")
			if _, err := CompileVendorTargets(t.Context(), io.Discard, NewTranspiler(), source, root); err != nil {
				t.Fatal(err)
			}
			if _, err := CompileAgentSurfaces(t.Context(), io.Discard, root); err != nil {
				t.Fatal(err)
			}
			if tc.drift {
				writeRegisterFixture(t, root, target, "drifted\n")
			}
			err := VerifyCompiledContext(t.Context(), io.Discard, NewTranspiler(), source, root)
			assertSiblingDriftAndLint(t, err, target, tc.drift, tc.lint)
		})
	}
}

// assertSiblingDriftAndLint checks that a clean tree passes and that drift and lint each stay in the error, drift first.
func assertSiblingDriftAndLint(t *testing.T, err error, target string, drift, lint bool) {
	t.Helper()
	if !drift && !lint {
		if err != nil {
			t.Fatalf("clean projections: %v", err)
		}
		return
	}
	lintErr := ErrAgentTextProse
	prefix := ErrAgentProjectionDrift.Error() + ": " + target
	if target == "CLAUDE.md" {
		lintErr = ErrContextProse
		prefix = "target " + target + " is out of sync"
	}
	if err == nil || errors.Is(err, lintErr) != lint {
		t.Fatalf("lint chain: %v; want lint=%v drift=%v", err, lint, drift)
	}
	driftAt := strings.Index(err.Error(), prefix)
	if (driftAt >= 0) != drift {
		t.Fatalf("drift diagnostic %q: %v; want drift=%v", prefix, err, drift)
	}
	if lint && driftAt > strings.Index(err.Error(), lintErr.Error()) {
		t.Errorf("drift must precede lint: %v", err)
	}
}

// Matching targets return the complete compiled result to callers.
func TestVerifyCompiled_Positive_MatchingTargets(t *testing.T) {
	root := t.TempDir()
	source := writeRegisterFixture(t, root, "AGENTS.md", "# Policy\n")
	tr := NewTranspiler()
	want, err := tr.CompileContext(t.Context(), source)
	if err != nil || len(want.Files) == 0 {
		t.Fatalf("compile: %v, %v", want, err)
	}
	if err := tr.WriteOutputsContext(t.Context(), want, root); err != nil {
		t.Fatal(err)
	}
	res, err := tr.VerifyCompiled(t.Context(), source, root)
	if err != nil || res == nil {
		t.Fatalf("matching targets: %v, %v", res, err)
	}
	if len(res.Files) != len(want.Files) || res.SourcePath != source {
		t.Errorf("verified result = %+v; want %d files from %s", res, len(want.Files), source)
	}
}

// Compile and source failures forbid every target read, even when all targets would drift.
func TestVerifyCompiled_Boundary_UpstreamFailure(t *testing.T) {
	cases := []struct {
		name, content string
		missing       bool
		maxLines      int
	}{
		{name: "missing source", missing: true},
		{name: "invalid text", content: "\xff"},
		{name: "empty source"},
		{name: "compile budget", content: "# Policy\n\nRun checks.\n", maxLines: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "AGENTS.md")
			if !tc.missing {
				writeRegisterFixture(t, root, "AGENTS.md", tc.content)
			}
			tr := NewTranspiler()
			if tc.maxLines > 0 {
				tr.MaxLines = tc.maxLines
			}
			reads := 0
			tr.readTarget = func(context.Context, string, string) ([]byte, error) {
				reads++
				return []byte("drifted\n"), nil
			}
			res, err := tr.VerifyCompiled(t.Context(), source, root)
			if res != nil || err == nil || reads != 0 {
				t.Fatalf("upstream failure: result=%v reads=%d err=%v", res, reads, err)
			}
		})
	}
}

// The complete selected target set remains observable after any first failure.
func TestVerifyCompiled_Boundary_AllTargetsFail(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "all drifted"
		if missing {
			name = "all missing"
		}
		t.Run(name, func(t *testing.T) {
			verifyAllVendorTargetsFail(t, missing)
		})
	}
}

// verifyAllVendorTargetsFail checks that VerifyCompiled reads every vendor target and names each one that is missing or drifted.
func verifyAllVendorTargetsFail(t *testing.T, missing bool) {
	t.Helper()
	root := t.TempDir()
	source := writeRegisterFixture(t, root, "AGENTS.md", "# Policy\n")
	tr := NewTranspiler()
	want, err := tr.CompileContext(t.Context(), source)
	if err != nil || len(want.Files) == 0 {
		t.Fatalf("compile: %v, %v", want, err)
	}
	var reads []string
	tr.readTarget = func(_ context.Context, _, rel string) ([]byte, error) {
		reads = append(reads, rel)
		if missing {
			return nil, os.ErrNotExist
		}
		return []byte("drifted\n"), nil
	}
	res, err := tr.VerifyCompiled(t.Context(), source, root)
	if res != nil || err == nil || len(reads) != len(want.Files) {
		t.Fatalf("result=%v reads=%v err=%v", res, reads, err)
	}
	for _, file := range want.Files {
		if !strings.Contains(err.Error(), "target "+file.RelativePath+" ") {
			t.Errorf("missing target diagnostic: %s", file.RelativePath)
		}
	}
	if errors.Is(err, os.ErrNotExist) != missing {
		t.Errorf("missing chain: %v; want %v", err, missing)
	}
}

// compileFixture runs CompileContextProjections over root, the write the CLI's compile-context
// and the MCP standards_compile_context tool run, with root/AGENTS.md as the source. It writes
// fixtureSource there first when root has none.
func compileFixture(t *testing.T, root string) error {
	t.Helper()
	source := filepath.Join(root, "AGENTS.md")
	if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
		writeCanonicalFixture(t, source, fixtureSource)
	}
	return CompileContextProjections(t.Context(), io.Discard, NewTranspiler(), source, root)
}

// Positive: a write ships every declared skill into the plugin, and verify then accepts it. The
// plugin shipped none, while praetor's own harvester already read plugin skills from
// <plugin>/skills, so installing the plugin delivered the personas and not one skill. The write
// also splices the text register into the source.
func TestCompileContextProjections_Positive_ShipsEveryDeclaredSkill(t *testing.T) {
	root := skillFixture(t)
	if err := compileFixture(t, root); err != nil {
		t.Fatalf("compile: %v", err)
	}
	verified, err := VerifyPluginSkills(t.Context(), root)
	if err != nil || verified != 2 {
		t.Fatalf("freshly projected skills do not verify: %d %v", verified, err)
	}
	if source := readFixtureText(t, filepath.Join(root, "AGENTS.md")); source == fixtureSource {
		t.Fatal("a completed write left the source unspliced, so the unchanged-source checks below prove nothing")
	}
}

// Boundary: a repository shipping no plugin manifest gets no plugin copies, and verify requires
// none, rather than inventing a requirement it never declared.
func TestCompileContextProjections_Boundary_NoPluginManifestShipsNothing(t *testing.T) {
	root := skillFixture(t)
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(PluginManifestRel))); err != nil {
		t.Fatal(err)
	}
	if err := compileFixture(t, root); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(PluginSkillsRel))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("plugin skills written without a plugin manifest: %v", err)
	}
	verified, err := VerifyPluginSkills(t.Context(), root)
	if err != nil || verified != 0 {
		t.Errorf("verified %d skills without a plugin manifest: %v", verified, err)
	}
}

// Negative: a symlinked canonical persona is refused before anything is written, the plugin copy
// included; main followed it and shipped the link target's text in the plugin.
func TestCompileContextProjections_Negative_RefusesSymlinkedPersona(t *testing.T) {
	root := skillFixture(t)
	victim := filepath.Join(root, "victim.md")
	writeOutputFixture(t, victim, "victim text\n")
	persona := filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel), "linked.md")
	if err := os.MkdirAll(filepath.Dir(persona), 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, victim, persona)
	if err := compileFixture(t, root); !errors.Is(err, errPersonaNotRegular) {
		t.Fatalf("want errPersonaNotRegular, got %v", err)
	}
	for _, rel := range []string{PluginAgentsRel + "/linked.md", ".claude/agents/linked.md", "CLAUDE.md"} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s written before the refusal: %v", rel, err)
		}
	}
}

// Negative: an existing vendor file, persona copy or plugin copy the writer cannot observe (not
// UTF-8 text, a NUL byte, above contextopt.MaxSourceBytes) is refused before the text register
// splice and before the first write. The writer refused it only when it reached that file, so
// CLAUDE.md, written first, and the spliced source had already changed.
func TestCompileContextProjections_Negative_UnobservableTargetLeavesTreeUnchanged(t *testing.T) {
	cases := map[string]struct{ rel, data string }{
		"vendor file not UTF-8":   {rel: ".codex/rules.md", data: "\xff\xfe\n"},
		"persona copy with NUL":   {rel: ".claude/agents/helper.md", data: "helper\x00\n"},
		"plugin skill above cap":  {rel: PluginSkillsRel + "/hiss-audit/" + SkillEntryName, data: strings.Repeat("a", contextopt.MaxSourceBytes+1)},
		"plugin persona not text": {rel: PluginAgentsRel + "/helper.md", data: "\xc3\x28\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := skillFixture(t)
			writeOutputFixture(t, filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel), "helper.md"), "---\nname: helper\ndescription: d\nmodel: flash\n---\n# Helper\n")
			writeCanonicalFixture(t, filepath.Join(root, "AGENTS.md"), fixtureSource)
			writeOutputFixture(t, filepath.Join(root, "CLAUDE.md"), "old claude\n")
			writeOutputFixture(t, filepath.Join(root, filepath.FromSlash(tc.rel)), tc.data)
			err := compileFixture(t, root)
			if err == nil || !strings.Contains(err.Error(), "target "+tc.rel+": existing output cannot be replaced") {
				t.Fatalf("want %s refused, got %v", tc.rel, err)
			}
			expectOutputFixture(t, filepath.Join(root, "CLAUDE.md"), "old claude\n")
			expectOutputFixture(t, filepath.Join(root, "AGENTS.md"), fixtureSource)
			expectOutputFixture(t, filepath.Join(root, filepath.FromSlash(tc.rel)), tc.data)
			if personaDirWritten(root, ".github/agents") {
				t.Error("a persona copy was written before the refusal")
			}
		})
	}
}

// ErrAgentSurface marks the persona and plugin skill failures of VerifyCompiledContext, so a
// caller such as adoption can tell the step that owns each one without matching text. Positive:
// a drifted persona copy is marked, keeps ErrAgentProjectionDrift below the mark and its text,
// and an orphaned plugin skill is marked too. Negative: a vendor file out of sync is not marked.
// Boundary: a clean write verifies with no error at all, marked or not.
func TestVerifyCompiledContext_MarksAgentSurfaceFailures(t *testing.T) {
	verify := func(root string) error {
		return VerifyCompiledContext(t.Context(), io.Discard, NewTranspiler(), filepath.Join(root, "AGENTS.md"), root)
	}
	compiled := func(t *testing.T) string {
		t.Helper()
		root := skillFixture(t)
		writeOutputFixture(t, filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel), "helper.md"), "---\nname: helper\ndescription: d\nmodel: flash\n---\n# Helper\n")
		if err := compileFixture(t, root); err != nil {
			t.Fatalf("compile: %v", err)
		}
		return root
	}
	root := compiled(t)
	if err := verify(root); err != nil {
		t.Fatalf("a clean write must verify: %v", err)
	}
	writeOutputFixture(t, filepath.Join(root, ".claude", "agents", "helper.md"), "# Drifted helper\n")
	err := verify(root)
	if !errors.Is(err, ErrAgentSurface) || !errors.Is(err, ErrAgentProjectionDrift) ||
		!strings.Contains(err.Error(), "agent persona verification failed") || strings.Contains(err.Error(), ErrAgentSurface.Error()) {
		t.Fatalf("a drifted persona copy must be marked with its own text and chain, got %v", err)
	}
	root = compiled(t)
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(PluginSkillsRel), "not-declared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verify(root); !errors.Is(err, ErrAgentSurface) || !strings.Contains(err.Error(), "not-declared") {
		t.Fatalf("an orphaned plugin skill must be marked, got %v", err)
	}
	root = compiled(t)
	writeOutputFixture(t, filepath.Join(root, "CLAUDE.md"), "# Hand-edited\n")
	if err := verify(root); err == nil || errors.Is(err, ErrAgentSurface) {
		t.Fatalf("a vendor file out of sync must fail unmarked, got %v", err)
	}
}

func readFixtureText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
