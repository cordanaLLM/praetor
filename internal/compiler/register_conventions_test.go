// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/changelog"
	"github.com/cordanaLLM/praetor/internal/config"
)

// fragmentClause is how the social row ends where compile-context detects a fragment directory.
const fragmentClause = "conventional commit subject unchanged; " + config.FragmentConvention + " |"

// renderFragmentFixture renders the register block of a repository holding the given manifest
// (none when empty), with a fragment directory when withDir is set.
func renderFragmentFixture(t *testing.T, manifest string, withDir bool) string {
	t.Helper()
	root := t.TempDir()
	if manifest != "" {
		writeRegisterFixture(t, root, ".standards.yaml", manifest)
	}
	if withDir {
		if err := os.Mkdir(filepath.Join(root, changelog.FragmentDir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, block, err := LoadRegisterBlock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return block
}

// TestLoadRegisterBlockFollowsFragmentDirectory: the social row names a changelog fragment only
// where the repository keeps a fragment directory or its manifest says so (#328). Positive: a
// detected directory. Negative: a repository without one carries none of the repository clauses
// the row used to assert for every adopter. Boundary: a manifest key wins over detection, an
// empty one included.
func TestLoadRegisterBlockFollowsFragmentDirectory(t *testing.T) {
	if block := renderFragmentFixture(t, "", true); !strings.Contains(block, fragmentClause) {
		t.Errorf("fragment directory not detected:\n%s", block)
	}
	plain := renderFragmentFixture(t, "", false)
	for _, clause := range []string{"changelog fragment", "receipt fence", "PR template"} {
		if strings.Contains(plain, clause) {
			t.Errorf("repository without a fragment directory or conventions carries %q:\n%s", clause, plain)
		}
	}
	stated := renderFragmentFixture(t, "version: 1\nregister:\n  conventions:\n    social: release notes section unchanged\n", true)
	if !strings.Contains(stated, "conventional commit subject unchanged; release notes section unchanged |") || strings.Contains(stated, "changelog fragment") {
		t.Errorf("manifest convention must replace the detected one:\n%s", stated)
	}
	declined := renderFragmentFixture(t, "version: 1\nregister:\n  conventions:\n    social: \"\"\n", true)
	if declined != plain {
		t.Errorf("an empty social convention must render the universal row despite the directory:\n%s", declined)
	}
}

// TestSyncRegisterBlockFollowsFragmentDirectory: the block compile-context writes verifies while
// the directory stays, and --verify reports drift once the repository retires its fragment lane,
// so a stale changelog clause never survives a verified tree.
func TestSyncRegisterBlockFollowsFragmentDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, changelog.FragmentDir)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	agents := writeRegisterFixture(t, root, "AGENTS.md", registerTestSource)
	if changed, err := SyncRegisterBlock(ctx, root, agents, true); err != nil || !changed {
		t.Fatalf("write: changed=%v err=%v", changed, err)
	}
	if !strings.Contains(readRegisterFixture(t, agents), fragmentClause) {
		t.Fatal("written block lacks the detected fragment convention")
	}
	if _, err := SyncRegisterBlock(ctx, root, agents, false); err != nil {
		t.Fatalf("verify with the directory in place: %v", err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncRegisterBlock(ctx, root, agents, false); !errors.Is(err, ErrRegisterBlockOutOfSync) {
		t.Fatalf("retired fragment lane: error = %v, want %v", err, ErrRegisterBlockOutOfSync)
	}
	if changed, err := SyncRegisterBlock(ctx, root, agents, true); err != nil || !changed {
		t.Fatalf("rewrite: changed=%v err=%v", changed, err)
	}
	if strings.Contains(readRegisterFixture(t, agents), "changelog fragment") {
		t.Fatal("rewritten block still names a changelog fragment")
	}
}
