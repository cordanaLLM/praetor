// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// TestCatalogPathAcceptsSlashIdentitiesOnEveryHost pins what a catalog path is:
// an identity inside the catalog, written into an adopter's lock and compared
// against the slash constants -- not a location on the host that resolved it.
//
// Both checks in validateCatalogPath used filepath semantics. filepath.Clean and
// filepath.Dir return the host separator, so on Windows the declared slash form
// matched neither, and every pinned artifact was refused with "catalog artifact
// must be directly inside the profiles or facets directory". Adoption from a
// local praetor checkout could not build a lock at all.
func TestCatalogPathAcceptsSlashIdentitiesOnEveryHost(t *testing.T) {
	for _, rel := range []string{
		path.Join(archetypeDirName, "os-image.yaml"),
		path.Join(archetypeDirName, "planning-artifacts.yaml"),
		path.Join(archetypeDirName, facetDirName, "security-high.yaml"),
		".config/archetypes/framework.yaml",
		".config/archetypes/facets/agent-sandboxed.yaml",
	} {
		t.Run(rel, func(t *testing.T) {
			if err := validateCatalogPath(rel); err != nil {
				t.Fatalf("declared catalog identity rejected: %v", err)
			}
		})
	}
}

// TestCatalogPathHandlesAHostSpelledIdentityAsThePlatformDoes records a limit of
// this check rather than a guarantee, because the first version of this test
// asserted unconditional tolerance and failed on Linux.
//
// catalogDirAllowed normalises, but it is handed filepath.Dir(rel), which splits
// on the host separator. On Windows a backslash-spelled identity therefore
// resolves into the catalog directory and is accepted; on POSIX the same string
// is one legal file name whose directory is ".", so it is refused. Both answers
// are correct for their platform. The guarantee this function owes -- that a
// declared slash identity is accepted everywhere -- is asserted above.
func TestCatalogPathHandlesAHostSpelledIdentityAsThePlatformDoes(t *testing.T) {
	err := validateCatalogPath(`.config\archetypes\os-image.yaml`)
	if filepath.Separator == '/' && err == nil {
		t.Fatal("a one-segment POSIX name was accepted as a catalog identity")
	}
	if filepath.Separator != '/' && err != nil {
		t.Fatalf("a Windows-spelled catalog identity was refused: %v", err)
	}
}

// TestCatalogPathRefusesEscapingIdentities is the negative half. Normalising the
// separator must not make the check permissive about anything else: a path outside
// the two catalog directories, an escaping path and a non-YAML file stay refused.
func TestCatalogPathRefusesEscapingIdentities(t *testing.T) {
	for _, rel := range []string{
		".config/archetypes/nested/deep.yaml",
		".config/archetypes/os-image.txt",
		"../escape.yaml",
		".config/archetypes/../../escape.yaml",
		"/absolute/os-image.yaml",
		"os-image.yaml",
		"",
	} {
		t.Run(rel, func(t *testing.T) {
			if err := validateCatalogPath(rel); err == nil {
				t.Fatalf("unsafe or misplaced catalog identity accepted: %q", rel)
			}
		})
	}
}

// TestCatalogPathBoundsAndRejectsControls is the boundary: the 4096 cap and the
// UTF-8/control-character rule still hold once the separator question is settled.
func TestCatalogPathBoundsAndRejectsControls(t *testing.T) {
	within := archetypeDirName + "/" + strings.Repeat("a", 4096-len(archetypeDirName)-len("/.yaml")) + ".yaml"
	if err := validateCatalogPath(within); err != nil {
		t.Fatalf("path at the bound rejected: %v", err)
	}
	if err := validateCatalogPath(within + "a"); err == nil {
		t.Fatal("path past the bound accepted")
	}
	if err := validateCatalogPath(archetypeDirName + "/os\x00image.yaml"); err == nil {
		t.Fatal("control character accepted")
	}
}
