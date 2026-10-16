// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// archetypeCatalog returns a catalog root whose .config/archetypes holds one profile per id,
// each the repository's framework profile under that id, and an empty facets directory.
func archetypeCatalog(t *testing.T, ids ...string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", ".config", "archetypes", "framework.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".config", "archetypes", "facets"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		writeArchetypeProfile(t, root, id, id, string(text))
	}
	return root
}

// writeArchetypeProfile writes profile file name.yaml below root with id in place of framework.
func writeArchetypeProfile(t *testing.T, root, name, id, framework string) string {
	t.Helper()
	path := filepath.Join(root, ".config", "archetypes", name+".yaml")
	text := strings.Replace(framework, `id: "framework"`, `id: "`+id+`"`, 1)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// profileIDs returns the sorted profile ids archetypeSources indexes below root under ctx.
func profileIDs(t *testing.T, ctx interface{ Done() <-chan struct{} }, index map[string]string) []string {
	t.Helper()
	ids := make([]string, 0, len(index))
	for id := range index {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// One adoption indexed the same catalog three times (#1382); under WithArchetypeIndexCache it is
// indexed once per root. Positive: a second call answers from the cache: a profile rewritten to
// another id of the same size, with its time restored, still reads as the first id. Negative:
// without the cache every call indexes afresh. Boundary: another root under the same context, a
// profile added to the root and a profile whose size changed are each indexed afresh, so no index
// outlives the catalog it was built from.
func TestArchetypeIndexCache_3D(t *testing.T) {
	framework, err := os.ReadFile(filepath.Join("..", "..", ".config", "archetypes", "framework.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	root := archetypeCatalog(t, "alpha")
	cached := WithArchetypeIndexCache(t.Context())
	profiles, _, err := archetypeSources(cached, root)
	if err != nil || !slices.Equal(profileIDs(t, cached, profiles), []string{"alpha"}) {
		t.Fatalf("first index: %v, %v", profiles, err)
	}
	path := filepath.Join(root, ".config", "archetypes", "alpha.yaml")
	stamp := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if profiles, _, err = archetypeSources(cached, root); err != nil || !slices.Equal(profileIDs(t, cached, profiles), []string{"alpha"}) {
		t.Fatalf("index after a time change: %v, %v", profiles, err)
	}
	writeArchetypeProfile(t, root, "alpha", "omega", string(framework))
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if profiles, _, err = archetypeSources(cached, root); err != nil || !slices.Equal(profileIDs(t, cached, profiles), []string{"alpha"}) {
		t.Errorf("a second call under the cache indexed again: %v, %v", profiles, err)
	}
	if profiles, _, err = archetypeSources(t.Context(), root); err != nil || !slices.Equal(profileIDs(t, cached, profiles), []string{"omega"}) {
		t.Errorf("an uncached call: %v, %v; want the catalog as it is", profiles, err)
	}

	// Another root that lists the same file, size and times as the first: only the root tells them
	// apart, and it gets its own index.
	other := archetypeCatalog(t, "alpha")
	otherPath := writeArchetypeProfile(t, other, "alpha", "sigma", string(framework))
	for _, stamped := range []string{path, otherPath, filepath.Dir(path) + "/facets", filepath.Dir(otherPath) + "/facets"} {
		if err := os.Chtimes(stamped, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = archetypeSources(cached, root); err != nil {
		t.Fatal(err)
	}
	if profiles, _, err = archetypeSources(cached, other); err != nil || !slices.Equal(profileIDs(t, cached, profiles), []string{"sigma"}) {
		t.Errorf("another root under the same context: %v, %v", profiles, err)
	}
	writeArchetypeProfile(t, root, "gamma", "gamma", string(framework))
	if profiles, _, err = archetypeSources(cached, root); err != nil || !slices.Equal(profileIDs(t, cached, profiles), []string{"gamma", "omega"}) {
		t.Errorf("a profile added to the root: %v, %v", profiles, err)
	}
	writeArchetypeProfile(t, root, "gamma", "gamma-longer", string(framework))
	if profiles, _, err = archetypeSources(cached, root); err != nil || !slices.Equal(profileIDs(t, cached, profiles), []string{"gamma-longer", "omega"}) {
		t.Errorf("a profile whose size changed: %v, %v", profiles, err)
	}
	if WithArchetypeIndexCache(cached) != cached {
		t.Error("a context that already caches got a second cache")
	}
}
