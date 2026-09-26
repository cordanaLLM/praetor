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

// LockStatus names the outcome of a lockfile validation that found no defect. An
// invalid lock is an error, never a status.
type LockStatus string

const (
	// LockStatusVerified means every declared profile and facet was hashed against its
	// catalog source and matched its pin.
	LockStatusVerified LockStatus = "verified"
	// LockStatusUnverifiable means the pins and the aggregate digest are well formed,
	// but the selected catalog has no .config/archetypes directory to hash against.
	LockStatusUnverifiable LockStatus = "unverifiable"
)

// LockValidation reports what ValidateLockfile established about a valid lock.
type LockValidation struct {
	Profiles int
	Facets   int
	Status   LockStatus
}

// Verified reports whether every declared entry was hashed against its catalog source.
func (v *LockValidation) Verified() bool {
	return v != nil && v.Status == LockStatusVerified
}

// LockValidationOptions selects the lockfile and the catalog its content digests are
// recomputed from.
type LockValidationOptions struct {
	// Root holds .standards.lock; the lock read is confined to it.
	Root string
	// CatalogRoot holds the pinned .config/archetypes. Empty selects Root and a relative
	// path resolves against the working directory, as EffectiveOptions resolves its
	// CatalogRoot; callers authorize an explicit path.
	CatalogRoot string
	// RequireSources fails with ErrLockUnverifiable instead of returning
	// LockStatusUnverifiable. Gates that certify content digests set it.
	RequireSources bool
}

// ErrLockVersionInvalid reports an unsupported schema version or an unpinned version.
var ErrLockVersionInvalid = errors.New("lockfile requires version 1 and exact SemVer pins")

// Accept the same optional v prefix and SemVer grammar as release preparation.
var lockVersionPattern = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// ValidateLockfile validates root's .standards.lock against the catalog in root; it
// is ValidateLockfileWithOptions with only Root set.
func ValidateLockfile(ctx context.Context, root string, manifest *Manifest) (*LockValidation, error) {
	return ValidateLockfileWithOptions(ctx, LockValidationOptions{Root: root}, manifest)
}

// ValidateLockfileWithOptions validates a YAML or JSON .standards.lock against its
// manifest without modifying files. It checks exact version pins, every entry digest,
// the aggregate digest, and the content of every declared archetype in the catalog.
// A catalog with .config/archetypes must define every declared id. A catalog without
// one yields LockStatusUnverifiable, or ErrLockUnverifiable under RequireSources. All
// reads are bounded; the lock read is confined to Root and catalog reads to CatalogRoot.
func ValidateLockfileWithOptions(ctx context.Context, opts LockValidationOptions, manifest *Manifest) (*LockValidation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if manifest == nil {
		return nil, errors.New("lock validation requires a manifest")
	}
	path, err := util.ConfinePath(opts.Root, ".standards.lock")
	if err != nil {
		return nil, fmt.Errorf(".standards.lock escapes the repository root: %w", err)
	}
	lock, err := loadStandardsLock(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := validateLockMetadata(lock, manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	catalog := opts.CatalogRoot
	if catalog == "" {
		catalog = filepath.Dir(path)
	}
	unverified, err := verifyLockSources(ctx, catalog, lock, manifest)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := verifyAggregateDigest(lock); err != nil {
		return nil, fmt.Errorf("%s %w", path, err)
	}
	return lockValidationResult(path, lock, unverified, opts.RequireSources)
}

// verifyLockSources hashes the declared profiles and facets in catalog and returns how
// many had no catalog to hash against.
func verifyLockSources(ctx context.Context, catalog string, lock *standardsLock, manifest *Manifest) (int, error) {
	profiles, facets, err := archetypeSources(ctx, catalog)
	if err != nil {
		return 0, err
	}
	unverifiedProfiles, err := verifyLockEntries(ctx, manifest.Profiles, lock.Profiles, profiles, "profile")
	if err != nil {
		return 0, err
	}
	unverifiedFacets, err := verifyLockEntries(ctx, manifest.Facets, lock.Facets, facets, "facet")
	if err != nil {
		return 0, err
	}
	return unverifiedProfiles + unverifiedFacets, nil
}

func lockValidationResult(path string, lock *standardsLock, unverified int, requireSources bool) (*LockValidation, error) {
	result := &LockValidation{Profiles: len(lock.Profiles), Facets: len(lock.Facets), Status: LockStatusVerified}
	if unverified == 0 {
		return result, nil
	}
	if requireSources {
		return nil, fmt.Errorf("%s: %d declared profiles and facets: %w", path, unverified, ErrLockUnverifiable)
	}
	result.Status = LockStatusUnverifiable
	return result, nil
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
