// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// symlinkOrSkip links target at link, or skips where the platform refuses symlinks (HISS-21).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if isSymlinkPermissionError(err) {
			t.Skipf("symlinks refused on this platform: %v", err)
		}
		t.Fatal(err)
	}
}

// Positive (order engine-stage-cleanup-replaced-link-20261019): a verify-all that replaces the
// linked node_modules with a real directory, as npm ci does, passes, and the checkout's folder
// stays as it was.
func TestRunGatedPipeline_Positive_ReplacedDependencyLinkIsCleanedUp(t *testing.T) {
	requireMake(t)
	dir := createIgnoredDependencyRepo(t, "@rm node_modules && mkdir -p node_modules/pkg && touch node_modules/pkg/installed")
	depFile := filepath.Join(dir, "node_modules", "x", "ok")
	writeFile(t, depFile, "dependency content\n")

	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	skipIfWindowsSymlinksRefused(t, rep)
	got := stageNamed(t, rep, stageDeclaredVerify)
	if rep.Status != StatusAdmitted || got.Status != StagePassed {
		t.Fatalf("status %s, stage %s %q; want admitted with the stage passed", rep.Status, got.Status, got.Message)
	}
	if _, err := os.Stat(depFile); err != nil {
		t.Fatalf("checkout dependency folder was damaged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "pkg")); !os.IsNotExist(err) {
		t.Fatalf("the stage's install reached the checkout: %v", err)
	}
}

// Negative: a link the stage made to a folder outside is unlinked, never followed.
func TestCleanupLinkedFolder_Negative_OutsideLinkIsNeverFollowed(t *testing.T) {
	stage, outside := t.TempDir(), t.TempDir()
	keep := filepath.Join(outside, "keep")
	writeFile(t, keep, "outside\n")
	link := filepath.Join(stage, "node_modules")
	symlinkOrSkip(t, outside, link)

	if err := cleanupLinkedFolder(stage, link); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the link is still there: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("cleanup followed the link and removed %s: %v", keep, err)
	}
}

// Boundary: a folder whose directory now resolves outside the stage worktree is refused, and
// nothing outside is removed.
func TestCleanupLinkedFolder_Boundary_PathEscapeIsRefused(t *testing.T) {
	stage, outside := t.TempDir(), t.TempDir()
	keep := filepath.Join(outside, "node_modules", "keep")
	writeFile(t, keep, "outside\n")
	symlinkOrSkip(t, outside, filepath.Join(stage, "web"))

	err := cleanupLinkedFolder(stage, filepath.Join(stage, "web", "node_modules"))
	if err == nil || !strings.Contains(err.Error(), "resolves outside the stage worktree") {
		t.Fatalf("err %v; want a refusal naming the escape", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("cleanup removed %s outside the stage worktree: %v", keep, err)
	}
}

// Boundary: a folder already gone is no error.
func TestCleanupLinkedFolder_Boundary_MissingFolderIsNoError(t *testing.T) {
	stage := t.TempDir()
	if err := cleanupLinkedFolder(stage, filepath.Join(stage, "node_modules")); err != nil {
		t.Fatalf("cleanup of a missing folder: %v", err)
	}
}

// Boundary: under a directory that resolves outside, an entry that is no directory (a file or a
// link there) is refused too, never unlinked outside the stage worktree.
func TestCleanupLinkedFolder_Boundary_EscapedEntryIsNotUnlinked(t *testing.T) {
	stage, outside := t.TempDir(), t.TempDir()
	keep := filepath.Join(outside, "node_modules")
	writeFile(t, keep, "outside file\n")
	symlinkOrSkip(t, outside, filepath.Join(stage, "web"))

	err := cleanupLinkedFolder(stage, filepath.Join(stage, "web", "node_modules"))
	if err == nil || !strings.Contains(err.Error(), "its directory resolves outside the stage worktree") {
		t.Fatalf("err %v; want a refusal naming the escaped directory", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("cleanup unlinked %s outside the stage worktree: %v", keep, err)
	}
}
