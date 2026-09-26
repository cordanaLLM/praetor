package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// BuildLockfile pins target profiles and facets to actual files in an explicitly
// selected Praetor source bundle. The version comes from that bundle's validated
// lock, never from a default. The deterministic result does not modify either root.
func BuildLockfile(ctx context.Context, sourceRoot string, target *Manifest) ([]byte, error) {
	if err := validateLockBuildInputs(ctx, sourceRoot, target); err != nil {
		return nil, err
	}
	source, err := readLockBuildSources(ctx, sourceRoot)
	if err != nil {
		return nil, err
	}
	lock := &standardsLock{Version: 1, PinnedVersion: source.version}
	lock.Profiles, err = buildLockEntries(ctx, target.Profiles, source.profiles, source.version, "profile")
	if err != nil {
		return nil, err
	}
	lock.Facets, err = buildLockEntries(ctx, target.Facets, source.facets, source.version, "facet")
	if err != nil {
		return nil, err
	}
	if err := validateLockMetadata(lock, target); err != nil {
		return nil, err
	}
	lock.Digest = digestPrefix + canonicalLockDigest(lock)
	return yaml.Marshal(lock)
}

func validateLockBuildInputs(ctx context.Context, sourceRoot string, target *Manifest) error {
	if ctx == nil || target == nil || sourceRoot == "" {
		return errors.New("lock generation requires context, target manifest and explicit source root")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if target.Version != 1 {
		return errors.New("target manifest requires version 1")
	}
	if len(target.Profiles) > maxLockEntries || len(target.Facets) > maxLockEntries {
		return fmt.Errorf("lock generation exceeds %d entries per kind", maxLockEntries)
	}
	return nil
}

type lockBuildSources struct {
	version          string
	profiles, facets map[string]string
}

func readLockBuildSources(ctx context.Context, sourceRoot string) (*lockBuildSources, error) {
	source, err := readLockManifest(ctx, sourceRoot)
	if err != nil {
		return nil, err
	}
	path, err := util.ConfinePath(sourceRoot, ".standards.lock")
	if err != nil {
		return nil, err
	}
	pins, err := validatedSourcePins(ctx, filepath.Dir(path), source, path)
	if err != nil {
		return nil, err
	}
	profiles, facets, err := archetypeSources(ctx, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	return &lockBuildSources{version: pins.PinnedVersion, profiles: profiles, facets: facets}, nil
}

func validatedSourcePins(ctx context.Context, root string, manifest *Manifest, path string) (*standardsLock, error) {
	before, err := readLockSource(ctx, path)
	if err != nil {
		return nil, err
	}
	if _, err := ValidateLockfileWithOptions(ctx, LockValidationOptions{Root: root, RequireSources: true}, manifest); err != nil {
		return nil, fmt.Errorf("validate lock source: %w", err)
	}
	after, err := readLockSource(ctx, path)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(before, after) {
		return nil, errors.New("lock source changed during validation")
	}
	return decodeStandardsLock(before)
}

func readLockManifest(ctx context.Context, root string) (*Manifest, error) {
	path, err := util.ConfinePath(root, ".standards.yaml")
	if err != nil {
		return nil, err
	}
	data, err := readLockSource(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read source manifest: %w", err)
	}
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse source manifest: %w", err)
	}
	if manifest.Version != 1 {
		return nil, errors.New("source manifest requires version 1")
	}
	return &manifest, nil
}

func buildLockEntries(ctx context.Context, ids []string, sources map[string]string, version, kind string) ([]lockEntry, error) {
	entries := make([]lockEntry, 0, len(ids))
	for i := 0; i < len(ids) && i < maxLockEntries; i++ {
		path, ok := sources[ids[i]]
		if !ok {
			return nil, fmt.Errorf("lock source missing %s %q", kind, ids[i])
		}
		digest, err := fileDigest(ctx, path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, lockEntry{ID: ids[i], Version: version, Digest: digestPrefix + digest})
	}
	return entries, nil
}
