package adopt

import (
	"slices"
	"strings"
	"testing"
)

// declare runs setManifestDeclaration over data and decodes the result, failing the test on
// either error.
func declare(t *testing.T, data string, lists ...declarationList) (string, []string, []string) {
	t.Helper()
	out, err := setManifestDeclaration(t.Context(), []byte(data), lists)
	if err != nil {
		t.Fatalf("setManifestDeclaration: %v", err)
	}
	manifest, err := decodeTargetManifest(out)
	if err != nil {
		t.Fatalf("the rewritten manifest must decode: %v\n%s", err, out)
	}
	return string(out), manifest.Profiles, manifest.Facets
}

// Positive: a rewritten list takes the operator's own sequence indentation and line endings,
// keeps the comment on its key line, and every other line stays byte for byte.
func TestSetManifestDeclaration_Positive_KeepsEveryOtherLine(t *testing.T) {
	const operator = "# head comment\nversion: 1\n\nprofiles: # primary\n    - framework # old item\n\n# facets below\nfacets:\n    - security:high\nrepository:\n    owner: acme\n"
	for _, eol := range []string{"\n", "\r\n"} {
		data := strings.ReplaceAll(operator, "\n", eol)
		out, profiles, facets := declare(t, data, declarationList{key: manifestProfilesKey, ids: []string{"os-image"}})
		want := strings.ReplaceAll(strings.Replace(operator, "    - framework # old item\n", "    - os-image\n", 1), "\n", eol)
		if out != want || !slices.Equal(profiles, []string{"os-image"}) || !slices.Equal(facets, []string{"security:high"}) {
			t.Fatalf("eol %q: got\n%q\nwant\n%q", eol, out, want)
		}
	}
}

// Boundary: a flow list becomes a block list, an absent key is appended, an empty list is written
// `[]`, and an id YAML would read as another type is quoted so it stays a string.
func TestSetManifestDeclaration_Boundary_LayoutsAndScalars(t *testing.T) {
	out, profiles, facets := declare(t, "version: 1\nprofiles: [framework]\n",
		declarationList{key: manifestProfilesKey, ids: []string{"os-image", "yes"}},
		declarationList{key: manifestFacetsKey, ids: []string{"security:high"}})
	if want := "version: 1\nprofiles:\n  - os-image\n  - \"yes\"\nfacets:\n  - security:high\n"; out != want {
		t.Fatalf("got\n%q\nwant\n%q", out, want)
	}
	if !slices.Equal(profiles, []string{"os-image", "yes"}) || !slices.Equal(facets, []string{"security:high"}) {
		t.Fatalf("decoded profiles %v facets %v", profiles, facets)
	}
	out, _, facets = declare(t, "version: 1\nprofiles:\n  - framework\nfacets: [security:high] # floor\n",
		declarationList{key: manifestFacetsKey})
	if want := "version: 1\nprofiles:\n  - framework\nfacets: [] # floor\n"; out != want || len(facets) != 0 {
		t.Fatalf("got\n%q\nwant\n%q", out, want)
	}
}

// Negative: a flow-style root is not text-patched; the re-encode keeps every key and declares the
// new list. Input that is not one YAML mapping is refused, never rewritten.
func TestSetManifestDeclaration_Negative_FlowRootAndInvalidInput(t *testing.T) {
	out, profiles, _ := declare(t, "{version: 1, profiles: [framework], editors: []}\n",
		declarationList{key: manifestProfilesKey, ids: []string{"os-image"}})
	if !slices.Equal(profiles, []string{"os-image"}) || !strings.Contains(out, "editors") {
		t.Fatalf("a flow root must be re-encoded with every key kept:\n%s", out)
	}
	for _, data := range []string{"- a\n- b\n", "version: 1\n---\nversion: 1\n", "profiles: [\n"} {
		if _, err := setManifestDeclaration(t.Context(), []byte(data), []declarationList{{key: manifestProfilesKey, ids: []string{"x"}}}); err == nil {
			t.Errorf("%q must be refused", data)
		}
	}
}
