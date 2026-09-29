// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"
	"testing"
)

// Positive: a declared list decodes as written, a surface without docs is accepted as
// unmapped, and the list survives RenderManifest, which adoption uses to rewrite the manifest.
func TestLoadManifestDocsSurfacesPositive(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, `version: 1
docs_surfaces:
  - name: "build script"
    paths:
      - "scripts/build.sh"
    exclude:
      - "scripts/*_test.sh"
    docs:
      - "docs/onboarding.md"
      - "README.md"
  - name: "release workflow"
    paths:
      - ".github/workflows/release.yml"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.DocsSurfaces) != 2 {
		t.Fatalf("decoded %d surfaces, want 2: %+v", len(m.DocsSurfaces), m.DocsSurfaces)
	}
	build, release := m.DocsSurfaces[0], m.DocsSurfaces[1]
	if build.Name != "build script" || strings.Join(build.Paths, ",") != "scripts/build.sh" ||
		strings.Join(build.Exclude, ",") != "scripts/*_test.sh" || strings.Join(build.Docs, ",") != "docs/onboarding.md,README.md" {
		t.Fatalf("build surface = %+v", build)
	}
	if release.Name != "release workflow" || len(release.Docs) != 0 {
		t.Fatalf("an unmapped surface must decode with no docs: %+v", release)
	}
	rendered, err := RenderManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadManifest(writeManifest(t, string(rendered)))
	if err != nil {
		t.Fatalf("rendered manifest does not reload: %v\n%s", err, rendered)
	}
	if len(again.DocsSurfaces) != 2 || strings.Join(again.DocsSurfaces[0].Docs, ",") != "docs/onboarding.md,README.md" {
		t.Fatalf("docs_surfaces lost in the render round trip: %+v", again.DocsSurfaces)
	}
	if absent, err := LoadManifest(writeManifest(t, "version: 1\n")); err != nil || absent.DocsSurfaces != nil {
		t.Fatalf("an absent list must decode to none: %+v, %v", absent, err)
	}
}

// Negative: every malformed surface is refused by the manifest loader with its position named.
func TestLoadManifestDocsSurfacesNegative(t *testing.T) {
	cases := map[string]string{
		"  - paths: [\"a.sh\"]\n":                                                          "docs_surfaces[0].name must be a non-empty string",
		"  - name: \" \"\n    paths: [\"a.sh\"]\n":                                         "docs_surfaces[0].name must be a non-empty string",
		"  - name: \"a\\tb\"\n    paths: [\"a.sh\"]\n":                                     "docs_surfaces[0].name must not contain a control character",
		"  - name: \"a\"\n":                                                                "docs_surfaces[0].paths must list at least one glob",
		"  - name: \"a\"\n    paths: [\"/etc/passwd\"]\n":                                  "docs_surfaces[0].paths[0] must be repository-relative",
		"  - name: \"a\"\n    paths: [\"a.sh\"]\n    exclude: [\"../x\"]\n":                "docs_surfaces[0].exclude[0] must not contain an empty, . or .. segment",
		"  - name: \"a\"\n    paths: [\"a.sh\"]\n    docs: [\"**\"]\n":                     "docs_surfaces[0].docs[0] must name a path",
		"  - name: \"a\"\n    paths: [\"a.sh\"]\n  - name: \"a\"\n    paths: [\"b.sh\"]\n": `docs_surfaces[1] repeats the name "a" of docs_surfaces[0]`,
		"  - name: \"a\"\n    paths: [\"a.sh\"]\n    doc: [\"x.md\"]\n":                    "field doc not found",
	}
	for section, want := range cases {
		_, err := LoadManifest(writeManifest(t, "version: 1\ndocs_surfaces:\n"+section))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("section %q: got %v, want an error containing %q", section, err, want)
		}
	}
}

// Boundary: each bound admits its exact limit and refuses one more.
func TestValidateDocsSurfacesBoundary(t *testing.T) {
	surfaces := func(count int) []DocsSurface {
		list := make([]DocsSurface, count)
		for index := range list {
			list[index] = DocsSurface{Name: fmt.Sprintf("s%d", index), Paths: []string{"a.sh"}}
		}
		return list
	}
	globs := func(count int) []string {
		list := make([]string, count)
		for index := range list {
			list[index] = fmt.Sprintf("docs/%d.md", index)
		}
		return list
	}
	if err := ValidateDocsSurfaces(surfaces(MaxDocsSurfaces)); err != nil {
		t.Fatalf("exactly %d surfaces: %v", MaxDocsSurfaces, err)
	}
	if err := ValidateDocsSurfaces(surfaces(MaxDocsSurfaces + 1)); err == nil || !strings.Contains(err.Error(), "maximum is 128") {
		t.Fatalf("one surface over the bound: %v", err)
	}
	if err := ValidateDocsSurfaces([]DocsSurface{{Name: "a", Paths: globs(MaxDocsSurfaceGlobs), Docs: globs(MaxDocsSurfaceGlobs)}}); err != nil {
		t.Fatalf("exactly %d globs: %v", MaxDocsSurfaceGlobs, err)
	}
	err := ValidateDocsSurfaces([]DocsSurface{{Name: "a", Paths: []string{"a.sh"}, Docs: globs(MaxDocsSurfaceGlobs + 1)}})
	if err == nil || !strings.Contains(err.Error(), "docs_surfaces[0].docs has 33 globs; maximum is 32") {
		t.Fatalf("one glob over the bound: %v", err)
	}
	if err := ValidateDocsSurfaces([]DocsSurface{{Name: strings.Repeat("n", MaxDocsSurfaceNameBytes), Paths: []string{"a.sh"}}}); err != nil {
		t.Fatalf("a name of exactly %d bytes: %v", MaxDocsSurfaceNameBytes, err)
	}
	err = ValidateDocsSurfaces([]DocsSurface{{Name: strings.Repeat("n", MaxDocsSurfaceNameBytes+1), Paths: []string{"a.sh"}}})
	if err == nil || !strings.Contains(err.Error(), "exceeds 128 bytes") {
		t.Fatalf("a name one byte over the bound: %v", err)
	}
	if err := ValidateDocsSurfaces(nil); err != nil {
		t.Fatalf("no surfaces: %v", err)
	}
}
