// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package harvester

import (
	"os"
	"path/filepath"
	"testing"
)

// A checkout and its linked worktree must keep one identity when they are spelled through
// an aliased ancestor, and the checkout must still classify as its own kind.
//
// This is the condition the macOS and Windows legs of the Platform Neutrality matrix are in
// permanently and Linux is never in by accident (#135). macOS reaches its own TMPDIR through
// /var -> /private/var; the Windows runner's TMP is the 8.3 short name
// C:\Users\RUNNER~1\AppData\Local\Temp while git answers with C:\Users\runneradmin\...
// Either way the caller holds one spelling and git answers with its own real_path, so
// GitCommonDir arrived in two spellings for one directory and the shared identity assertion
// in TestScanLocalWorkstationRepositoryObservations failed on every run of both legs. A
// symlinked ancestor is the portable way to force the same two-spellings condition here.
//
// Both assertions are load-bearing. Canonicalising GitCommonDir alone would trade the lost
// identity for a misclassification: the git directory would be resolved while
// observation.Path still holds the caller's spelling, so a main checkout would stop matching
// its own .git and be reported as a linked worktree.
func TestInventoryIdentitySurvivesAnAliasedSpelling(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	main := filepath.Join(real, "main")
	initTestRepository(t, main)
	if err := os.MkdirAll(filepath.Join(real, "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(real, "worktrees", "linked")
	if out, err := runTestGit(main, "worktree", "add", "-b", "inventory-alias", linked); err != nil {
		t.Fatalf("add linked worktree: %v (%s)", err, out)
	}
	// Both observations are taken through the alias, exactly as a scan rooted at an aliased
	// developer directory takes them.
	mainObservation := inspectRepository(t.Context(), filepath.Join(alias, "main"))
	linkedObservation := inspectRepository(t.Context(), filepath.Join(alias, "worktrees", "linked"))
	for _, observation := range []RepositoryObservation{mainObservation, linkedObservation} {
		if len(observation.ProbeErrors) != 0 {
			t.Fatalf("an aliased spelling broke the probe: %+v", observation)
		}
	}
	// A checkout without a remote is reported as local-only, which is the "main" branch of
	// the classification with the remote observation applied on top.
	if mainObservation.Classification != "local-only" {
		t.Errorf("checkout misclassified through an alias: %+v", mainObservation)
	}
	if linkedObservation.Classification != "linked-worktree" {
		t.Errorf("linked worktree misclassified through an alias: %+v", linkedObservation)
	}
	if mainObservation.GitCommonDir != linkedObservation.GitCommonDir {
		t.Fatalf("an aliased spelling split one identity: %+v %+v", mainObservation, linkedObservation)
	}
}
