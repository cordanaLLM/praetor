// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// maxDryRunTreeEntries bounds the walk of dryRunTree (HISS-02).
const maxDryRunTreeEntries = 1000

// dryRunTree maps every entry below dir to its bytes, or "dir" for a directory.
func dryRunTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	tree := make(map[string]string)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || len(tree) > maxDryRunTreeEntries {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || entry.IsDir() {
			tree[rel] = "dir"
			return err
		}
		data, err := os.ReadFile(path)
		tree[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return tree
}

// applyBoth runs the same templates-only apply twice, over two directories prepare seeds alike:
// once as a dry run and once for real. It returns both reports and fails the test when the dry
// run changed its directory.
func applyBoth(t *testing.T, force bool, prepare func(dir string)) (dry, applied *flavor.ApplyReport) {
	t.Helper()
	dryDir, realDir := t.TempDir(), t.TempDir()
	prepare(dryDir)
	prepare(realDir)
	before := dryRunTree(t, dryDir)
	dry, err := flavor.ApplyFlavorWith(t.Context(), dryDir, "native-gpu-systems",
		flavor.ApplyOptions{TemplatesOnly: true, DryRun: true, Force: force})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if after := dryRunTree(t, dryDir); !reflect.DeepEqual(before, after) {
		t.Fatalf("the dry run changed the tree: before %v, after %v", before, after)
	}
	applied, err = flavor.ApplyFlavorWith(t.Context(), realDir, "native-gpu-systems",
		flavor.ApplyOptions{TemplatesOnly: true, Force: force})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return dry, applied
}

// sameTemplates fails the test when the dry run and the apply list different templates.
func sameTemplates(t *testing.T, dry, applied *flavor.ApplyReport) {
	t.Helper()
	if !reflect.DeepEqual(dry.CreatedTemplates, applied.CreatedTemplates) ||
		!reflect.DeepEqual(dry.RefreshedTemplates, applied.RefreshedTemplates) ||
		!reflect.DeepEqual(dry.SkippedTemplates, applied.SkippedTemplates) ||
		!reflect.DeepEqual(dry.CoveredTemplates, applied.CoveredTemplates) {
		t.Fatalf("the dry run plans other templates than the apply writes:\ndry   %+v\napply %+v", dry, applied)
	}
}

// Positive: a dry run lists every template the apply creates, and writes neither a template nor
// the session ledger.
func TestApplyFlavor_Positive_DryRunPlansWhatApplyWrites(t *testing.T) {
	dry, applied := applyBoth(t, false, func(string) {})
	sameTemplates(t, dry, applied)
	if len(dry.CreatedTemplates) != 3 || dry.WorkingDirCreated || !applied.WorkingDirCreated {
		t.Fatalf("dry run %+v, apply %+v", dry, applied)
	}
}

// Negative: a dry run that would render settings is refused before anything is read or written:
// the settings writers have no plan.
func TestApplyFlavor_Negative_DryRunRefusesSettings(t *testing.T) {
	dir := t.TempDir()
	report, err := flavor.ApplyFlavorWith(t.Context(), dir, "native-gpu-systems", flavor.ApplyOptions{DryRun: true})
	if !errors.Is(err, flavor.ErrDryRunSettings) || report != nil {
		t.Fatalf("a dry run with settings must be refused: %+v, %v", report, err)
	}
	if tree := dryRunTree(t, dir); len(tree) != 1 {
		t.Fatalf("a refused dry run wrote %v", tree)
	}
}

// Boundary: over an existing template that differs, a plain dry run plans the skip the apply
// makes, and a forced dry run plans the overwrite the forced apply makes, without writing it.
func TestApplyFlavor_Boundary_DryRunOverExistingTemplate(t *testing.T) {
	const operator = "BasedOnStyle: Google\n"
	seed := func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, ".clang-format"), []byte(operator), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, force := range []bool{false, true} {
		dry, applied := applyBoth(t, force, seed)
		sameTemplates(t, dry, applied)
		created := slices.Contains(dry.CreatedTemplates, ".clang-format")
		if created != force || slices.Contains(dry.SkippedTemplates, ".clang-format") == force {
			t.Fatalf("force %v: the existing template is planned wrongly: %+v", force, dry)
		}
	}
}
