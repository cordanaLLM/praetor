package compiler

import (
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
			writeOutputFixture(t, filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel), "helper.md"), "---\nname: helper\n---\n# Helper\n")
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

func readFixtureText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
