package config

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// maxCatalogCombinations bounds the profile and facet pairs one combination test resolves
// (HISS-02). The shipped catalog holds 14 profiles and 6 facets, 98 pairs with the bare
// profiles.
const maxCatalogCombinations = 4096

// catalogSelector resolves the DevContainer features of a repository that declares profiles
// and facets of the catalog under root. It hashes the catalog once (hashLockCatalog), pins
// each selection as BuildLockfile pins it (buildLockEntries) and loads it through the
// effective-policy loader, so a selection resolves here exactly as it does for an adopter.
type catalogSelector struct {
	root             string
	profiles, facets map[string]string
}

func newCatalogSelector(t *testing.T, root string) catalogSelector {
	t.Helper()
	profiles, facets, err := hashLockCatalog(t.Context(), root)
	if err != nil {
		t.Fatalf("hash catalog %s: %v", root, err)
	}
	return catalogSelector{root: root, profiles: profiles, facets: facets}
}

// inputs renders the manifest and the lock of a repository declaring profiles and facets.
func (s catalogSelector) inputs(t *testing.T, profiles, facets []string) (manifest, lock []byte) {
	t.Helper()
	const version = "v1.0.0"
	pins := &standardsLock{Version: 1, PinnedVersion: version}
	var err error
	if pins.Profiles, err = buildLockEntries(profiles, s.profiles, version, "profile"); err != nil {
		t.Fatal(err)
	}
	if pins.Facets, err = buildLockEntries(facets, s.facets, version, "facet"); err != nil {
		t.Fatal(err)
	}
	pins.Digest = digestPrefix + canonicalLockDigest(pins)
	if lock, err = yaml.Marshal(pins); err != nil {
		t.Fatal(err)
	}
	manifest, err = yaml.Marshal(map[string]any{"version": 1, "profiles": profiles, "facets": facets})
	if err != nil {
		t.Fatal(err)
	}
	return manifest, lock
}

// resolve returns the features of the selection, or the error feature resolution reports. A
// selection the policy loader itself refuses fails the test: that is not a feature conflict.
func (s catalogSelector) resolve(t *testing.T, profiles, facets []string) ([]DevContainerFeature, error) {
	t.Helper()
	manifest, lock := s.inputs(t, profiles, facets)
	opts := EffectiveOptions{Root: filepath.Join(t.TempDir(), "repository"), CatalogRoot: s.root}
	policy, err := LoadEffectivePolicyInputsContext(t.Context(), opts, manifest, lock)
	if err != nil {
		t.Fatalf("load policy of profiles %v with facets %v: %v", profiles, facets, err)
	}
	return ResolveDevContainerFeatures(t.Context(), policy)
}

// assertOneEntryPerFeature fails unless the selection resolves to one entry per feature
// identity.
func (s catalogSelector) assertOneEntryPerFeature(t *testing.T, profiles, facets []string) {
	t.Helper()
	features, err := s.resolve(t, profiles, facets)
	if err != nil {
		t.Errorf("profiles %v with facets %v do not resolve: %v", profiles, facets, err)
		return
	}
	seen := make(map[string]bool, len(features))
	for _, feature := range features {
		if seen[feature.Identity()] {
			t.Errorf("profiles %v with facets %v resolve %s more than once", profiles, facets, feature.Identity())
		}
		seen[feature.Identity()] = true
	}
}

// selections lists, bounded, every profile of the catalog alone and with each one of its
// facets; the catalog restricts no facet to a profile, so each profile takes every facet. The
// last selection declares the whole catalog at once, which a manifest may also do.
func (s catalogSelector) selections(t *testing.T) [][2][]string {
	t.Helper()
	profiles, facets := slices.Sorted(maps.Keys(s.profiles)), slices.Sorted(maps.Keys(s.facets))
	if len(profiles)*(len(facets)+1) > maxCatalogCombinations {
		t.Fatalf("%d profiles and %d facets exceed %d combinations", len(profiles), len(facets), maxCatalogCombinations)
	}
	selected := make([][2][]string, 0, len(profiles)*(len(facets)+1)+1)
	for _, profile := range profiles {
		selected = append(selected, [2][]string{{profile}, {}})
		for _, facet := range facets {
			selected = append(selected, [2][]string{{profile}, {facet}})
		}
	}
	return append(selected, [2][]string{profiles, facets})
}

