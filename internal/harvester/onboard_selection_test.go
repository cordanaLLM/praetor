package harvester

import (
	"context"
	"github.com/cordanaLLM/praetor/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// selectionOnboardFixture is verifiedOnboardFixture with extra manifest lines appended.
func selectionOnboardFixture(t *testing.T, extra string) string {
	t.Helper()
	repo := verifiedOnboardFixture(t)
	mustWriteFile(t, filepath.Join(repo, ".standards.yaml"), "version: 1\nprofiles: [framework]\n"+extra)
	return repo
}

func onboardExists(repo, rel string) bool {
	_, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel)))
	return err == nil
}

// Positive: an existing manifest's editors and agent_clients limit what onboarding writes.
func TestOnboardRepository_Positive_HonoursDeclaredSelection(t *testing.T) {
	repo := selectionOnboardFixture(t, "editors: [helix]\nagent_clients: [codex]\n")
	if _, err := OnboardRepository(context.Background(), repo, false); err != nil {
		t.Fatalf("onboard: %v", err)
	}
	for _, rel := range []string{".helix/config.toml", ".codex/rules.md"} {
		if !onboardExists(repo, rel) {
			t.Errorf("selected output %s missing", rel)
		}
	}
	for _, rel := range []string{".vscode/settings.json", ".editorconfig", "CLAUDE.md", ".windsurfrules"} {
		if onboardExists(repo, rel) {
			t.Errorf("unselected output %s written", rel)
		}
	}
}

// Negative: an unknown id fails onboarding instead of writing every surface.
func TestOnboardRepository_Negative_UnknownSelectionFails(t *testing.T) {
	for id, extra := range map[string]string{"notepad": "editors: [notepad]\n", "vim": "agent_clients: [vim]\n"} {
		repo := selectionOnboardFixture(t, extra)
		_, err := OnboardRepository(context.Background(), repo, false)
		if err == nil || !strings.Contains(err.Error(), id) {
			t.Errorf("%s: unknown id not rejected: %v", id, err)
		}
		if onboardExists(repo, ".vscode/settings.json") {
			t.Errorf("%s: editor files written on a rejected selection", id)
		}
	}
}

// Boundary: empty lists select nothing, so onboarding writes no editor file and no projection.
func TestOnboardRepository_Boundary_EmptySelectionWritesNone(t *testing.T) {
	repo := selectionOnboardFixture(t, "editors: []\nagent_clients: []\n")
	if _, err := OnboardRepository(context.Background(), repo, false); err != nil {
		t.Fatalf("onboard: %v", err)
	}
	for _, rel := range []string{".vscode/settings.json", ".editorconfig", "CLAUDE.md", ".codex/rules.md"} {
		if onboardExists(repo, rel) {
			t.Errorf("empty selection wrote %s", rel)
		}
	}
}

// Boundary: onboarding keeps every developer-owned editor file (editor.IsPreservedEditorFile),
// not only .editorconfig and .clang-tidy: a developer's own .nvim.lua survives, while the
// Praetor module beside it, which does not exist yet, is still written.
func TestOnboardRepository_Boundary_KeepsDeveloperOwnedNvimLua(t *testing.T) {
	repo := selectionOnboardFixture(t, "editors: [neovim]\nagent_clients: []\n")
	const own = "vim.opt.number = true\n"
	mustWriteFile(t, filepath.Join(repo, ".nvim.lua"), own)
	if _, err := OnboardRepository(context.Background(), repo, false); err != nil {
		t.Fatalf("onboard: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".nvim.lua"))
	if err != nil || string(data) != own {
		t.Fatalf("onboarding replaced a developer-owned .nvim.lua: %q %v", data, err)
	}
	if !onboardExists(repo, "lua/standards.lua") {
		t.Error("onboarding skipped the missing Praetor module lua/standards.lua")
	}
}

func TestOnboardRepository_FacetsMatchDefault(t *testing.T) {
	repo := verifiedOnboardFixture(t)
	plan, err := OnboardRepository(context.Background(), repo, true)
	if err != nil {
		t.Fatalf("onboard: %v", err)
	}

	want := config.DefaultFacets()
	if len(plan.Facets) != len(want) {
		t.Fatalf("plan.Facets = %v, want %v", plan.Facets, want)
	}
	for i, f := range plan.Facets {
		if f != want[i] {
			t.Errorf("plan.Facets[%d] = %q, want %q", i, f, want[i])
		}
	}
}
