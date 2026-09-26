package config

import (
	"fmt"
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
