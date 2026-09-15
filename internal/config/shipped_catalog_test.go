package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shippedCatalogDir is the repository's own archetype catalog, relative to this package.
const shippedCatalogDir = "../../.config/archetypes"

// requiredArchetypeKeys are the fields every shipped profile must carry. A profile missing one
// still loads -- the decoder tolerates absent sections -- and then contributes nothing where a
// reader expected a bound, which is the silent shape this repository keeps finding.
var requiredArchetypeKeys = []string{"id:", "name:", "description:", "runtime:", "complexity:"}

// TestShippedArchetypes_Positive_EveryProfileLoadsAndIsSelfConsistent reads the catalog this
// repository actually ships rather than a synthetic fixture.
//
// Every other test in this package builds its own archetypes in a temporary root, so until now
// nothing asserted that the files under .config/archetypes are well formed at all. A malformed
// or mis-titled profile would have been found by whichever downstream repository selected it
// first, which is the wrong place to find it.
func TestShippedArchetypes_Positive_EveryProfileLoadsAndIsSelfConsistent(t *testing.T) {
	names := shippedProfileNames(t)
	if len(names) == 0 {
		t.Fatal("no archetypes found; the catalog path is wrong")
	}
	for _, name := range names {
		body := readShippedProfile(t, name)
		id := archetypeID(body)
		want := strings.TrimSuffix(name, ".yaml")
		if id != want {
			t.Errorf("%s declares id %q; the id must match the filename, because the loader keys the catalog by id while the lockfile pins by path", name, id)
		}
		for _, key := range requiredArchetypeKeys {
			if !strings.Contains(body, "\n"+key) && !strings.HasPrefix(body, key) {
				t.Errorf("%s omits %q", name, strings.TrimSuffix(key, ":"))
			}
		}
	}
}

// TestShippedArchetypes_Negative_NoDuplicateIDs guards the one collision the loader rejects at
// runtime. Catching it here names the offending pair; catching it there fails every command.
func TestShippedArchetypes_Negative_NoDuplicateIDs(t *testing.T) {
	seen := make(map[string]string)
	for _, name := range shippedProfileNames(t) {
		id := archetypeID(readShippedProfile(t, name))
		if first, dup := seen[id]; dup {
			t.Errorf("%s and %s both declare id %q", first, name, id)
		}
		seen[id] = name
	}
}

// TestShippedArchetypes_Boundary_OSImageCoversBootArtifacts pins the profile added for the OS
// and kernel forges. The bound is the boundary: os-image exists because those repositories
// build bootable artifacts under stricter limits than the interim gitops-infra profile they
// were forced onto, so a silent relaxation back towards the defaults would defeat the point of
// having added it.
func TestShippedArchetypes_Boundary_OSImageCoversBootArtifacts(t *testing.T) {
	body := readShippedProfile(t, "os-image.yaml")
	for _, want := range []string{"max_func_loc: 60", "slsa_level: 3", "enforce_cosign: true", "require_sbom: true"} {
		if !strings.Contains(body, want) {
			t.Errorf("os-image.yaml no longer declares %q", want)
		}
	}
}

// shippedProfileNames lists the profile files, excluding the facets subdirectory.
func shippedProfileNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(shippedCatalogDir)
	if err != nil {
		t.Fatalf("read shipped catalog: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			names = append(names, entry.Name())
		}
	}
	return names
}

// readShippedProfile returns one profile's text.
func readShippedProfile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(shippedCatalogDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// archetypeID extracts the quoted or bare id without pulling in a decoder the loader does not
// use for this purpose.
func archetypeID(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "id:") {
			continue
		}
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "id:")), `"`)
	}
	return ""
}
