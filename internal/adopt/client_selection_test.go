package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func adoptWithManifest(t *testing.T, name, manifest string) (string, *AdoptReport, error) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	mustWrite(t, filepath.Join(repoPath, manifestFile), manifest)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath})
	return repoPath, rep, err
}

func notApplicableDetail(rep *AdoptReport, path string) string {
	for _, d := range rep.ActionDetails {
		if d.Path == path && d.Action == actionSkip {
			return d.Details
		}
	}
	return ""
}

// Positive: editors: [vscode] and agent_clients: [claude] limit adoption to those surfaces,
// report the rest as not applicable without a warning, and a file the repository deletes is
// not recreated by the next run once it is no longer selected (#202).
func TestAdopt_Positive_ManifestSelectsEditorsAndAgentClients(t *testing.T) {
	repoPath, rep, err := adoptWithManifest(t, "selected-tooling",
		"version: 1\neditors: [vscode]\nagent_clients: [claude]\n")
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	for _, rel := range []string{".vscode/settings.json", "CLAUDE.md"} {
		if !fileExists(filepath.Join(repoPath, filepath.FromSlash(rel))) {
			t.Errorf("selected surface %s not written", rel)
		}
	}
	for _, rel := range []string{".editorconfig", ".helix/config.toml", ".nvim.lua", ".windsurfrules", ".gemini/GEMINI.md", ".codex/rules.md"} {
		if fileExists(filepath.Join(repoPath, filepath.FromSlash(rel))) {
			t.Errorf("unselected surface %s written", rel)
		}
	}
	if details := notApplicableDetail(rep, "editors"); !strings.Contains(details, "universal") || strings.Contains(details, "vscode") {
		t.Errorf("editors not-applicable detail = %q", details)
	}
	if notApplicableDetail(rep, ".windsurfrules") == "" {
		t.Errorf("unselected projection not reported: %v", rep.ActionDetails)
	}
	for _, warning := range rep.Warnings {
		if strings.Contains(warning, "Not selected") {
			t.Errorf("a declared selection must not warn: %q", warning)
		}
	}

	for _, rel := range []string{".vscode/settings.json", "CLAUDE.md"} {
		if err := os.Remove(filepath.Join(repoPath, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\neditors: []\nagent_clients: []\n")
	rep, err = Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("second Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	for _, rel := range []string{".vscode/settings.json", "CLAUDE.md"} {
		if fileExists(filepath.Join(repoPath, filepath.FromSlash(rel))) {
			t.Errorf("deleted surface %s recreated although no longer selected", rel)
		}
	}
}

// Negative: an unknown editor or agent client fails adoption at its step instead of quietly
// adopting the known subset or every surface.
func TestAdopt_Negative_UnknownSelectionIDFails(t *testing.T) {
	for _, tc := range []struct{ name, manifest, id string }{
		{"editor", "version: 1\neditors: [vscode, notepad]\n", "notepad"},
		{"client", "version: 1\nagent_clients: [claude, vim]\n", "vim"},
	} {
		_, rep, err := adoptWithManifest(t, "unknown-"+tc.name, tc.manifest)
		if err == nil {
			t.Fatalf("%s: adoption accepted an unknown id", tc.name)
		}
		if !strings.Contains(err.Error(), tc.id) {
			t.Errorf("%s: error %q does not name %q", tc.name, err, tc.id)
		}
		if len(rep.Errors) == 0 {
			t.Errorf("%s: the failure is not in the report", tc.name)
		}
	}
}

// Boundary: a manifest without either key adopts every editor and every projection, the
// behaviour before the keys existed, and reports nothing as not applicable.
func TestAdopt_Boundary_AbsentSelectionKeepsEverySurface(t *testing.T) {
	repoPath, rep, err := adoptWithManifest(t, "default-tooling", "version: 1\n")
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	for _, rel := range []string{".vscode/settings.json", ".helix/config.toml", "CLAUDE.md", ".windsurfrules", ".codex/rules.md"} {
		if !fileExists(filepath.Join(repoPath, filepath.FromSlash(rel))) {
			t.Errorf("default surface %s missing", rel)
		}
	}
	if notApplicableDetail(rep, "editors") != "" || notApplicableDetail(rep, ".windsurfrules") != "" {
		t.Errorf("absent keys reported surfaces not applicable: %v", rep.ActionDetails)
	}
}
