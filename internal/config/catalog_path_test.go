// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// The catalog directory constants are slash paths, while filepath.Dir yields the host
// separator. On Windows a profile artifact therefore produced `.config\archetypes`, matched
// neither constant, and adoption from a local praetor checkout could not build a lock at all.
//
// The comparison is what this pins, not the platform: catalogDirAllowed is handed the exact
// string filepath.Dir would return on each host, so the Windows shape is exercised on Linux.
func TestCatalogDirAcceptsBothHostSeparators(t *testing.T) {
	allowed := []struct {
		name string
		dir  string
	}{
		{"profiles, slash", ".config/archetypes"},
		{"profiles, windows", `.config\archetypes`},
		{"facets, slash", ".config/archetypes/facets"},
		{"facets, windows", `.config\archetypes\facets`},
		{"facets, mixed", `.config/archetypes\facets`},
	}
	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			if !catalogDirAllowed(tc.dir) {
				t.Errorf("catalogDirAllowed(%q) = false, want true", tc.dir)
			}
		})
	}
}

// Negative: normalising separators must not widen what the catalog accepts. A directory outside
// the two allowed ones stays rejected in either shape.
func TestCatalogDirRejectsEverythingElse(t *testing.T) {
	rejected := []string{
		".config",
		".config/archetypes/facets/nested",
		`.config\archetypes\facets\nested`,
		"archetypes",
		".config/archetypesX",
		"",
		".",
	}
	for _, dir := range rejected {
		if catalogDirAllowed(dir) {
			t.Errorf("catalogDirAllowed(%q) = true, want false", dir)
		}
	}
}

// Boundary: the whole validator accepts a real artifact path on this host, so the helper is
// wired in rather than merely correct in isolation.
func TestValidateCatalogPathAcceptsHostShapedArtifacts(t *testing.T) {
	for _, rel := range []string{
		filepath.Join(".config", "archetypes", "app-service.yaml"),
		filepath.Join(".config", "archetypes", "facets", "security-high.yaml"),
	} {
		if err := validateCatalogPath(rel); err != nil {
			t.Errorf("validateCatalogPath(%q) = %v, want nil", rel, err)
		}
	}
}

// Negative: the validator still refuses an artifact outside the catalog directories, and says so.
func TestValidateCatalogPathRejectsAnArtifactElsewhere(t *testing.T) {
	err := validateCatalogPath(filepath.Join(".config", "app-service.yaml"))
	if err == nil {
		t.Fatal("an artifact outside the catalog directories must be rejected")
	}
	if !strings.Contains(err.Error(), "directly inside") {
		t.Errorf("the error must name the constraint, got %v", err)
	}
}
