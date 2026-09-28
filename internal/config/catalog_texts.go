package config

import (
	"context"
	"errors"
	"fmt"
)

// CatalogText is the catalog source of one profile or facet a manifest declares.
type CatalogText struct {
	// Kind is "profile" or "facet".
	Kind string
	// ID is the declared id, which the file's own id: field defines.
	ID string
	// Content is the file exactly as the catalog holds it.
	Content []byte
}

// DeclaredCatalogTexts reads, from catalogRoot's .config/archetypes, the source of every profile
// and then every facet manifest declares, in manifest order: the files lock validation hashes
// for those ids. Two catalogs read for one manifest therefore pair up index by index. A catalog
// without .config/archetypes is ErrLockUnverifiable, and a declared id no archetype defines is
// ErrLockSourceMissing, as lock validation reports them. Reads are bounded and confined to
// catalogRoot like lock validation's.
func DeclaredCatalogTexts(ctx context.Context, catalogRoot string, manifest *Manifest) ([]CatalogText, error) {
	if manifest == nil {
		return nil, errors.New("catalog texts require a manifest")
	}
	if len(manifest.Profiles) > maxLockEntries || len(manifest.Facets) > maxLockEntries {
		return nil, fmt.Errorf("manifest exceeds maximum of %d entries per kind", maxLockEntries)
	}
	profiles, facets, err := archetypeSources(ctx, catalogRoot)
	if err != nil {
		return nil, err
	}
	if profiles == nil {
		return nil, fmt.Errorf("%s: %w", catalogRoot, ErrLockUnverifiable)
	}
	texts := make([]CatalogText, 0, len(manifest.Profiles)+len(manifest.Facets))
	texts, err = appendCatalogTexts(ctx, texts, "profile", manifest.Profiles, profiles)
	if err != nil {
		return nil, err
	}
	return appendCatalogTexts(ctx, texts, "facet", manifest.Facets, facets)
}

// appendCatalogTexts appends the source of every id, of one kind, that sources indexes.
func appendCatalogTexts(ctx context.Context, texts []CatalogText, kind string, ids []string, sources map[string]string) ([]CatalogText, error) {
	for i := 0; i < len(ids) && i < maxLockEntries; i++ {
		path, ok := sources[ids[i]]
		if !ok {
			return nil, fmt.Errorf("%s %q: %w", kind, ids[i], ErrLockSourceMissing)
		}
		data, err := readLockSource(ctx, path)
		if err != nil {
			return nil, err
		}
		texts = append(texts, CatalogText{Kind: kind, ID: ids[i], Content: data})
	}
	return texts, nil
}
