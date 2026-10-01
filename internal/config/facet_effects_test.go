package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// facetEffectsProfile is a profile text at the default branch-protection and supply-chain values.
const facetEffectsProfile = "id: base\nbranch_protection:\n  enforce_linear_history: true\n  require_signed_commits: false\n" +
	"  required_approving_reviewers: 1\n  dismiss_stale_reviews: true\nsupply_chain:\n  slsa_level: 1\n"

// facetEffectsPolicy is a resolved policy declaring profile "base" and facets, retaining
// texts as its catalog: an id ending in .yaml names a profile text, any other a facet text.
func facetEffectsPolicy(facets []string, texts map[string]string) *EffectivePolicy {
	policy := &EffectivePolicy{Manifest: &Manifest{Version: 1, Profiles: []string{"base"}, Facets: facets}}
	for name, content := range texts {
		rel := ".config/archetypes/facets/" + name + ".yaml"
		if strings.HasSuffix(name, ".yaml") {
			rel = ".config/archetypes/" + name
		}
		policy.CatalogArtifacts = append(policy.CatalogArtifacts, PolicyArtifact{RelativePath: rel, Content: []byte(content)})
	}
	return policy
}

// TestFacetEffects_Positive_NamesWhatEachFacetRaises (#596): each declared facet, in declaration
// order, names every branch-protection and supply-chain setting it raises over the profile, by
// catalog key with both values; a facet raising nothing names nothing.
func TestFacetEffects_Positive_NamesWhatEachFacetRaises(t *testing.T) {
	policy := facetEffectsPolicy([]string{"strict", "quiet"}, map[string]string{
		"base.yaml": facetEffectsProfile,
		"strict": "id: strict\nbranch_protection:\n  require_signed_commits: true\n  required_approving_reviewers: 2\n" +
			"supply_chain:\n  slsa_level: 3\n  enforce_cosign: true\n  require_sbom: true\n",
		"quiet": "id: quiet\nlinters:\n  - semgrep\n",
	})
	effects, err := policy.FacetEffects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"branch_protection.required_approving_reviewers 1 -> 2", "branch_protection.require_signed_commits false -> true",
		"supply_chain.slsa_level 1 -> 3", "supply_chain.enforce_cosign false -> true", "supply_chain.require_sbom false -> true"}
	if len(effects) != 2 || effects[0].Facet != "strict" || !slices.Equal(effects[0].Raises, want) ||
		effects[1].Facet != "quiet" || len(effects[1].Raises) != 0 {
		t.Fatalf("facet effects = %+v, want strict raising %v and quiet raising nothing", effects, want)
	}
}

// TestFacetEffects_Positive_ShippedDefaultsOverTemplateSeed pins the summary against the shipped
// catalog: over template-seed, security:high and api:public-contract raise the reviewer count
// and signed commits, security:high also the supply chain, and docs:seo-portal and
// agent:sandboxed raise neither section (the settings #596 tabulates).
func TestFacetEffects_Positive_ShippedDefaultsOverTemplateSeed(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	manifest := &Manifest{Version: 1, Profiles: []string{"template-seed"}, Facets: DefaultFacets()}
	rendered, err := RenderManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := BuildLockfile(t.Context(), root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := LoadEffectivePolicyInputsContext(t.Context(), EffectiveOptions{Root: t.TempDir(), CatalogRoot: root, Audit: true}, rendered, lock)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := policy.FacetEffects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	reviews := []string{"branch_protection.required_approving_reviewers 1 -> 2", "branch_protection.require_signed_commits false -> true"}
	want := map[string][]string{
		"security:high":       append(slices.Clone(reviews), "supply_chain.slsa_level 1 -> 3", "supply_chain.enforce_cosign false -> true", "supply_chain.require_sbom false -> true"),
		"api:public-contract": reviews,
		"docs:seo-portal":     nil,
		"agent:sandboxed":     nil,
	}
	if len(effects) != len(want) {
		t.Fatalf("effects for %d facets, want %d: %+v", len(effects), len(want), effects)
	}
	for _, effect := range effects {
		if !slices.Equal(effect.Raises, want[effect.Facet]) {
			t.Errorf("%s raises %v, want %v", effect.Facet, effect.Raises, want[effect.Facet])
		}
	}
}

// TestFacetEffects_Negative_RefusesWhatItCannotPair: a policy without its manifest, a declared
// facet or profile whose text the policy did not retain, and a retained text the archetype
// reader refuses are errors, never an empty summary.
func TestFacetEffects_Negative_RefusesWhatItCannotPair(t *testing.T) {
	if _, err := (*EffectivePolicy)(nil).FacetEffects(t.Context()); err == nil {
		t.Fatal("a nil policy must be refused")
	}
	if _, err := (&EffectivePolicy{}).FacetEffects(t.Context()); err == nil {
		t.Fatal("a policy without its manifest must be refused")
	}
	cases := map[string]*EffectivePolicy{
		`facet "absent"`:  facetEffectsPolicy([]string{"absent"}, map[string]string{"base.yaml": facetEffectsProfile}),
		`profile "base"`:  facetEffectsPolicy([]string{"quiet"}, map[string]string{"quiet": "id: quiet\n"}),
		"parse archetype": facetEffectsPolicy(nil, map[string]string{"base.yaml": facetEffectsProfile, "bad": "id: bad\nunknown_key: 1\n"}),
	}
	for want, policy := range cases {
		if _, err := policy.FacetEffects(t.Context()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want an error naming %s, got %v", want, err)
		}
	}
}

// TestFacetEffects_Boundary_EmptyEqualAndOversized: no declared facet is an empty summary, a
// facet repeating the profile's values raises nothing, a facet declared as a profile is not
// paired with a profile text, and a declaration past the lock bound is refused.
func TestFacetEffects_Boundary_EmptyEqualAndOversized(t *testing.T) {
	effects, err := facetEffectsPolicy(nil, map[string]string{"base.yaml": facetEffectsProfile}).FacetEffects(t.Context())
	if err != nil || len(effects) != 0 {
		t.Fatalf("no declared facet must summarise nothing: %+v, %v", effects, err)
	}
	same := facetEffectsPolicy([]string{"same"}, map[string]string{"base.yaml": facetEffectsProfile,
		"same": strings.Replace(facetEffectsProfile, "id: base", "id: same", 1)})
	if effects, err := same.FacetEffects(t.Context()); err != nil || len(effects) != 1 || len(effects[0].Raises) != 0 {
		t.Fatalf("a facet at the profile's values must raise nothing: %+v, %v", effects, err)
	}
	if _, err := facetEffectsPolicy([]string{"base"}, map[string]string{"base.yaml": facetEffectsProfile}).FacetEffects(t.Context()); err == nil {
		t.Fatal("a profile text must not stand in for a facet of the same id")
	}
	oversized := facetEffectsPolicy(make([]string, maxLockEntries+1), nil)
	if _, err := oversized.FacetEffects(t.Context()); err == nil {
		t.Fatal("a declaration past the lock bound must be refused")
	}
}
