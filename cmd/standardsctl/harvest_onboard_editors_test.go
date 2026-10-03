package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// Issue #717: `harvest onboard` merges an existing editor file through the rule `editors
// generate` applies and prints each file's outcome as `editors generate` prints it.

// onboardEditorsRepo is a repository with a valid source-less lock whose manifest selects
// editors and no agent client, holding settings as .vscode/settings.json unless it is empty.
func onboardEditorsRepo(t *testing.T, editors, settings string) string {
	t.Helper()
	repo := t.TempDir()
	writeFixtureFile(t, repo, ".standards.yaml",
		"version: 1\nprofiles: [framework]\neditors: ["+editors+"]\nagent_clients: []\n")
	testsupport.WritePinnedLock(t, repo, "framework", "id: framework\nname: onboarding fixture\n")
	if settings != "" {
		writeFixtureFile(t, repo, ".vscode/settings.json", settings)
	}
	return repo
}

func onboardCLI(t *testing.T, repo string, live bool) (string, error) {
	t.Helper()
	args := []string{"--repo=" + repo}
	if live {
		args = append(args, "--dry-run=false")
	}
	return captureStdout(t, func() error { return runHarvestOnboard(context.Background(), args) })
}

func readOnboardFile(t *testing.T, repo, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Positive: the repository's key stays, the managed values are added, and the run prints the
// file as merged.
func TestHarvestOnboard_Positive_PrintsMergedEditorFile(t *testing.T) {
	repo := onboardEditorsRepo(t, "vscode", "{\n  \"repo.key\": \"kept\"\n}\n")
	out, err := onboardCLI(t, repo, true)
	if err != nil {
		t.Fatalf("onboard: %v\n%s", err, out)
	}
	if !strings.Contains(out, "  - [MERGED] [vscode] .vscode/settings.json") {
		t.Errorf("merge not reported:\n%s", out)
	}
	got := readOnboardFile(t, repo, ".vscode/settings.json")
	if !strings.Contains(got, `"repo.key": "kept"`) || !strings.Contains(got, `"standards.sentinel.headroomMB"`) {
		t.Errorf("settings not merged:\n%s", got)
	}
}

// Negative: a commented settings file lacking managed values fails the run, live or dry, with
// the refusal, which names the file; its bytes stay and the run writes no AGENTS.md.
func TestHarvestOnboard_Negative_RefusesCommentedEditorFile(t *testing.T) {
	const commented = "{\n  // repository note\n  \"repo.key\": true\n}\n"
	for _, live := range []bool{true, false} {
		repo := onboardEditorsRepo(t, "vscode", commented)
		out, err := onboardCLI(t, repo, live)
		var refusal *editor.CommentedJSONError
		if !errors.As(err, &refusal) {
			t.Fatalf("live=%v: refusal lost: %v\n%s", live, err, out)
		}
		if !strings.Contains(out, "[FAIL]") || !strings.Contains(out, "cannot safely merge existing .vscode/settings.json") {
			t.Errorf("live=%v: refusal not printed by name:\n%s", live, out)
		}
		if got := readOnboardFile(t, repo, ".vscode/settings.json"); got != commented {
			t.Errorf("live=%v: refused file rewritten:\n%s", live, got)
		}
		if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("live=%v: a refused run wrote AGENTS.md: %v", live, err)
		}
	}
}

// Boundary: a dry run prints no editor outcome and leaves a file a live run would merge alone;
// a preserved developer-owned file is printed and counted unverified, as `editors generate` does.
func TestHarvestOnboard_Boundary_DryRunAndPreservedFile(t *testing.T) {
	const mergeable = "{\n  \"repo.key\": true\n}\n"
	repo := onboardEditorsRepo(t, "vscode", mergeable)
	out, err := onboardCLI(t, repo, false)
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if strings.Contains(out, "[MERGED]") || strings.Contains(out, "[CREATED]") {
		t.Errorf("dry run printed editor outcomes:\n%s", out)
	}
	if got := readOnboardFile(t, repo, ".vscode/settings.json"); got != mergeable {
		t.Errorf("dry run changed the settings file:\n%s", got)
	}

	neovim := onboardEditorsRepo(t, "neovim", "")
	writeFixtureFile(t, neovim, ".nvim.lua", "vim.opt.number = true\n")
	out, err = onboardCLI(t, neovim, true)
	if err != nil {
		t.Fatalf("onboard: %v\n%s", err, out)
	}
	for _, want := range []string{"  - [PRESERVED] [neovim] .nvim.lua", "[UNVERIFIED] 1 existing human-owned file(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
