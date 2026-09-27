package compiler

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
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
		// The writer observes an existing output before replacing it and refuses one that is not
		// UTF-8 text. That refusal used to come only at write time, after CLAUDE.md was written.
		"existing output is not UTF-8 text": {
			arrange: func(t *testing.T, out string) {
				writeOutputFixture(t, filepath.Join(out, ".codex", "rules.md"), "\xff\xfe\n")
			},
			text: "existing output cannot be replaced: source must be UTF-8 text without NUL bytes",
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

// Negative: a symlinked skill directory, canonical or shipped, is refused rather than skipped.
// IsDir is false for a symlink, so the old filter verified the plugin without it.
func TestPluginSkillsRefuseSymlinkedSkillDirectories(t *testing.T) {
	for _, base := range []string{CanonicalSkillsRel, PluginSkillsRel} {
		t.Run(base, func(t *testing.T) {
			root := skillFixture(t)
			if err := compileFixture(t, root); err != nil {
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

// symlinkOrSkip creates link -> target, or skips: a host without symlinks cannot hold the
// symlinked component these tests refuse.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
}

// Negative: the persona writer itself follows no symlinked directory, independent of the
// pre-check in front of it. contextopt.WriteSnapshot resolved an existing .claude/agents, so a
// .claude pointing out of the root received the persona.
func TestWriteConfinedTextRefusesSymlinkedDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "agents", "reviewer.md")
	writeOutputFixture(t, victim, "victim\n")
	symlinkOrSkip(t, outside, filepath.Join(root, ".claude"))
	err := writeConfinedText(t.Context(), root, ".claude/agents/reviewer.md", []byte("persona\n"))
	if err == nil || !strings.Contains(err.Error(), "directory component must not be a symlink or file: .claude") {
		t.Fatalf("want the symlinked .claude refused, got %v", err)
	}
	expectOutputFixture(t, victim, "victim\n")
}

// Negative: CompileAgents refuses a symlinked persona directory, and a symlinked canonical
// directory, before it writes a single copy, with the error verify returns for the same tree.
func TestCompileAgentsRefusesSymlinkedDirectoriesBeforeAnyWrite(t *testing.T) {
	const refused = "path component must be a directory, never a symlink"
	cases := map[string]struct {
		link string // root-relative directory replaced by a symlink to its relocated copy
		want string
	}{
		"persona dir":   {link: ".claude", want: "target .claude/agents/helper.md: " + refused},
		"canonical dir": {link: ".agents", want: "read .agents/agents: " + refused},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := writePersonaFixture(t, "")
			if err := os.MkdirAll(filepath.Join(root, ".claude", "agents"), 0o700); err != nil {
				t.Fatal(err)
			}
			moved := filepath.Join(t.TempDir(), "moved")
			if err := os.Rename(filepath.Join(root, tc.link), moved); err != nil {
				t.Fatal(err)
			}
			symlinkOrSkip(t, moved, filepath.Join(root, tc.link))
			_, err := CompileAgents(t.Context(), root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			for _, dir := range []string{".github/agents", ".codex/agents", ".gemini/agents"} {
				if personaDirWritten(root, dir) {
					t.Errorf("%s written before the refusal", dir)
				}
			}
			if _, err := os.Lstat(filepath.Join(moved, "agents", "helper.md")); tc.link == ".claude" && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("wrote through the symlinked .claude: %v", err)
			}
			if _, verr := VerifyAgentProjections(t.Context(), root); verr == nil || !strings.Contains(verr.Error(), refused) {
				t.Errorf("verify must refuse the same tree, got %v", verr)
			}
		})
	}
}

// Boundary: checkOutputPath tolerates only an output that does not exist yet. Any other failure
// to inspect it (here a name the platform refuses to look up) is surfaced, never read as
// "absent, so writable".
func TestCheckOutputPathSurfacesLeafInspectionErrors(t *testing.T) {
	root := t.TempDir()
	if err := checkOutputPath(t.Context(), root, "absent.md"); err != nil {
		t.Fatalf("an absent output must be writable: %v", err)
	}
	err := checkOutputPath(t.Context(), root, "bad\x00name.md")
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want the lookup failure surfaced, got %v", err)
	}
}

// Negative and boundary: checkOutputPath refuses an existing output the writer cannot observe,
// one that is not UTF-8 text, holds a NUL byte or exceeds contextopt.MaxSourceBytes, and accepts
// one of exactly that size.
func TestCheckOutputPathAppliesTheWritersContentRefusals(t *testing.T) {
	root := t.TempDir()
	cases := map[string]struct {
		data   string
		refuse bool
	}{
		"not UTF-8":     {data: "\xff\xfe\n", refuse: true},
		"NUL byte":      {data: "text\x00more\n", refuse: true},
		"above the cap": {data: strings.Repeat("a", contextopt.MaxSourceBytes+1), refuse: true},
		"at the cap":    {data: strings.Repeat("a", contextopt.MaxSourceBytes)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rel := strings.ReplaceAll(name, " ", "-") + ".md"
			writeOutputFixture(t, filepath.Join(root, rel), tc.data)
			err := checkOutputPath(t.Context(), root, rel)
			if tc.refuse != (err != nil) {
				t.Fatalf("refuse=%t, got %v", tc.refuse, err)
			}
		})
	}
}
