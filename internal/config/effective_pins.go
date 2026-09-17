package config

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

func (l *effectiveLoader) pinnedLayers(opts EffectiveOptions, manifest *Manifest) ([]PolicyLayer, error) {
	path := filepath.Join(opts.Root, ".standards.lock")
	data, err := l.sourceSnapshot(path, "lock")
	if err != nil {
		return nil, err
	}
	if _, err := decodePolicyDocument(l.ctx, data); err != nil {
		return nil, fmt.Errorf("decode policy lock: %w", err)
	}
	lock, err := decodeStandardsLock(data)
	if err != nil {
		return nil, err
	}
	if err := validateEffectivePins(lock, manifest); err != nil {
		return nil, err
	}
	layers := []PolicyLayer{{Source: PolicySource{ID: "lock", Path: path, SHA256: policyDigest(data)}}}
	if len(manifest.Profiles)+len(manifest.Facets) == 0 {
		return layers, nil
	}
	profiles, facets, err := archetypeSources(l.ctx, opts.CatalogRoot)
	if err != nil {
		return nil, err
	}
	profileLayers, err := l.resolvePinnedKind(manifest.Profiles, lock.Profiles, profiles, "profile")
	if err != nil {
		return nil, err
	}
	facetLayers, err := l.resolvePinnedKind(manifest.Facets, lock.Facets, facets, "facet")
	if err != nil {
		return nil, err
	}
	layers = append(layers, profileLayers...)
	return append(layers, facetLayers...), nil
}

func validateEffectivePins(lock *standardsLock, manifest *Manifest) error {
	if err := validateLockMetadata(lock, manifest); err != nil {
		return err
	}
	digest, err := normalizeDigest(lock.Digest)
	if err != nil {
		return err
	}
	if digest != canonicalLockDigest(lock) {
		return fmt.Errorf("%w: top-level digest does not match pinned entries", ErrLockDigestMismatch)
	}
	return nil
}

func (l *effectiveLoader) resolvePinnedKind(ids []string, entries []lockEntry, sources map[string]string, kind string) ([]PolicyLayer, error) {
	pins := make(map[string]lockEntry, len(entries))
	for i := 0; i < len(entries) && i < maxLockEntries; i++ {
		pins[entries[i].ID] = entries[i]
	}
	layers := make([]PolicyLayer, 0, len(ids))
	for i := 0; i < len(ids) && i < maxLockEntries; i++ {
		id := ids[i]
		pin, ok := pins[id]
		if !ok {
			return nil, fmt.Errorf("%w: %s %q", ErrLockEntryMissing, kind, id)
		}
		path, ok := sources[id]
		if !ok {
			return nil, fmt.Errorf("effective policy requires materialized %s %q in the selected catalog", kind, id)
		}
		layer, err := l.pinnedLayer(path, kind, pin)
		if err != nil {
			return nil, err
		}
		layers = append(layers, layer)
	}
	return layers, nil
}

func (l *effectiveLoader) pinnedLayer(path, kind string, pin lockEntry) (PolicyLayer, error) {
	data, err := l.snapshot(path)
	if err != nil {
		return PolicyLayer{}, err
	}
	node, err := decodePolicyDocument(l.ctx, data)
	layer := PolicyLayer{Source: PolicySource{ID: kind + ":" + pin.ID, Path: path, SHA256: policyDigest(data)}}
	if err != nil {
		return layer, err
	}
	digest, err := normalizeDigest(pin.Digest)
	if err != nil {
		return layer, err
	}
	if layer.Source.SHA256 != digest {
		return layer, fmt.Errorf("%w: %s", ErrLockDigestMismatch, layer.Source.ID)
	}
	id := strings.TrimSuffix(filepath.Base(path), ".yaml")
	if value := policyMember(node, "id"); value != nil {
		if err := value.Decode(&id); err != nil {
			return layer, errors.New("archetype ID must be a string")
		}
		id = strings.TrimSpace(id)
	}
	if id != pin.ID {
		return layer, fmt.Errorf("pinned %s identity changed", kind)
	}
	layer.Complexity, err = decodeComplexity(policyMember(node, "complexity"))
	if err == nil {
		l.retainArtifact(path, kind, data, layer.Source.SHA256)
	}
	return layer, err
}

func (l *effectiveLoader) retainArtifact(source, kind string, data []byte, digest string) {
	// RelativePath is the artifact's identity in the catalog, not a location on
	// the host that resolved it: it is compared against the slash constants, is
	// prefix-matched with archetypeDirName+"/", and is written into an adopter's
	// lock. filepath.Join would spell it with the host separator, so a lock
	// written on Windows would not match one written on Linux.
	rel := path.Join(archetypeDirName, filepath.Base(source))
	if kind == "facet" {
		rel = path.Join(archetypeDirName, facetDirName, filepath.Base(source))
	}
	l.artifacts = append(l.artifacts, PolicyArtifact{RelativePath: rel, SHA256: digest, Content: data})
}
