package config

import (
	"fmt"
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
