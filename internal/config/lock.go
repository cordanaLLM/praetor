package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// LockValidation reports the contents verified by ValidateLockfile. Source-less
// consumers verify pins and the aggregate digest; local sources are also hashed.
type LockValidation struct {
	Profiles int
	Facets   int
}

// ErrLockVersionInvalid reports an unsupported schema version or an unpinned version.
var ErrLockVersionInvalid = errors.New("lockfile requires version 1 and exact SemVer pins")

// Accept the same optional v prefix and SemVer grammar as release preparation.
var lockVersionPattern = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// ValidateLockfile validates the repository's YAML or JSON .standards.lock against
// its manifest without modifying files. It checks exact version pins, every entry
// digest, the aggregate digest, and available local archetype contents. All reads
// are bounded and confined to root; missing local source directories are allowed.
func ValidateLockfile(ctx context.Context, root string, manifest *Manifest) (*LockValidation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if manifest == nil {
		return nil, errors.New("lock validation requires a manifest")
	}
	path, err := util.ConfinePath(root, ".standards.lock")
	if err != nil {
		return nil, fmt.Errorf(".standards.lock escapes the repository root: %w", err)
	}
	root = filepath.Dir(path)
	lock, err := loadStandardsLock(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := validateLockMetadata(lock, manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	profiles, facets, err := archetypeSources(ctx, root)
	if err != nil {
		return nil, err
	}
	if err := verifyLockEntries(ctx, manifest.Profiles, lock.Profiles, profiles, "profile"); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := verifyLockEntries(ctx, manifest.Facets, lock.Facets, facets, "facet"); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	topLevel, err := normalizeDigest(lock.Digest)
	if err != nil {
		return nil, fmt.Errorf("%s top-level digest: %w", path, err)
	}
	if expected := canonicalLockDigest(lock); topLevel != expected {
		return nil, fmt.Errorf("%s top-level digest is %s%s but the pinned entries hash to %s%s: %w",
			path, digestPrefix, topLevel, digestPrefix, expected, ErrLockDigestMismatch)
	}
	return &LockValidation{Profiles: len(lock.Profiles), Facets: len(lock.Facets)}, nil
}

func validateLockMetadata(lock *standardsLock, manifest *Manifest) error {
	if lock.Version != 1 || !lockVersionPattern.MatchString(lock.PinnedVersion) {
		return ErrLockVersionInvalid
	}
	if len(lock.Profiles) > maxLockEntries || len(lock.Facets) > maxLockEntries ||
		len(manifest.Profiles) > maxLockEntries || len(manifest.Facets) > maxLockEntries {
		return fmt.Errorf("lockfile or manifest exceeds maximum of %d entries per kind", maxLockEntries)
	}
	for _, entries := range [][]lockEntry{lock.Profiles, lock.Facets} {
		if err := validateLockEntryMetadata(entries); err != nil {
			return err
		}
	}
	return nil
}

func validateLockEntryMetadata(entries []lockEntry) error {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.ID == "" {
			return errors.New("lockfile entry ID must not be empty")
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			return fmt.Errorf("duplicate lockfile entry %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		if !lockVersionPattern.MatchString(entry.Version) {
			return fmt.Errorf("%w: entry %q version %q", ErrLockVersionInvalid, entry.ID, entry.Version)
		}
		if _, err := normalizeDigest(entry.Digest); err != nil {
			return fmt.Errorf("entry %q: %w", entry.ID, err)
		}
	}
	return nil
}

// decodeStandardsLock keeps the established YAML format while refusing malformed
// JSON masquerading as YAML, trailing documents, and empty lockfiles.
func decodeStandardsLock(data []byte) (*standardsLock, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("lockfile is empty")
	}
	if (trimmed[0] == '{' || trimmed[0] == '[') && !json.Valid(trimmed) {
		return nil, errors.New("lockfile contains malformed JSON")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var lock standardsLock
	if err := decoder.Decode(&lock); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("lockfile must contain exactly one document")
	}
	return &lock, nil
}
