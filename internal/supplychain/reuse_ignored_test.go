// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"os"
	"path/filepath"
	"testing"
)

func ignoredNames(globs ...string) map[string]string {
	names := map[string]string{}
	for _, path := range ReuseIgnoredPaths([]ReuseAnnotation{{Paths: globs}}) {
		names[path.Path] = path.Name
	}
	return names
}

// Positive (#1287): a glob whose last segment is a licence file name reuse skips, a .license
// sidecar, an SPDX document, or one below a directory reuse skips, labels nothing and is named.
func TestReuseIgnoredPathsNamesSkippedFiles(t *testing.T) {
	want := map[string]string{
		".agents/skills/caveman/LICENSE": "LICENSE",
		"**/LICENSE":                     "LICENSE",
		"skills/*/LICENSE":               "LICENSE",
		"docs/LICENSE.md":                "LICENSE.md",
		"vendor/x/LICENCE-MIT":           "LICENCE-MIT",
		"COPYING":                        "COPYING",
		"src/logo.svg.license":           "logo.svg.license",
		"LICENSES/MIT.txt":               "LICENSES",
		"sbom.spdx.json":                 "sbom.spdx.json",
	}
	got := ignoredNames(func() []string {
		globs := make([]string, 0, len(want))
		for glob := range want {
			globs = append(globs, glob)
		}
		return globs
	}()...)
	for glob, name := range want {
		if got[glob] != name {
			t.Errorf("%q: named %q, want %q", glob, got[glob], name)
		}
	}
}

// Negative: globs that can match files reuse reads are not named: the whole tree, a file that
// only holds the word, a name that continues LICENSE without "-" or ".", and an escaped star.
func TestReuseIgnoredPathsKeepsReadFiles(t *testing.T) {
	if got := ignoredNames("**", "tools/figures/dist/**", "skills/caveman/SKILL.md", "docs/licensing.md",
		"MYLICENSE", "LICENSEE.txt", `docs/\*LICENSE`, "src/license.go"); len(got) != 0 {
		t.Errorf("named %v", got)
	}
}

// Boundary: a segment with a star can match other names, so it is not named even when one of its
// matches is a LICENSE; the table number counts from 1 in file order; the engine's own REUSE.toml
// names no skipped file.
func TestReuseIgnoredPathsBoundaries(t *testing.T) {
	if got := ignoredNames("skills/caveman/LICEN*", "*/LICENSES*/x", "LICENSE*"); len(got) != 0 {
		t.Errorf("a starred segment was named: %v", got)
	}
	paths := ReuseIgnoredPaths([]ReuseAnnotation{{Paths: []string{"**"}}, {Paths: []string{"README.md", "a/LICENSE"}}})
	if len(paths) != 1 || paths[0].Table != 2 || paths[0].Path != "a/LICENSE" {
		t.Errorf("paths %+v", paths)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", ReuseFile))
	if err != nil {
		t.Fatal(err)
	}
	tables, err := ReuseAnnotationTables(string(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range ReuseIgnoredPaths(tables) {
		t.Errorf("the engine's %s: %s", ReuseFile, path)
	}
}
