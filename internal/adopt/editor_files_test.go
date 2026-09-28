package adopt

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/editor"
)

// hasWarningContaining reports whether one of the report's warnings contains text.
func hasWarningContaining(rep *AdoptReport, text string) bool {
	return slices.ContainsFunc(rep.Warnings, func(warning string) bool { return strings.Contains(warning, text) })
}

// actionDetail returns the details of the report's first action on path with action, or "".
func actionDetail(rep *AdoptReport, path, action string) string {
	for _, d := range rep.ActionDetails {
		if d.Path == path && d.Action == action {
			return d.Details
		}
	}
	return ""
}

// editorAdoptOptions adopts repoPath under the framework profile, the archetype
// `praetorctl editors verify` synthesizes for (editor.DefaultOptions), so a verification after
// adoption checks the same managed values adoption wrote.
func editorAdoptOptions(t *testing.T, repoPath string, force, dryRun bool) AdoptOptions {
	t.Helper()
	return AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Profile: "framework", Force: force, DryRun: dryRun}
}

// adoptEditorsFixture writes files (slash path -> content) into a new repository whose manifest
// selects editors, adopts it, plain or under force, and returns the repository and the report.
func adoptEditorsFixture(t *testing.T, name, editors string, files map[string]string, force bool) (string, *AdoptReport) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\neditors: ["+editors+"]\nagent_clients: []\n")
	for rel, content := range files {
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(rel)), content)
	}
	rep, err := Adopt(context.Background(), editorAdoptOptions(t, repoPath, force, false))
	if err != nil {
		t.Fatalf("Adopt (force=%v): %v", force, err)
	}
	assertNoIssues(t, rep)
	return repoPath, rep
}

// verifyEditors runs what `praetorctl editors verify` runs for repoPath's declared editors
// (cmd/standardsctl/editors.go: the manifest selection, the repository's resolved complexity).
func verifyEditors(t *testing.T, repoPath string, editors []string) (editor.VerificationReport, error) {
	t.Helper()
	ctx := context.Background()
	opts := editor.DefaultOptions()
	opts.WorkspaceRoot = repoPath
	opts.Editors = editors
	complexity, _, err := config.ResolveRepositoryComplexity(ctx, repoPath)
	if err != nil {
		t.Fatalf("resolve complexity: %v", err)
	}
	opts.Complexity = complexity
	set, err := editor.SynthesizeContext(ctx, opts)
	if err != nil {
		t.Fatalf("synthesize editors: %v", err)
	}
	return editor.VerifyWithReport(set, repoPath)
}

// Positive: a plain run keeps an adopter's .vscode/settings.json byte for byte and names the
// managed values it lacks; --force merges them, keeps the adopter's own key, reports a merge
// (not a replace) with its backup note, and `editors verify` then passes (#502).
func TestAdopt_Positive_ForceMergesEditorJSONKeepingAdopterKeys(t *testing.T) {
	const rel = ".vscode/settings.json"
	custom := "{\n    \"adopter.custom\": \"keep\"\n}\n"
	repoPath, rep := adoptEditorsFixture(t, "editor-merge", "vscode", map[string]string{rel: custom}, false)
	if got := mustRead(t, filepath.Join(repoPath, rel)); got != custom {
		t.Fatalf("plain run changed %s:\n%s", rel, got)
	}
	if !hasWarningContaining(rep, rel+": kept unchanged") || !hasWarningContaining(rep, `"/standards.sentinel.headroomMB"`) {
		t.Fatalf("plain run must warn with the missing managed values, got %v", rep.Warnings)
	}

	rep, err := Adopt(context.Background(), editorAdoptOptions(t, repoPath, true, false))
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	got := mustRead(t, filepath.Join(repoPath, rel))
	if !strings.Contains(got, `"adopter.custom": "keep"`) || !strings.Contains(got, `"standards.sentinel.headroomMB": 1024`) {
		t.Fatalf("--force must keep the adopter key and add the managed ones, got:\n%s", got)
	}
	detail := actionDetail(rep, rel, actionMerge)
	if !strings.Contains(detail, "merged into existing content") || !strings.Contains(detail, "backup") {
		t.Errorf("merge entry must carry the delta and backup note, got %q (actions %v)", detail, rep.ActionDetails)
	}
	for _, entry := range rep.Replaced() {
		if entry.Path == rel {
			t.Errorf("a merge must not be reported as a replace: %v", entry)
		}
	}
	verification, err := verifyEditors(t, repoPath, []string{editor.EditorVSCode})
	if err != nil || !slices.Contains(verification.Verified, rel) {
		t.Fatalf("editors verify after --force: %v %+v", err, verification)
	}
}

