package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ValidateCatalogProjectionContext validates the prospective local index without
// writes. Exact snapshots replace matching filenames for parsing, while every
// other existing YAML file participates in duplicate-ID and syntax validation.
// Callers separately authorize replacement and use CAS publication for the files.
func ValidateCatalogProjectionContext(ctx context.Context, targetRoot string, artifacts []PolicyArtifact) error {
	if ctx == nil || targetRoot == "" || len(artifacts) > 2*maxLockEntries {
		return errors.New("catalog projection requires context, root and at most 512 artifacts")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	root, err := filepath.Abs(targetRoot)
	if err != nil {
		return err
	}
	snapshots, err := catalogProjectionSnapshots(ctx, root, artifacts)
	if err != nil {
		return err
	}
	for _, rel := range []string{archetypeDirName, filepath.Join(archetypeDirName, facetDirName)} {
		if _, err := indexArchetypesWithSnapshots(ctx, root, filepath.Join(root, rel), snapshots, true); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func catalogProjectionSnapshots(ctx context.Context, root string, artifacts []PolicyArtifact) (map[string][]byte, error) {
	snapshots := make(map[string][]byte, len(artifacts))
	total := 0
	for i := 0; i < len(artifacts) && i < 2*maxLockEntries; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		artifact := artifacts[i]
		if err := validateCatalogArtifact(artifact); err != nil {
			return nil, err
		}
		total += len(artifact.Content)
		if total > maxPolicyBytes {
			return nil, errors.New("catalog projection exceeds aggregate byte bound")
		}
		path := filepath.Join(root, artifact.RelativePath)
		if _, exists := snapshots[path]; exists {
			return nil, fmt.Errorf("duplicate catalog artifact path %q", artifact.RelativePath)
		}
		snapshots[path] = append([]byte(nil), artifact.Content...)
	}
	return snapshots, nil
}

func validateCatalogArtifact(artifact PolicyArtifact) error {
	if err := validateCatalogPath(artifact.RelativePath); err != nil {
		return err
	}
	if len(artifact.Content) > maxArchetypeBytes || !utf8.Valid(artifact.Content) {
		return errors.New("catalog artifact requires UTF-8 content of at most 1 MiB")
	}
	if artifact.SHA256 != policyDigest(artifact.Content) {
		return errors.New("catalog artifact differs from its validated hash")
	}
	return nil
}

func validateCatalogPath(rel string) error {
	if !filepath.IsLocal(rel) || filepath.Clean(rel) != rel || len(rel) > 4096 || !strings.HasSuffix(rel, ".yaml") {
		return errors.New("catalog artifact requires a bounded clean local YAML path")
	}
	if !utf8.ValidString(rel) || strings.ContainsFunc(rel, unicode.IsControl) {
		return errors.New("catalog artifact path requires UTF-8 without controls")
	}
	// Compare slash-normalised: the catalog constants are slash paths, while filepath.Dir
	// yields the host separator, so a Windows path would otherwise never match.
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir != archetypeDirName && dir != archetypeDirName+"/"+facetDirName {
		return errors.New("catalog artifact must be directly inside the profiles or facets directory")
	}
	return nil
}
