package compiler

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoOutputs is a compile result whose first output sits at the target root and whose last one
// sits below a directory component, the order the vendor registry writes CLAUDE.md and
// .codex/rules.md in.
func twoOutputs() *CompileResult {
	return &CompileResult{Files: []TargetFile{
		{RelativePath: "CLAUDE.md", Content: "new claude\n"},
		{RelativePath: ".codex/rules.md", Content: "new rules\n"},
	}}
}

func writeOutputFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func expectOutputFixture(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s: got %q, %v; want %q", path, got, err, want)
	}
}

// Negative: a symlinked directory component that stays inside the target directory is refused
// before the first output is written. ConfinePath accepted the in-root link and an Lstat of the
// leaf followed it, so CLAUDE.md was already overwritten when the writer refused .codex.
func TestWriteOutputsRefusesInRootSymlinkedDirectoryBeforeAnyWrite(t *testing.T) {
	out := t.TempDir()
	writeOutputFixture(t, filepath.Join(out, "CLAUDE.md"), "old claude\n")
	if err := os.Mkdir(filepath.Join(out, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(out, "real"), filepath.Join(out, ".codex")); err != nil {
		t.Fatal(err)
	}
	err := NewTranspiler().WriteOutputsContext(t.Context(), twoOutputs(), out)
	if err == nil || !strings.Contains(err.Error(), "target .codex/rules.md: path component must be a directory") {
		t.Fatalf("want the symlinked .codex refused, got %v", err)
	}
	expectOutputFixture(t, filepath.Join(out, "CLAUDE.md"), "old claude\n")
	if _, err := os.Lstat(filepath.Join(out, "real", "rules.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("wrote through the symlinked directory: %v", err)
	}
}

// Negative: an existing output that is an in-root symlink, or a directory component that is a
// regular file, is refused before the first write.
func TestWriteOutputsRefusesUnwritableTargetsBeforeAnyWrite(t *testing.T) {
	cases := map[string]struct {
		arrange func(t *testing.T, out string)
		want    error
		text    string
	}{
		"in-root symlinked output": {
			arrange: func(t *testing.T, out string) {
				writeOutputFixture(t, filepath.Join(out, "real.md"), "keep\n")
				writeOutputFixture(t, filepath.Join(out, ".codex", "keep"), "")
				if err := os.Symlink(filepath.Join(out, "real.md"), filepath.Join(out, ".codex", "rules.md")); err != nil {
					t.Fatal(err)
				}
			},
			want: errOutputNotRegular,
		},
		"directory component is a file": {
			arrange: func(t *testing.T, out string) { writeOutputFixture(t, filepath.Join(out, ".codex"), "file\n") },
			text:    "path component must be a directory",
		},
		"output is a directory": {
			arrange: func(t *testing.T, out string) {
				if err := os.MkdirAll(filepath.Join(out, ".codex", "rules.md"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: errOutputNotRegular,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out := t.TempDir()
			writeOutputFixture(t, filepath.Join(out, "CLAUDE.md"), "old claude\n")
			tc.arrange(t, out)
			err := NewTranspiler().WriteOutputsContext(t.Context(), twoOutputs(), out)
			if err == nil || !strings.Contains(err.Error(), "target .codex/rules.md") ||
				(tc.want != nil && !errors.Is(err, tc.want)) || !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("want .codex/rules.md refused (%v %q), got %v", tc.want, tc.text, err)
			}
			expectOutputFixture(t, filepath.Join(out, "CLAUDE.md"), "old claude\n")
			if _, err := os.Lstat(filepath.Join(out, "real.md")); err == nil {
				expectOutputFixture(t, filepath.Join(out, "real.md"), "keep\n")
			}
		})
	}
}

// Positive and boundary: real directories and regular outputs are replaced, and a target
// directory that does not exist yet is created with everything below it.
func TestWriteOutputsWritesRealAndAbsentTargets(t *testing.T) {
	existing := t.TempDir()
	writeOutputFixture(t, filepath.Join(existing, "CLAUDE.md"), "old claude\n")
	writeOutputFixture(t, filepath.Join(existing, ".codex", "rules.md"), "old rules\n")
	absent := filepath.Join(t.TempDir(), "not", "yet")
	for _, out := range []string{existing, absent} {
		if err := NewTranspiler().WriteOutputsContext(t.Context(), twoOutputs(), out); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
		expectOutputFixture(t, filepath.Join(out, "CLAUDE.md"), "new claude\n")
		expectOutputFixture(t, filepath.Join(out, ".codex", "rules.md"), "new rules\n")
	}
}

// Negative: verify follows no symlink below the target directory either. A symlinked .cursor
// two components above the rules file, pointing at a copy that is in sync, was resolved by
// ReadSnapshot's ancestry walk and read as if it were the projection.
func TestVerifyCompiledRefusesSymlinkedDirectoryComponent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "AGENTS.md")
	writeCanonicalFixture(t, source, "# Policy\n")
	tr := NewTranspiler()
	result, err := tr.CompileContext(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.WriteOutputsContext(t.Context(), result, root); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.VerifyCompiled(t.Context(), source, root); err != nil {
		t.Fatalf("in-sync tree must verify: %v", err)
	}
	if err := os.Rename(filepath.Join(root, ".cursor"), filepath.Join(root, "cursor-copy")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "cursor-copy"), filepath.Join(root, ".cursor")); err != nil {
		t.Fatal(err)
	}
	_, err = tr.VerifyCompiled(t.Context(), source, root)
	if err == nil || !strings.Contains(err.Error(), "target .cursor/rules/hiss-invariants.mdc missing or unreadable: path component must be a directory") {
		t.Fatalf("want the symlinked .cursor refused, got %v", err)
	}
}

// Negative: a symlinked canonical persona is refused on the plugin path too, where no
// CompileAgents read stands in front of it; main followed it and shipped the target's text.
func TestProjectPluginAgentsRefusesSymlinkedPersona(t *testing.T) {
	root := skillFixture(t)
	victim := filepath.Join(root, "victim.md")
	writeOutputFixture(t, victim, "victim text\n")
	persona := filepath.Join(root, filepath.FromSlash(CanonicalAgentsRel), "linked.md")
	if err := os.MkdirAll(filepath.Dir(persona), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, persona); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectPluginAgents(t.Context(), root); !errors.Is(err, errPersonaNotRegular) {
		t.Fatalf("want errPersonaNotRegular, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(PluginAgentsRel), "linked.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("projected the symlinked persona: %v", err)
	}
}

// Negative: a symlinked skill directory, canonical or shipped, is refused rather than skipped.
// IsDir is false for a symlink, so the old filter verified the plugin without it.
func TestPluginSkillsRefuseSymlinkedSkillDirectories(t *testing.T) {
	for _, base := range []string{CanonicalSkillsRel, PluginSkillsRel} {
		t.Run(base, func(t *testing.T) {
			root := skillFixture(t)
			if _, err := ProjectPluginSkills(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyPluginSkills(t.Context(), root); err != nil {
				t.Fatalf("in-sync plugin must verify: %v", err)
			}
			link := filepath.Join(root, filepath.FromSlash(base), "linked")
			if err := os.Symlink(filepath.Join(root, filepath.FromSlash(CanonicalSkillsRel), "hiss-audit"), link); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyPluginSkills(t.Context(), root); !errors.Is(err, errSkillDirSymlink) {
				t.Fatalf("want errSkillDirSymlink, got %v", err)
			}
		})
	}
}
