package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// featureRefsByIdentity maps each DevContainer feature identity the archetype files at paths
// select to the distinct references naming it, sorted. ResolveDevContainerFeatures rejects a
// selection holding two references of one identity, so every file that selects a feature has
// to move to a new major together: a repository declaring two of them otherwise stops
// resolving.
func featureRefsByIdentity(t *testing.T, paths []string) map[string][]string {
	t.Helper()
	refs := make(map[string][]string)
	for _, path := range paths {
		data, err := os.ReadFile(path) // #nosec G304 -- path comes from the catalog index or a test root.
		if err != nil {
			t.Fatal(err)
		}
		archetype, err := decodeArchetype(t.Context(), path, data)
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		for _, feature := range archetype.Controls.DevFeatures {
			identity := feature.Identity()
			if !slices.Contains(refs[identity], feature.Ref) {
				refs[identity] = append(refs[identity], feature.Ref)
				slices.Sort(refs[identity])
			}
		}
	}
	return refs
}

// shippedArchetypePaths lists every profile and facet file of the repository's own catalog.
func shippedArchetypePaths(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	profiles, facets, err := archetypeSources(t.Context(), root)
	if err != nil {
		t.Fatalf("index shipped catalog: %v", err)
	}
	paths := make([]string, 0, len(profiles)+len(facets))
	for _, index := range []map[string]string{profiles, facets} {
		for _, path := range index {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths
}

func TestShippedFeatures_Positive_OneReferencePerFeature(t *testing.T) {
	refs := featureRefsByIdentity(t, shippedArchetypePaths(t))
	if len(refs) == 0 {
		t.Fatal("the shipped catalog selects no DevContainer feature; the catalog path is wrong")
	}
	for identity, named := range refs {
		if len(named) != 1 {
			t.Errorf("the shipped catalog names %s as %v; move every file selecting it to one reference", identity, named)
		}
	}
}

func TestShippedFeatures_Negative_TwoMajorsOfOneFeatureAreFound(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		writePolicyFile(t, root, "site.yaml", "id: site\ndevcontainer_features:\n  - ghcr.io/devcontainers/features/node:2\n"),
		writePolicyFile(t, root, "tooling.yaml", "id: tooling\ndevcontainer_features:\n  - ghcr.io/devcontainers/features/node:1:\n      version: \"24\"\n"),
	}
	want := []string{"ghcr.io/devcontainers/features/node:1", "ghcr.io/devcontainers/features/node:2"}
	if got := featureRefsByIdentity(t, paths)["ghcr.io/devcontainers/features/node"]; !slices.Equal(got, want) {
		t.Fatalf("two majors of one feature not reported: %v", got)
	}
	var artifacts []PolicyArtifact
	for _, path := range paths {
		data, err := os.ReadFile(path) // #nosec G304 -- path is a file this test wrote.
		if err != nil {
			t.Fatal(err)
		}
		artifacts = append(artifacts, PolicyArtifact{RelativePath: filepath.Base(path), Content: data})
	}
	if _, err := ResolveDevContainerFeatures(t.Context(), &EffectivePolicy{CatalogArtifacts: artifacts}); err == nil {
		t.Fatal("a selection naming two majors of one feature resolved")
	}
}

func TestShippedFeatures_Boundary_OneReferenceAcrossFilesIsOne(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		writePolicyFile(t, root, "site.yaml", "id: site\ndevcontainer_features:\n  - ghcr.io/devcontainers/features/node:2\n"),
		writePolicyFile(t, root, "web.yaml", "id: web\ndevcontainer_features:\n  - ghcr.io/devcontainers/features/node:2\n"),
	}
	refs := featureRefsByIdentity(t, paths)
	if got := refs["ghcr.io/devcontainers/features/node"]; len(refs) != 1 || len(got) != 1 {
		t.Fatalf("one reference repeated across files counted as several: %v", refs)
	}
}
