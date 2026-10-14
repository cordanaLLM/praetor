package config

import (
	"fmt"
	"slices"
	"testing"
)

func TestDeclaresFacetCompleteValidatedInventory(t *testing.T) {
	facets := make([]string, MaxManifestEntriesPerKind)
	for index := 0; index < len(facets) && index < MaxManifestEntriesPerKind; index++ {
		facets[index] = fmt.Sprintf("custom:%03d", index)
	}
	facets[len(facets)-1] = "docs:seo-portal"
	enabled, err := DeclaresFacet(facets, "docs:seo-portal")
	if err != nil || !enabled {
		t.Fatalf("facet at validated boundary was missed: enabled=%v err=%v", enabled, err)
	}
	if _, err := DeclaresFacet(append(facets, "custom:overflow"), "docs:seo-portal"); err == nil {
		t.Fatal("facet inventory above the validated manifest bound was accepted")
	}
}

func TestDeclaresFacetNegativeAndArguments(t *testing.T) {
	enabled, err := DeclaresFacet([]string{"security:high"}, "docs:seo-portal")
	if err != nil || enabled {
		t.Fatalf("absent facet result: enabled=%v err=%v", enabled, err)
	}
	if _, err := DeclaresFacet(nil, ""); err == nil {
		t.Fatal("empty facet selector accepted")
	}
}

func TestDefaultFacets(t *testing.T) {
	expected := []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed"}
	got := DefaultFacets()
	if len(got) != len(expected) {
		t.Fatalf("DefaultFacets() returned %d items, want %d", len(got), len(expected))
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("DefaultFacets()[%d] = %q, want %q", i, got[i], expected[i])
		}
	}

	shipped := shippedFacetIndex(t)
	for _, facet := range got {
		if _, ok := shipped[facet]; !ok {
			t.Errorf("DefaultFacets() includes %q, which is not a shipped facet", facet)
		}
	}

	got[0] = "mutated"
	got2 := DefaultFacets()
	if got2[0] == "mutated" {
		t.Error("DefaultFacets() returned a shared mutable slice, violating the fresh-slice contract")
	}
}

// DefaultFacetsFor (#1075): positive, the API and documentation facets only where the archetype
// publishes them; negative, a cluster repository gets neither; boundary, an archetype nobody
// named still gets the two that fit every archetype, in DefaultFacets order.
func TestDefaultFacetsFor_3D(t *testing.T) {
	for archetype, want := range map[string][]string{
		"framework":      DefaultFacets(),
		"library-client": DefaultFacets(),
		"web-package":    {"security:high", "api:public-contract", "agent:sandboxed"},
		"pages-site":     {"security:high", "docs:seo-portal", "agent:sandboxed"},
		"gitops-infra":   {"security:high", "agent:sandboxed"},
		"os-image":       {"security:high", "agent:sandboxed"},
		"":               {"security:high", "agent:sandboxed"},
	} {
		if got := DefaultFacetsFor(archetype); !slices.Equal(got, want) {
			t.Errorf("DefaultFacetsFor(%q) = %v, want %v", archetype, got, want)
		}
	}
}

// TestDescribeDefaultFacets_Positive_NamesEveryArchetypeLimit (#1075): the adopt help states the
// facets every archetype gets and, for each limited facet, the archetypes DefaultFacetsFor gives
// it to.
func TestDescribeDefaultFacets_Positive_NamesEveryArchetypeLimit(t *testing.T) {
	want := "security:high,agent:sandboxed, plus api:public-contract for framework, library-client, web-package " +
		"and docs:seo-portal for pages-site, framework, library-client"
	if got := DescribeDefaultFacets(); got != want {
		t.Fatalf("DescribeDefaultFacets() = %q, want %q", got, want)
	}
}
