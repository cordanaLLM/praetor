package compiler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// nestedRepo is a Git work tree holding a terse root AGENTS.md and files, every one staged.
func nestedRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, root, "")
	writeOutputFixture(t, filepath.Join(root, "AGENTS.md"), lintTerseAgents)
	for rel, content := range files {
		writeOutputFixture(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	testsupport.RunFixtureGit(t, root, []string{"add", "--all"})
	return root
}

// listNested is ListNestedContextFiles that fails the test on an error.
func listNested(t *testing.T, root string) []string {
	t.Helper()
	paths, err := ListNestedContextFiles(t.Context(), root)
	if err != nil {
		t.Fatalf("ListNestedContextFiles: %v", err)
	}
	return paths
}

// Positive: every tracked nested AGENTS.md at any depth is listed, sorted, as a slash path; the
// root file and other Markdown files are not, and neither is a file whose name only folds to
// AGENTS.md, whatever the platform's case folding.
func TestListNestedContextFiles_Positive(t *testing.T) {
	root := nestedRepo(t, map[string]string{
		"b/AGENTS.md":      lintTerseAgents,
		"a/deep/AGENTS.md": lintTerseAgents,
		"a/notes.md":       lintProseAgents,
		"c/agents.md":      lintProseAgents,
	})
	if got, want := listNested(t, root), []string{"a/deep/AGENTS.md", "b/AGENTS.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListNestedContextFiles = %q, want %q", got, want)
	}
}

// Positive: a clean nested AGENTS.md passes and is counted; the shared summary names it.
func TestLintNestedContexts_Positive(t *testing.T) {
	root := nestedRepo(t, map[string]string{"sub/AGENTS.md": lintTerseAgents})
	if n, err := lintNestedContexts(t.Context(), root, filepath.Join(root, "AGENTS.md")); err != nil || n != 1 {
		t.Fatalf("lintNestedContexts = %d, %v; want 1, nil", n, err)
	}
	line, err := AuditAgentSources(t.Context(), root, filepath.Join(root, "AGENTS.md"))
	if err != nil || !strings.Contains(line, "[PASS] Caveman lint verified (1 nested AGENTS.md, 0 personas and 0 skills") {
		t.Fatalf("AuditAgentSources = %q, %v", line, err)
	}
}