// featureCatalog writes a catalog of one profile and one facet whose devcontainer_features
// are the given YAML sequences.
func featureCatalog(t *testing.T, profileFeatures, facetFeatures string) catalogSelector {
	t.Helper()
	root := t.TempDir()
	writePolicyFile(t, root, ".config/archetypes/site.yaml", "id: site\ndevcontainer_features:\n"+profileFeatures)
	writePolicyFile(t, root, ".config/archetypes/facets/tooling.yaml", "id: tooling:node\ndevcontainer_features:\n"+facetFeatures)
	return newCatalogSelector(t, root)
}

const (
	nodeFeatureIdentity = "ghcr.io/devcontainers/features/node"
	bareNodeFeature     = "  - " + nodeFeatureIdentity + ":2\n"
	node24Feature       = "  - " + nodeFeatureIdentity + ":2:\n      version: \"24\"\n"
)

// Positive (#663): every profile of the shipped catalog resolves alone, with each facet, and
// with the whole catalog declared at once, to one entry per feature. A file that selects a
// feature with a reference or options another file does not share fails here, naming the
// selection.
func TestShippedFeatures_Positive_EveryProfileWithEveryFacetResolves(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	selector := newCatalogSelector(t, root)
	selections := selector.selections(t)
	if len(selections) < 2 || len(selector.facets) == 0 {
		t.Fatalf("the shipped catalog yields %d selections and %d facets; the catalog path is wrong",
			len(selections), len(selector.facets))
	}
	for _, selection := range selections {
		selector.assertOneEntryPerFeature(t, selection[0], selection[1])
	}
}

// Negative: the shape the shipped catalog had before #663, a profile selecting a feature with
// no options beside a facet selecting it with a version, is found by the same walk and stays
// refused by the strict merge.
func TestShippedFeatures_Negative_DifferingOptionsAcrossFilesAreFound(t *testing.T) {
	selector := featureCatalog(t, bareNodeFeature, node24Feature)
	if _, err := selector.resolve(t, []string{"site"}, []string{}); err != nil {
		t.Fatalf("the profile alone must resolve: %v", err)
	}
	_, err := selector.resolve(t, []string{"site"}, []string{"tooling:node"})
	if err == nil || !strings.Contains(err.Error(), nodeFeatureIdentity) {
		t.Fatalf("differing options of one feature must be refused by name, got %v", err)
	}
	conflicts := 0
	for _, selection := range selector.selections(t) {
		if _, err := selector.resolve(t, selection[0], selection[1]); err != nil {
			conflicts++
		}
	}
	// The profile with the facet, and the whole catalog, which is the same selection here.
	if conflicts != 2 {
		t.Fatalf("the walk found %d conflicting selections, want 2", conflicts)
	}
}

// Boundary: the same reference with the same options in a profile and a facet is one entry
// that keeps its options, and a catalog whose facet selects nothing resolves too.
func TestShippedFeatures_Boundary_AgreeingOptionsAcrossFilesAreOneEntry(t *testing.T) {
	selector := featureCatalog(t, node24Feature, node24Feature)
	for _, selection := range selector.selections(t) {
		selector.assertOneEntryPerFeature(t, selection[0], selection[1])
	}
	features, err := selector.resolve(t, []string{"site"}, []string{"tooling:node"})
	if err != nil || len(features) != 1 || features[0].Options["version"] != "24" {
		t.Fatalf("agreeing options must join into one entry that keeps them: %+v (%v)", features, err)
	}

	empty := featureCatalog(t, node24Feature, "  []\n")
	features, err = empty.resolve(t, []string{"site"}, []string{"tooling:node"})
	if err != nil || len(features) != 1 {
		t.Fatalf("a facet selecting no feature must leave the profile's entry: %+v (%v)", features, err)
	}
}
