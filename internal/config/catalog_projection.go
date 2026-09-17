package config

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/util"
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

// catalogDirAllowed reports whether a catalog artifact's parent directory is one of the two the
// catalog permits, comparing in slash form on every host.
//
// Both sides must be normalised before they are compared. archetypeDirName is a slash constant
// while filepath.Dir yields the host separator, so on Windows a profile artifact produced
// `.config\archetypes`, matched nothing, and adoption from a local praetor checkout could not
// build a lock. Taking the directory as a parameter is what makes the Windows shape testable
// from a Linux host.
func catalogDirAllowed(dir string) bool {
	normalised := util.NormalizeSlashes(dir)
	return normalised == archetypeDirName || normalised == archetypeDirName+"/"+facetDirName
}

func validateCatalogPath(rel string) error {
	// Cleanliness is judged on the slash form, for the same reason catalogDirAllowed
	// normalises below: filepath.Clean returns the host separator, so on Windows this
	// comparison was unequal for every declared slash identity and refused each one
	// with "requires a bounded clean local YAML path" -- before the directory check
	// below was ever reached. filepath.IsLocal is kept as-is because containment is a
	// host question, and it is what still rejects a drive-qualified or escaping path.
	normalised := util.NormalizeSlashes(rel)
	if !filepath.IsLocal(rel) || path.Clean(normalised) != normalised ||
		len(rel) > 4096 || !strings.HasSuffix(rel, ".yaml") {
		return errors.New("catalog artifact requires a bounded clean local YAML path")
	}
	if !utf8.ValidString(rel) || strings.ContainsFunc(rel, unicode.IsControl) {
		return errors.New("catalog artifact path requires UTF-8 without controls")
	}
	// Both sides must be in slash form before they are compared. archetypeDirName is a slash
	// constant, while filepath.Dir yields the host separator, so on Windows a profile artifact
	// produced ".config\\archetypes" and matched nothing -- adoption from a local praetor
	// checkout could not build a lock at all. filepath.ToSlash would fix Windows and leave the
	// behaviour untestable everywhere else, so the normalisation is unconditional.
	if !catalogDirAllowed(filepath.Dir(rel)) {
		return errors.New("catalog artifact must be directly inside the profiles or facets directory")
	}
	return nil
}
