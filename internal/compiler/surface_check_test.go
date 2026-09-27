package compiler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkDirInRoot makes rel below root a symlink to a fresh real directory that is also below root.
func linkDirInRoot(t *testing.T, root, rel string) {
	t.Helper()
	real := filepath.Join(root, "real-"+strings.TrimPrefix(rel, "."))
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, real, filepath.Join(root, filepath.FromSlash(rel)))
}

// Positive: real directories and regular vendor files pass the check.
func TestCheckVendorTargets_Positive_RealTreeAccepted(t *testing.T) {
	root := t.TempDir()
	writeOutputFixture(t, filepath.Join(root, "CLAUDE.md"), "old claude\n")
	writeOutputFixture(t, filepath.Join(root, ".github", "copilot-instructions.md"), "old copilot\n")
	if err := CheckVendorTargets(t.Context(), root); err != nil {
		t.Fatalf("a real tree must pass: %v", err)
	}
}

// Negative: a selected vendor file behind a symlinked directory is refused, and an unknown client
// fails the selection instead of checking a subset.
func TestCheckVendorTargets_Negative_SymlinkedDirAndUnknownClientRefused(t *testing.T) {
	root := t.TempDir()
	linkDirInRoot(t, root, ".github")
	err := CheckVendorTargets(t.Context(), root)
	if err == nil || !strings.Contains(err.Error(), "target .github/copilot-instructions.md: path component must be a directory, never a symlink") {
		t.Fatalf("want the symlinked .github refused, got %v", err)
	}
	writeOutputFixture(t, filepath.Join(root, ".standards.yaml"), "version: 1\nagent_clients: [claude, vim]\n")
	if err := CheckVendorTargets(t.Context(), root); err == nil || !strings.Contains(err.Error(), "vim") {
		t.Fatalf("want the unknown client named, got %v", err)
	}
}

// Boundary: an empty root passes, and a symlinked directory holding only an unselected client's
// vendor file is not checked, since no write goes there.
func TestCheckVendorTargets_Boundary_EmptyRootAndUnselectedLink(t *testing.T) {
	if err := CheckVendorTargets(t.Context(), t.TempDir()); err != nil {
		t.Fatalf("an empty root must pass: %v", err)
	}
	root := t.TempDir()
	writeOutputFixture(t, filepath.Join(root, ".standards.yaml"), "version: 1\nagent_clients: [claude]\n")
	linkDirInRoot(t, root, ".github")
	if err := CheckVendorTargets(t.Context(), root); err != nil {
		t.Fatalf("an unselected client's directory must not be checked: %v", err)
	}
}

// Positive: a canonical persona and one about to be added pass in a real tree.
func TestCheckPersonaTargets_Positive_RealTreeAccepted(t *testing.T) {
	root := writePersonaFixture(t, "")
	if err := CheckPersonaTargets(t.Context(), root, []string{"repo-auditor.md"}); err != nil {
		t.Fatalf("a real tree must pass: %v", err)
	}
}

// Negative: a symlinked .agents, a symlinked persona directory, a symlinked canonical persona and
// a canonical set above CompileAgents' cap are refused before any persona is written.
func TestCheckPersonaTargets_Negative_RefusesWhatCompileAgentsRefuses(t *testing.T) {
	const refused = "path component must be a directory, never a symlink"
	t.Run("canonical dir", func(t *testing.T) {
		root := t.TempDir()
		linkDirInRoot(t, root, ".agents")
		err := CheckPersonaTargets(t.Context(), root, []string{"repo-auditor.md"})
		if err == nil || !strings.Contains(err.Error(), "read .agents/agents: "+refused) {
			t.Fatalf("want the symlinked .agents refused, got %v", err)
		}
	})
	t.Run("persona dir", func(t *testing.T) {
		root := writePersonaFixture(t, "")
		linkDirInRoot(t, root, ".claude")
		err := CheckPersonaTargets(t.Context(), root, nil)
		if err == nil || !strings.Contains(err.Error(), "target .claude/agents/helper.md: "+refused) {
			t.Fatalf("want the symlinked .claude refused, got %v", err)
		}
	})
	t.Run("canonical persona", func(t *testing.T) {
		root := writePersonaFixture(t, "")
		victim := filepath.Join(root, "victim.md")
		writeOutputFixture(t, victim, "victim\n")
		symlinkOrSkip(t, victim, filepath.Join(root, ".agents", "agents", "linked.md"))
		if err := CheckPersonaTargets(t.Context(), root, nil); !errors.Is(err, errPersonaNotRegular) {
			t.Fatalf("want errPersonaNotRegular, got %v", err)
		}
	})
	t.Run("over the cap", func(t *testing.T) {
		root := personaSetFixture(t, maxAgentProjections)
		err := CheckPersonaTargets(t.Context(), root, []string{"added.md"})
		if err == nil || !strings.Contains(err.Error(), "would hold more than") {
			t.Fatalf("want the oversized set refused, got %v", err)
		}
	})
}

// Boundary: a repository without .agents passes with the personas it is about to add, and a set
// of exactly the cap passes, with an added persona that already exists counted once.
func TestCheckPersonaTargets_Boundary_AbsentCanonicalDirAndExactCap(t *testing.T) {
	if err := CheckPersonaTargets(t.Context(), t.TempDir(), []string{"repo-auditor.md", "repo-gatekeeper.md"}); err != nil {
		t.Fatalf("a root without .agents must pass: %v", err)
	}
	root := personaSetFixture(t, maxAgentProjections)
	if err := CheckPersonaTargets(t.Context(), root, []string{"persona-00.md"}); err != nil {
		t.Fatalf("a set at the cap must pass: %v", err)
	}
}

// personaSetFixture builds a root with n canonical personas named persona-NN.md.
func personaSetFixture(t *testing.T, n int) string {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		writeOutputFixture(t, filepath.Join(root, ".agents", "agents", fmt.Sprintf("persona-%02d.md", i)), "persona\n")
	}
	return root
}
