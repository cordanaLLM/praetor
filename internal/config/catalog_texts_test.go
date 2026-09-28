package config

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// Positive: every declared profile, then every declared facet, comes back in manifest order,
// resolved by its content-declared id whatever its file is named.
func TestDeclaredCatalogTexts_Positive_ManifestOrder(t *testing.T) {
	root := t.TempDir()
	writeLockTestCatalog(t, root, "framework.yaml", lockTestSource)
	writeLockTestCatalog(t, root, "renamed-service.yaml", "id: app-service\nname: Service\n")
	writeLockTestCatalog(t, root, "facets/security-high.yaml", "id: \"security:high\"\nname: High\n")
	manifest := &Manifest{Version: 1, Profiles: []string{"app-service", "framework"}, Facets: []string{"security:high"}}
	texts, err := DeclaredCatalogTexts(context.Background(), root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	want := []CatalogText{
		{Kind: "profile", ID: "app-service", Content: []byte("id: app-service\nname: Service\n")},
		{Kind: "profile", ID: "framework", Content: []byte(lockTestSource)},
		{Kind: "facet", ID: "security:high", Content: []byte("id: \"security:high\"\nname: High\n")},
	}
	if fmt.Sprintf("%q", texts) != fmt.Sprintf("%q", want) {
		t.Fatalf("DeclaredCatalogTexts() = %q, want %q", texts, want)
	}
}

// Negative: a catalog that defines no archetype with a declared id, a repository without a
// catalog and a missing manifest are errors, never a partial result.
func TestDeclaredCatalogTexts_Negative_MissingSources(t *testing.T) {
	root := t.TempDir()
	writeLockTestCatalog(t, root, "framework.yaml", lockTestSource)
	for name, manifest := range map[string]*Manifest{
		"profile": {Version: 1, Profiles: []string{"framework", "app-service"}},
		"facet":   {Version: 1, Profiles: []string{"framework"}, Facets: []string{"security:high"}},
	} {
		if texts, err := DeclaredCatalogTexts(context.Background(), root, manifest); !errors.Is(err, ErrLockSourceMissing) || texts != nil {
			t.Errorf("%s: an undefined id must be ErrLockSourceMissing, got %q, %v", name, texts, err)
		}
	}
	manifest := &Manifest{Version: 1, Profiles: []string{"framework"}}
	if _, err := DeclaredCatalogTexts(context.Background(), t.TempDir(), manifest); !errors.Is(err, ErrLockUnverifiable) {
		t.Errorf("a repository without .config/archetypes must be ErrLockUnverifiable, got %v", err)
	}
	if _, err := DeclaredCatalogTexts(context.Background(), root, nil); err == nil {
		t.Error("a nil manifest must be refused")
	}
}

// Boundary: a manifest declaring nothing yields no texts; one declaring more entries of a kind
// than a lock may pin is refused before any read; a cancelled context reads nothing.
func TestDeclaredCatalogTexts_Boundary_EntryBounds(t *testing.T) {
	root := t.TempDir()
	writeLockTestCatalog(t, root, "framework.yaml", lockTestSource)
	texts, err := DeclaredCatalogTexts(context.Background(), root, &Manifest{Version: 1})
	if err != nil || len(texts) != 0 {
		t.Errorf("an empty manifest must yield no texts, got %q, %v", texts, err)
	}
	facets := make([]string, maxLockEntries+1)
	for i := range facets {
		facets[i] = fmt.Sprintf("facet-%d", i)
	}
	if _, err := DeclaredCatalogTexts(context.Background(), root, &Manifest{Version: 1, Facets: facets}); err == nil {
		t.Errorf("%d facets must be refused", len(facets))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DeclaredCatalogTexts(ctx, root, &Manifest{Version: 1, Profiles: []string{"framework"}}); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context must stop the read, got %v", err)
	}
}