// Negative: under --force a conflicting managed value and a JSONC file are kept byte for byte,
// each with a warning naming why, and adoption finishes without an error.
func TestAdopt_Negative_ForceKeepsUnmergeableEditorJSON(t *testing.T) {
	conflict := "{\n  \"standards.sentinel.headroomMB\": 512\n}\n"
	jsonc := "{\n  // adopter note\n  \"recommendations\": []\n}\n"
	files := map[string]string{".vscode/settings.json": conflict, ".vscode/extensions.json": jsonc}
	repoPath, rep := adoptEditorsFixture(t, "editor-conflict", "vscode", files, true)
	for rel, content := range files {
		if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(rel))); got != content {
			t.Errorf("--force changed unmergeable %s:\n%s", rel, got)
		}
		if hasAction(rep, rel, actionMerge) || hasAction(rep, rel, actionReplace) {
			t.Errorf("unmergeable %s reported as written: %v", rel, rep.ActionDetails)
		}
	}
	if !hasWarningContaining(rep, `.vscode/settings.json: kept unchanged`) || !hasWarningContaining(rep, "conflicts with the existing value") {
		t.Errorf("conflict must be warned about, got %v", rep.Warnings)
	}
	if !hasWarningContaining(rep, ".vscode/extensions.json: kept unchanged, not verified: it is not strict JSON") {
		t.Errorf("JSONC file must be warned about, got %v", rep.Warnings)
	}
}

// Boundary: --force keeps a drifted lua/standards.lua, a non-JSON file nothing audits, and says
// how to regenerate it; it merges a .sublime-project, which is JSON, keeping the adopter's
// folder and setting; a dry run plans that merge and writes nothing.
func TestAdopt_Boundary_ForceKeepsNonJSONDriftAndMergesSublimeProject(t *testing.T) {
	const module, project = "lua/standards.lua", "standards.sublime-project"
	sublime := `{"folders":[{"path":"src"}],"settings":{"font_size":12}}` + "\n"
	files := map[string]string{module: "-- stale module\n", project: sublime}

	repoPath, _ := adoptEditorsFixture(t, "editor-dry-run", "neovim, sublime", files, false)
	rep, err := Adopt(context.Background(), editorAdoptOptions(t, repoPath, true, true))
	if err != nil {
		t.Fatalf("Adopt --force --dry-run: %v", err)
	}
	if got := mustRead(t, filepath.Join(repoPath, project)); got != sublime || !hasAction(rep, project, actionMerge) {
		t.Fatalf("dry run must plan the merge and write nothing, got merge detail %q:\n%s", actionDetail(rep, project, actionMerge), got)
	}

	repoPath, rep = adoptEditorsFixture(t, "editor-drift", "neovim, sublime", files, true)
	if got := mustRead(t, filepath.Join(repoPath, module)); got != "-- stale module\n" {
		t.Fatalf("--force replaced a drifted %s:\n%s", module, got)
	}
	if !hasWarningContaining(rep, module+": kept unchanged") || !hasWarningContaining(rep, "editors generate") {
		t.Errorf("drifted %s must be warned about with the regenerate path, got %v", module, rep.Warnings)
	}
	got := mustRead(t, filepath.Join(repoPath, project))
	for _, want := range []string{`"path": "src"`, `"font_size": 12`, `"path": "."`, `"tab_size": 4`} {
		if !strings.Contains(got, want) {
			t.Errorf("merged %s lacks %s:\n%s", project, want, got)
		}
	}
	if !hasAction(rep, project, actionMerge) {
		t.Errorf("%s merge not reported, warnings %v", project, rep.Warnings)
	}
}

// Negative: an editor file adoption cannot read, here a symlink the no-follow reader refuses, is
// kept with a warning under --force, the file behind the link is not written through, and
// adoption finishes without an error.
func TestAdopt_Negative_ForceKeepsUnreadableEditorFile(t *testing.T) {
	repoPath := newTestRepo(t, "editor-symlink")
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\neditors: [vscode]\nagent_clients: []\n")
	const target = "{\n  \"adopter.custom\": \"keep\"\n}\n"
	mustWrite(t, filepath.Join(repoPath, "shared-settings.json"), target)
	mustWrite(t, filepath.Join(repoPath, ".vscode", "extensions.json"), "{\"recommendations\":[]}\n")
	if err := os.Symlink(filepath.Join("..", "shared-settings.json"), filepath.Join(repoPath, ".vscode", "settings.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	rep, err := Adopt(context.Background(), editorAdoptOptions(t, repoPath, true, false))
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, "shared-settings.json")); got != target {
		t.Fatalf("adoption wrote through the symlinked editor file:\n%s", got)
	}
	if !hasWarningContaining(rep, ".vscode/settings.json: kept unchanged, not verified: it could not be read") {
		t.Errorf("unreadable editor file must be warned about, got %v", rep.Warnings)
	}
}
