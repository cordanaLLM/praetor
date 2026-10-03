package adopt

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/editor"
)

// Issue #316: VS Code reads .vscode/settings.json, extensions.json and tasks.json as JSON with
// Comments. Adoption reads them the same way, so a commented file holding every managed value
// is verified, and one it would have to rewrite is kept with its comments.

// vscodeFiles are the JSON files adoption generates for VS Code.
var vscodeFiles = []string{".vscode/settings.json", ".vscode/extensions.json", ".vscode/tasks.json"}

// commentJSON adds what an adopter adds to a generated JSON object: a header comment, a block
// comment inside the object and a trailing comma after its last member.
func commentJSON(t *testing.T, generated string) string {
	t.Helper()
	body, found := strings.CutSuffix(strings.TrimRight(generated, "\n"), "\n}")
	if !found || !strings.HasPrefix(body, "{\n") {
		t.Fatalf("generated editor JSON is not an indented object:\n%s", generated)
	}
	return "// adopter note\n{\n  /* kept by hand */\n" + strings.TrimPrefix(body, "{\n") + ",\n}\n"
}

// Positive: once the adopter's commented .vscode files hold every managed value, a plain run
// and a forced one both verify them, change no byte and warn about nothing, and
// `editors verify` passes them.
func TestAdopt_Positive_CommentedVSCodeFilesInSyncAreVerified(t *testing.T) {
	repoPath, _ := adoptEditorsFixture(t, "editor-jsonc-in-sync", "vscode", nil, false)
	commented := map[string]string{}
	for _, rel := range vscodeFiles {
		full := filepath.Join(repoPath, filepath.FromSlash(rel))
		commented[rel] = commentJSON(t, mustRead(t, full))
		mustWrite(t, full, commented[rel])
	}
	for _, force := range []bool{false, true} {
		rep, err := Adopt(context.Background(), editorAdoptOptions(t, repoPath, force, false))
		if err != nil {
			t.Fatalf("Adopt (force=%v): %v", force, err)
		}
		assertNoIssues(t, rep)
		for _, rel := range vscodeFiles {
			if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(rel))); got != commented[rel] {
				t.Errorf("force=%v changed the commented %s:\n%s", force, rel, got)
			}
			if hasWarningContaining(rep, rel) || hasAction(rep, rel, actionMerge) || hasAction(rep, rel, actionReplace) {
				t.Errorf("force=%v: in-sync commented %s was warned about or written: %v %v", force, rel, rep.Warnings, rep.ActionDetails)
			}
		}
	}
	verification, err := verifyEditors(t, repoPath, []string{editor.EditorVSCode})
	if err != nil {
		t.Fatalf("editors verify: %v", err)
	}
	for _, rel := range vscodeFiles {
		if !slices.Contains(verification.Verified, rel) {
			t.Errorf("editors verify did not verify the commented %s: %+v", rel, verification)
		}
	}
}

// Negative: a commented .vscode file that lacks managed values is kept on a plain run and under
// --force, with the warning that says why and names the values; a file that is not valid JSON
// with Comments is told what to fix, not to remove its comments.
func TestAdopt_Negative_CommentedVSCodeFileLackingValuesIsKept(t *testing.T) {
	lacking := "{\n  // adopter note\n  \"adopter.custom\": \"keep\",\n}\n"
	duplicate := "{\n  // adopter note\n  \"recommendations\": [],\n  \"recommendations\": []\n}\n"
	files := map[string]string{".vscode/settings.json": lacking, ".vscode/extensions.json": duplicate}
	for _, force := range []bool{false, true} {
		repoPath, rep := adoptEditorsFixture(t, "editor-jsonc-lacking", "vscode", files, force)
		for rel, content := range files {
			if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(rel))); got != content {
				t.Errorf("force=%v changed %s:\n%s", force, rel, got)
			}
			if hasAction(rep, rel, actionMerge) || hasAction(rep, rel, actionReplace) {
				t.Errorf("force=%v: %s reported as written: %v", force, rel, rep.ActionDetails)
			}
		}
		for _, want := range []string{
			".vscode/settings.json: kept unchanged, not verified: it carries comments or trailing commas, which a merge would lose",
			`"/standards.sentinel.headroomMB"`,
			"Add them by hand and re-run adopt, or remove the comments and trailing commas and run ",
			".vscode/extensions.json: kept unchanged, not verified: it is not valid JSON with Comments",
			"comments and trailing commas may stay, a duplicate key may not",
		} {
			if !hasWarningContaining(rep, want) {
				t.Errorf("force=%v: no warning says %q, got %v", force, want, rep.Warnings)
			}
		}
		if hasWarningContaining(rep, "Make it strict JSON") {
			t.Errorf("force=%v: a .vscode file was told to become strict JSON: %v", force, rep.Warnings)
		}
	}
}

// Boundary: the dialect follows the editor. The same commented bytes at a Zed path, which
// adoption reads as strict JSON, are kept with the strict-JSON advice, while VS Code's copy is
// kept for its comments.
func TestAdopt_Boundary_OnlyVSCodePathsAreReadAsJSONC(t *testing.T) {
	commented := "{\n  // adopter note\n  \"adopter.custom\": \"keep\"\n}\n"
	files := map[string]string{".vscode/settings.json": commented, ".zed/settings.json": commented}
	repoPath, rep := adoptEditorsFixture(t, "editor-jsonc-dialect", "vscode, zed", files, true)
	for rel := range files {
		if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(rel))); got != commented {
			t.Errorf("--force changed %s:\n%s", rel, got)
		}
	}
	if !hasWarningContaining(rep, ".zed/settings.json: kept unchanged, not verified: it is not strict JSON") ||
		!hasWarningContaining(rep, "Make it strict JSON (remove its comments, trailing commas and duplicate keys)") {
		t.Errorf("a commented Zed file must get the strict-JSON advice, got %v", rep.Warnings)
	}
	if !hasWarningContaining(rep, ".vscode/settings.json: kept unchanged, not verified: it carries comments or trailing commas") {
		t.Errorf("a commented VS Code file must be kept for its comments, got %v", rep.Warnings)
	}
}
