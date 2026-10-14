package config

import (
	"fmt"
	"slices"
	"strings"
)

// DeclaresFacet reports whether facets contains target after validating the
// collection against the same scalar bound used by manifest lock validation.
func DeclaresFacet(facets []string, target string) (bool, error) {
	if strings.TrimSpace(target) == "" {
		return false, fmt.Errorf("facet selector must not be empty")
	}
	if len(facets) > MaxManifestEntriesPerKind {
		return false, fmt.Errorf("manifest facets exceed maximum of %d entries", MaxManifestEntriesPerKind)
	}
	for index := 0; index < len(facets) && index < MaxManifestEntriesPerKind; index++ {
		if facets[index] == target {
			return true, nil
		}
	}
	return false, nil
}

// DefaultFacets returns every facet a default may declare, in declaration order: the set init
// declares, and the set DefaultFacetsFor narrows for one archetype.
func DefaultFacets() []string {
	return []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed"}
}

// facetArchetypes names, for the default facets that fit only some archetypes, the archetypes
// they fit: an API compatibility gate where the repository publishes an API, a documentation
// portal where it publishes documentation. Every other default fits every archetype.
var facetArchetypes = map[string][]string{
	"api:public-contract": {"framework", "library-client", "web-package"},
	"docs:seo-portal":     {"pages-site", "framework", "library-client"},
}

// DefaultFacetsFor returns the facets adoption declares for a repository of archetype when none
// are requested (#1075). security:high and agent:sandboxed fit every archetype;
// api:public-contract and docs:seo-portal only the archetypes facetArchetypes names. A fixed
// list gave a private cluster repository an API gate over its tool modules and a figures tool
// it had no documentation site for.
func DefaultFacetsFor(archetype string) []string {
	defaults := DefaultFacets()
	facets := make([]string, 0, len(defaults))
	for _, facet := range defaults {
		if fits, limited := facetArchetypes[facet]; limited && !slices.Contains(fits, archetype) {
			continue
		}
		facets = append(facets, facet)
	}
	return facets
}

// DescribeDefaultFacets states DefaultFacetsFor in prose for the adopt help: the facets every
// archetype gets, then each limited facet with the archetypes it fits.
func DescribeDefaultFacets() string {
	var always, limited []string
	for _, facet := range DefaultFacets() {
		if fits, ok := facetArchetypes[facet]; ok {
			limited = append(limited, facet+" for "+strings.Join(fits, ", "))
			continue
		}
		always = append(always, facet)
	}
	if len(limited) == 0 {
		return strings.Join(always, ",")
	}
	return strings.Join(always, ",") + ", plus " + strings.Join(limited, " and ")
}