// Negative: a nested AGENTS.md in prose fails as kind context, the verdict 'praetorctl caveman
// check --kind=context' prints for it; the error names the file and how many of the linted
// files failed, and the audit gate joins it with no other surface's failure.
func TestLintNestedContexts_Negative(t *testing.T) {
	root := nestedRepo(t, map[string]string{"bad/AGENTS.md": lintProseAgents, "good/AGENTS.md": lintTerseAgents})
	if report, _ := CheckContextText(lintProseAgents, caveman.Options{Kind: caveman.KindContext}); report.Passed() {
		t.Fatal("the prose fixture passes the context check, so this case proves nothing")
	}
	_, err := lintNestedContexts(t.Context(), root, filepath.Join(root, "AGENTS.md"))
	if !errors.Is(err, ErrNestedContextProse) || errors.Is(err, ErrContextProse) {
		t.Fatalf("want ErrNestedContextProse alone, got %v", err)
	}
	for _, want := range []string{"1 of 2 files", "C1 article-density",
		"praetorctl caveman check --kind=context " + filepath.Join("bad", "AGENTS.md")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if _, err := AuditAgentSources(t.Context(), root, filepath.Join(root, "AGENTS.md")); !errors.Is(err, ErrNestedContextProse) ||
		!strings.HasPrefix(err.Error(), "[FAIL] Agent source caveman lint: ") {
		t.Fatalf("AuditAgentSources: want the [FAIL] nested verdict, got %v", err)
	}
}

// Negative: a nested AGENTS.md that is a symlink is refused, never followed, and the error names
// it. Skipped on Windows, where creating a symlink needs a privilege a test runner lacks.
func TestLintNestedContexts_Negative_SymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs SeCreateSymbolicLinkPrivilege on Windows")
	}
	root := nestedRepo(t, map[string]string{"target.md": lintTerseAgents})
	if err := os.MkdirAll(filepath.Join(root, "link"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "target.md"), filepath.Join(root, "link", "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	testsupport.RunFixtureGit(t, root, []string{"add", "--all"})
	_, err := lintNestedContexts(t.Context(), root, "")
	if err == nil || errors.Is(err, ErrNestedContextProse) || !strings.Contains(err.Error(), "link/AGENTS.md") {
		t.Fatalf("want a read refusal naming link/AGENTS.md, got %v", err)
	}
}

// Boundary: only tracked files are read. An ignored and an untracked nested AGENTS.md in prose,
// and one inside a nested repository the outer index holds as a gitlink, list none and pass; so
// do zero nested files and a root outside every Git work tree.
func TestListNestedContextFiles_Boundary_OnlyTrackedFiles(t *testing.T) {
	ignored := nestedRepo(t, map[string]string{".gitignore": "ignored/\n", "ignored/AGENTS.md": lintProseAgents})
	writeOutputFixture(t, filepath.Join(ignored, "untracked", "AGENTS.md"), lintProseAgents)

	nested := nestedRepo(t, nil)
	inner := filepath.Join(nested, "inner")
	writeOutputFixture(t, filepath.Join(inner, "AGENTS.md"), lintProseAgents)
	testsupport.InitGitRepoWithOrigin(t, inner, "")
	testsupport.RunFixtureGit(t, inner, []string{"add", "--all"}, []string{"commit", "--quiet", "-m", "inner"})
	testsupport.RunFixtureGit(t, nested, []string{"add", "--all"})
	if staged := testsupport.RunFixtureGit(t, nested, []string{"ls-files", "--stage", "--", "inner"}); !strings.HasPrefix(staged, "160000 ") {
		t.Fatalf("the nested repository is not a gitlink in the outer index: %q", staged)
	}

	for name, root := range map[string]string{"ignored and untracked": ignored, "nested repository": nested,
		"zero nested files": nestedRepo(t, nil), "outside Git": t.TempDir()} {
		t.Run(name, func(t *testing.T) {
			if paths := listNested(t, root); len(paths) != 0 {
				t.Fatalf("listed %q", paths)
			}
			if n, err := lintNestedContexts(t.Context(), root, filepath.Join(root, "AGENTS.md")); err != nil || n != 0 {
				t.Fatalf("lintNestedContexts = %d, %v; want 0, nil", n, err)
			}
		})
	}
}

// Boundary: the cap admits exactly its number of nested files and refuses one more, naming both
// numbers.
func TestListNestedContextFiles_Boundary_Cap(t *testing.T) {
	root := nestedRepo(t, map[string]string{"a/AGENTS.md": lintTerseAgents, "b/AGENTS.md": lintTerseAgents})
	if paths, err := listNestedContextFiles(t.Context(), root, 2); err != nil || len(paths) != 2 {
		t.Fatalf("at the cap: %q, %v; want two files", paths, err)
	}
	writeOutputFixture(t, filepath.Join(root, "c", "AGENTS.md"), lintTerseAgents)
	testsupport.RunFixtureGit(t, root, []string{"add", "--all"})
	paths, err := listNestedContextFiles(t.Context(), root, 2)
	if !errors.Is(err, ErrNestedContextCap) || paths != nil {
		t.Fatalf("over the cap: %q, %v; want ErrNestedContextCap", paths, err)
	}
	if !strings.Contains(err.Error(), "3 tracked, more than the cap of 2") {
		t.Fatalf("error lacks count and cap: %v", err)
	}
	if MaxNestedContextFiles < 219 {
		t.Fatalf("MaxNestedContextFiles = %d is below the 219 files the #311 adopter tracks", MaxNestedContextFiles)
	}
}

// Boundary: the canonical source is linted once, by LintContext, even when it is a tracked nested
// file; a tracked file missing from the working tree holds no text and is not counted; and a
// cancelled context stops the run.
func TestLintNestedContexts_Boundary_SourceDeletedAndCancelled(t *testing.T) {
	root := nestedRepo(t, map[string]string{"docs/AGENTS.md": lintProseAgents, "gone/AGENTS.md": lintProseAgents})
	if err := os.RemoveAll(filepath.Join(root, "gone")); err != nil {
		t.Fatal(err)
	}
	if n, err := lintNestedContexts(t.Context(), root, filepath.Join(root, "docs", "AGENTS.md")); err != nil || n != 0 {
		t.Fatalf("source skipped, deletion ignored: %d, %v; want 0, nil", n, err)
	}
	if _, err := lintNestedContexts(t.Context(), root, filepath.Join(root, "AGENTS.md")); !errors.Is(err, ErrNestedContextProse) {
		t.Fatalf("docs/AGENTS.md is not the source here and must fail: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := lintNestedContexts(ctx, root, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: want context.Canceled, got %v", err)
	}
}

// Boundary: the error quotes at most maxQuotedLintFindings files and counts the rest.
func TestLintNestedContexts_Boundary_QuotedFiles(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < maxQuotedLintFindings+2; i++ {
		files[fmt.Sprintf("d%d/AGENTS.md", i)] = lintProseAgents
	}
	_, err := lintNestedContexts(t.Context(), nestedRepo(t, files), "")
	want := fmt.Sprintf("%d of %d files", maxQuotedLintFindings+2, maxQuotedLintFindings+2)
	if !errors.Is(err, ErrNestedContextProse) || !strings.Contains(err.Error(), want) ||
		!strings.HasSuffix(err.Error(), "(+2 more files)") || strings.Count(err.Error(), "praetorctl caveman check") != maxQuotedLintFindings {
		t.Fatalf("want %s with %d quoted files and +2 more, got %v", want, maxQuotedLintFindings, err)
	}
}

// Boundary: the listing parser drops the root file and empty records and keeps names verbatim.
func TestNestedContextPaths_Boundary(t *testing.T) {
	for name, tc := range map[string]struct {
		out  string
		want []string
	}{
		"empty listing":    {"", nil},
		"root only":        {"AGENTS.md\x00", nil},
		"unterminated":     {"x/AGENTS.md", []string{"x/AGENTS.md"}},
		"spaces and order": {"z/AGENTS.md\x00a b/AGENTS.md\x00", []string{"a b/AGENTS.md", "z/AGENTS.md"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := nestedContextPaths([]byte(tc.out), 2)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("nestedContextPaths(%q) = %q, %v; want %q", tc.out, got, err, tc.want)
			}
		})
	}
}

// This repository's own nested AGENTS.md files, if any, pass the gate.
func TestRepositoryNestedContextsPassCavemanLint(t *testing.T) {
	root := filepath.Dir(canonicalAgents)
	if _, err := lintNestedContexts(t.Context(), root, canonicalAgents); err != nil {
		t.Fatal(err)
	}
}
