// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/changelog"
	"github.com/cordanaLLM/praetor/internal/config"
)

// fragmentClause is how the social row ends where compile-context detects a fragment directory.
const fragmentClause = "conventional commit subject unchanged; " + config.FragmentConvention + " |"

// keepFragmentFixture gives root a fragment directory holding the placeholder a release render
// leaves, the least a repository keeps under version control to have one.
func keepFragmentFixture(t *testing.T, root string) {
	t.Helper()
	writeRegisterFixture(t, root, changelog.FragmentDir+"/"+changelog.FragmentPlaceholder, "")
}

// maxCloneEntries bounds the walk over a fixture tree in cloneTrackedFiles (HISS-02).
const maxCloneEntries = 256

// cloneTrackedFiles copies the regular files below src into dst and creates a directory only
// for a file it holds: what a fresh clone of src's committed tree contains, since git keeps no
// empty directory.
func cloneTrackedFiles(t *testing.T, src, dst string) {
	t.Helper()
	seen := 0
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if seen++; seen > maxCloneEntries {
			return fmt.Errorf("fixture tree exceeds %d entries", maxCloneEntries)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		writeRegisterFixture(t, dst, filepath.ToSlash(rel), string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// renderFragmentFixture renders the register block of a repository holding the given manifest
// (none when empty), with a fragment directory when withDir is set.
func renderFragmentFixture(t *testing.T, manifest string, withDir bool) string {
	t.Helper()
	root := t.TempDir()
	if manifest != "" {
		writeRegisterFixture(t, root, ".standards.yaml", manifest)
	}
	if withDir {
		keepFragmentFixture(t, root)
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
	keepFragmentFixture(t, root)
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
	if err := os.RemoveAll(filepath.Join(root, changelog.FragmentDir)); err != nil {
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

// TestRegisterBlockAgreesWithFreshClone: the block compile-context writes verifies both in the
// checkout that wrote it and in a fresh clone of the same commit, which holds no empty directory.
// Positive: after a release render empties the fragment directory, the placeholder the render
// leaves keeps the clause in both trees. Negative: a bare empty directory is no fragment lane,
// so the clone, which lacks it, verifies against the same block.
func TestRegisterBlockAgreesWithFreshClone(t *testing.T) {
	ctx := context.Background()
	released := t.TempDir()
	if _, err := changelog.CreateFragment(released, changelog.Fragment{Type: changelog.TypeFixed, Title: "Fix a defect"}); err != nil {
		t.Fatal(err)
	}
	agents := writeRegisterFixture(t, released, "AGENTS.md", registerTestSource)
	if _, err := SyncRegisterBlock(ctx, released, agents, true); err != nil {
		t.Fatal(err)
	}
	if err := changelog.RenderReleaseContext(ctx, released, "1.0.0", "2026-10-01"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readRegisterFixture(t, agents), fragmentClause) {
		t.Fatal("block written beside a fragment lacks the fragment convention")
	}
	empty := t.TempDir()
	if err := os.Mkdir(filepath.Join(empty, changelog.FragmentDir), 0o755); err != nil {
		t.Fatal(err)
	}
	emptyAgents := writeRegisterFixture(t, empty, "AGENTS.md", registerTestSource)
	if _, err := SyncRegisterBlock(ctx, empty, emptyAgents, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readRegisterFixture(t, emptyAgents), "changelog fragment") {
		t.Error("an empty fragment directory must not render the fragment convention")
	}
	for _, checkout := range []string{released, empty} {
		clone := t.TempDir()
		cloneTrackedFiles(t, checkout, clone)
		for _, root := range []string{checkout, clone} {
			if _, err := SyncRegisterBlock(ctx, root, filepath.Join(root, "AGENTS.md"), false); err != nil {
				t.Errorf("verify %s: %v", root, err)
			}
		}
	}
}
