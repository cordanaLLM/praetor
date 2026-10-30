// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

func TestNodeVersionFileSeedDryRun(t *testing.T) {
	rootDry := newTestRepo(t, "node-version-dry")
	optsDry := AdoptOptions{Path: rootDry, Profile: "framework", DryRun: true, LockSourceRoot: newAdoptLockSource(t)}
	reportDry, err := Adopt(t.Context(), optsDry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rootDry, markdownassets.NodeVersionFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dry-run adopt wrote .node-version")
	}
	if !slices.Contains(reportDry.CreatedFiles, markdownassets.NodeVersionFile) {
		t.Fatal("dry-run report did not record created .node-version")
	}
}

func TestNodeVersionFileSeedOnAbsent(t *testing.T) {
	pins, err := devcontainer.LoadCLIPins()
	if err != nil {
		t.Fatal(err)
	}
	wantContent := pins.Node.Version + "\n"

	root := newTestRepo(t, "node-version-seed")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	gotData, err := os.ReadFile(filepath.Join(root, markdownassets.NodeVersionFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotData) != wantContent {
		t.Fatalf(".node-version content = %q, want %q", string(gotData), wantContent)
	}
	if !slices.Contains(report.CreatedFiles, markdownassets.NodeVersionFile) {
		t.Fatal("adopt report did not record created .node-version")
	}

	report2, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	gotData2, err := os.ReadFile(filepath.Join(root, markdownassets.NodeVersionFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotData2) != wantContent {
		t.Fatalf("second run modified .node-version: %q", string(gotData2))
	}
	if slices.Contains(report2.CreatedFiles, markdownassets.NodeVersionFile) {
		t.Fatal("second run recorded created .node-version")
	}
	if !slices.Contains(report2.ReconciledFiles, markdownassets.NodeVersionFile) {
		t.Fatal("second run did not record reconciled .node-version")
	}
}

// assertNoNodeVersionWarning fails when the report calls the repository's own value drift.
func assertNoNodeVersionWarning(t *testing.T, warnings []string) {
	t.Helper()
	for _, warning := range warnings {
		if strings.Contains(warning, markdownassets.NodeVersionFile) {
			t.Fatalf("adopt warned about the repository's own value: %s", warning)
		}
	}
}

func TestNodeVersionFilePreservesAdopterVersion(t *testing.T) {
	root := newTestRepo(t, "node-version-adopter")
	customVersion := "26.12.1\n"
	mustWrite(t, filepath.Join(root, markdownassets.NodeVersionFile), customVersion)

	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, filepath.Join(root, markdownassets.NodeVersionFile))
	if got != customVersion {
		t.Fatalf("adopt overwrote adopter .node-version: got %q, want %q", got, customVersion)
	}
	if !slices.Contains(report.ReconciledFiles, markdownassets.NodeVersionFile) {
		t.Fatal("adopt did not record reconciled .node-version")
	}
	assertNoNodeVersionWarning(t, report.Warnings)

	// --force adopt leaves bytes identical
	optsForce := AdoptOptions{Path: root, Profile: "framework", Force: true, LockSourceRoot: newAdoptLockSource(t)}
	reportForce, err := Adopt(t.Context(), optsForce)
	if err != nil {
		t.Fatal(err)
	}
	gotForce := mustRead(t, filepath.Join(root, markdownassets.NodeVersionFile))
	if gotForce != customVersion {
		t.Fatalf("force adopt overwrote adopter .node-version: got %q, want %q", gotForce, customVersion)
	}
	for _, rep := range reportForce.Replaced() {
		if rep.Path == markdownassets.NodeVersionFile {
			t.Fatalf("force adopt replaced .node-version: %+v", rep)
		}
	}
	assertNoNodeVersionWarning(t, reportForce.Warnings)
}

// Removing the facet removes every managed file of the gate and leaves the version file: the
// repository may use it for more than the gate.
func TestNodeVersionFileStaysWhenFacetDisabled(t *testing.T) {
	root := newTestRepo(t, "node-version-disabled")
	mustWrite(t, filepath.Join(root, readmeFile), "# Disabled\n")
	mustWrite(t, filepath.Join(root, makefileName), "all:\n\t@echo operator\n")
	mustWrite(t, filepath.Join(root, ".prettierrc"), "{}\n")
	mustWrite(t, filepath.Join(root, prettierIgnoreFile), "operator-output/\n")
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	seeded := mustRead(t, filepath.Join(root, markdownassets.NodeVersionFile))
	setDocumentationFacet(t, root, false)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertDocumentationDisabled(t, root)
	if got := mustRead(t, filepath.Join(root, markdownassets.NodeVersionFile)); got != seeded {
		t.Fatalf("disabling the facet changed .node-version: %q, want %q", got, seeded)
	}
}

// The adopter's Renovate has to reach the file, so the managed entry that turns Renovate off for
// the gate's files must list those files and not this one.
func TestRenovateManagedPathsExcludesNodeVersion(t *testing.T) {
	session := &adoptSession{
		repoPath: newTestRepo(t, "node-version-renovate"),
		arch:     "framework",
		facets:   config.DefaultFacets(),
		report:   &AdoptReport{},
	}
	paths, err := renovateManagedPaths(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, DocumentationWorkflowFile) {
		t.Fatalf("managed paths %v lack %s, so this list is not the gate's", paths, DocumentationWorkflowFile)
	}
	if slices.Contains(paths, markdownassets.NodeVersionFile) {
		t.Fatalf("managed paths %v turn Renovate off for %s", paths, markdownassets.NodeVersionFile)
	}
}
