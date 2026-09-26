package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// digestPrefix is the only accepted digest algorithm prefix in .standards.lock.
	digestPrefix = "sha256:"
	// archetypeDirName holds the profile archetypes a lockfile pins.
	archetypeDirName = ".config/archetypes"
	// facetDirName holds the facet archetypes a lockfile pins.
	facetDirName = "facets"
	// emptyInputDigest is sha256 of zero bytes, the classic placeholder value.
	emptyInputDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	// maxLockEntries bounds every lockfile loop (HISS-02).
	maxLockEntries = 256
	// MaxManifestEntriesPerKind is the validated profile/facet inventory bound.
	// Consumers that inspect a manifest collection must use the same limit as
	// lock validation so a valid final entry is never silently skipped.
	MaxManifestEntriesPerKind = maxLockEntries
	// maxArchetypeBytes bounds how much of an archetype file is hashed.
	maxArchetypeBytes = 1 << 20
)

var (
	// ErrLockDigestPlaceholder reports a digest that was never computed from real content.
	ErrLockDigestPlaceholder = errors.New("lockfile digest is a placeholder")
	// ErrLockDigestMalformed reports a digest that is not sha256:<64 hex>.
	ErrLockDigestMalformed = errors.New("lockfile digest must be sha256:<64 hex characters>")
	// ErrLockEntryMissing reports a manifest profile or facet that the lockfile does not pin.
	ErrLockEntryMissing = errors.New("lockfile does not pin a declared profile or facet")
	// ErrLockDigestMismatch reports a pinned digest that disagrees with the source file.
	ErrLockDigestMismatch = errors.New("lockfile digest does not match the archetype source")
	// ErrLockSourceMissing reports a present catalog that defines no archetype with a
	// declared id: the archetype was removed, or its content-declared id: was changed.
	ErrLockSourceMissing = errors.New("catalog defines no archetype with the pinned id")
	// ErrLockUnverifiable reports well-formed pins whose content cannot be hashed because
	// the selected catalog has no .config/archetypes directory.
	ErrLockUnverifiable = errors.New("lockfile content digests are unverifiable: the selected catalog has no .config/archetypes directory")
)

// lockEntry pins one archetype or facet at a version and content digest.
type lockEntry struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	Digest  string `yaml:"digest"`
}

// standardsLock is the parsed .standards.lock document.
type standardsLock struct {
	Version       int         `yaml:"version"`
	PinnedVersion string      `yaml:"pinned_version"`
	Digest        string      `yaml:"digest"`
	GeneratedAt   string      `yaml:"generated_at,omitempty"`
	Profiles      []lockEntry `yaml:"profiles"`
	Facets        []lockEntry `yaml:"facets"`
}

// loadStandardsLock reads and parses a .standards.lock file.
func loadStandardsLock(ctx context.Context, path string) (*standardsLock, error) {
	data, err := readLockSource(ctx, path)
	if err != nil {
		return nil, err
	}
	lock, err := decodeStandardsLock(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return lock, nil
}

// normalizeDigest validates the sha256:<hex> shape and rejects placeholder values,
// returning the bare hex digest.
func normalizeDigest(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, digestPrefix) {
		return "", fmt.Errorf("%w: %q", ErrLockDigestMalformed, raw)
	}
	value := strings.ToLower(strings.TrimPrefix(trimmed, digestPrefix))
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("%w: %q", ErrLockDigestMalformed, raw)
	}
	if isPlaceholderDigest(value) {
		return "", fmt.Errorf("%w: %q was never computed from real content", ErrLockDigestPlaceholder, raw)
	}
	return value, nil
}

// isPlaceholderDigest recognises digests of empty input and hand-typed repeating patterns
// such as 1234567890abcdef or fedcba0987654321 repeated four times.
func isPlaceholderDigest(value string) bool {
	if value == emptyInputDigest {
		return true
	}
	return value == strings.Repeat(value[:16], 4)
}

// fileDigest returns the lowercase sha256 of a file's contents.
func fileDigest(ctx context.Context, path string) (string, error) {
	data, err := readLockSource(ctx, path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func confinedArchetypePath(root, dir, name string) (string, error) {
	rel, err := filepath.Rel(root, filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	path, err := util.ConfinePath(root, rel)
	if err != nil {
		return "", fmt.Errorf("archetype escapes the repository root: %w", err)
	}
	return path, nil
}

// parseArchetypeID uses the same bounded document parser for on-disk and planned
// sources, defaulting a missing ID to the filename as before.
func parseArchetypeID(ctx context.Context, path string, data []byte) (string, error) {
	node, err := decodePolicyDocument(ctx, data)
	if err != nil {
		return "", fmt.Errorf("parse archetype %s: %w", path, err)
	}
	var doc struct {
		ID string `yaml:"id"`
	}
	if err := node.Decode(&doc); err != nil {
		return "", fmt.Errorf("parse archetype %s: %w", path, err)
	}
	if strings.TrimSpace(doc.ID) == "" {
		return strings.TrimSuffix(filepath.Base(path), ".yaml"), nil
	}
	return doc.ID, nil
}

// canonicalLockDigest is the digest over all pinned entry digests, in a stable order.
func canonicalLockDigest(lock *standardsLock) string {
	lines := make([]string, 0, len(lock.Profiles)+len(lock.Facets))
	for i := 0; i < len(lock.Profiles) && i < maxLockEntries; i++ {
		lines = append(lines, "profile:"+lock.Profiles[i].ID+"="+lock.Profiles[i].Digest)
	}
	for i := 0; i < len(lock.Facets) && i < maxLockEntries; i++ {
		lines = append(lines, "facet:"+lock.Facets[i].ID+"="+lock.Facets[i].Digest)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	return hex.EncodeToString(sum[:])
}

// verifyAggregateDigest checks the top-level digest against the canonical digest of
// every pinned entry. Lock validation and effective policy share this one check.
func verifyAggregateDigest(lock *standardsLock) error {
	topLevel, err := normalizeDigest(lock.Digest)
	if err != nil {
		return fmt.Errorf("top-level digest: %w", err)
	}
	if expected := canonicalLockDigest(lock); topLevel != expected {
		return fmt.Errorf("top-level digest is %s%s but the pinned entries hash to %s%s: %w",
			digestPrefix, topLevel, digestPrefix, expected, ErrLockDigestMismatch)
	}
	return nil
}

// pinnedSource pairs a declared archetype id with its lock pin and catalog source.
// path is empty only when no catalog is materialized.
type pinnedSource struct {
	id   string
	pin  lockEntry
	path string
}

// resolveLockPins is the one declared-id -> pin -> catalog-source resolution shared by
// lock validation and effective policy. A declared id without a pin is
// ErrLockEntryMissing. A nil sources index means no catalog is materialized and every
// path stays empty; a non-nil index must define every declared id, so a removed
// archetype or a changed content-declared id: is ErrLockSourceMissing, never a skip.
func resolveLockPins(declared []string, entries []lockEntry, sources map[string]string, kind string) ([]pinnedSource, error) {
	pins := make(map[string]lockEntry, len(entries))
	for i := 0; i < len(entries) && i < maxLockEntries; i++ {
		pins[entries[i].ID] = entries[i]
	}
	resolved := make([]pinnedSource, 0, len(declared))
	for i := 0; i < len(declared) && i < maxLockEntries; i++ {
		id := declared[i]
		pin, ok := pins[id]
		if !ok {
			return nil, fmt.Errorf("%w: %s %q", ErrLockEntryMissing, kind, id)
		}
		path, ok := sources[id]
		if !ok && sources != nil {
			return nil, fmt.Errorf("lock requires materialized %s %q in the selected catalog: %w", kind, id, ErrLockSourceMissing)
		}
		resolved = append(resolved, pinnedSource{id: id, pin: pin, path: path})
	}
	return resolved, nil
}

// verifyLockEntries hashes every declared entry's catalog source against its pin and
// returns how many entries had no catalog to hash against.
func verifyLockEntries(ctx context.Context, declared []string, entries []lockEntry, sources map[string]string, kind string) (int, error) {
	resolved, err := resolveLockPins(declared, entries, sources, kind)
	if err != nil {
		return 0, err
	}
	unverified := 0
	for i := 0; i < len(resolved) && i < maxLockEntries; i++ {
		entry := resolved[i]
		digest, err := normalizeDigest(entry.pin.Digest)
		if err != nil {
			return 0, fmt.Errorf("%s %q: %w", kind, entry.id, err)
		}
		if entry.path == "" {
			unverified++
			continue
		}
		actual, err := fileDigest(ctx, entry.path)
		if err != nil {
			return 0, err
		}
		if actual != digest {
			return 0, fmt.Errorf("%w: %s %q pins %s%s but %s hashes to %s%s",
				ErrLockDigestMismatch, kind, entry.id, digestPrefix, digest, entry.path, digestPrefix, actual)
		}
	}
	return unverified, nil
}

// archetypeSources indexes a catalog's archetype and facet definitions. Both indexes
// are nil when the catalog has no .config/archetypes directory, the one state in which
// pinned content cannot be recomputed. A present catalog without a facets directory
// yields an empty facet index, so a declared facet is missing rather than unverifiable.
func archetypeSources(ctx context.Context, rootDir string) (profiles, facets map[string]string, err error) {
	profiles, err = optionalArchetypeIndex(ctx, rootDir, archetypeDirName)
	if err != nil || profiles == nil {
		return nil, nil, err
	}
	facets, err = optionalArchetypeIndex(ctx, rootDir, filepath.Join(archetypeDirName, facetDirName))
	if err != nil {
		return nil, nil, err
	}
	if facets == nil {
		facets = map[string]string{}
	}
	return profiles, facets, nil
}

// optionalArchetypeIndex returns a nil index only when the directory is absent; one that
// exists but cannot be indexed, including a path that is not a directory, is an error.
// A relative root resolves against the working directory, as normalizeEffectiveOptions
// resolves CatalogRoot, because ConfinePath returns an absolute path and the index
// relativizes every entry against root.
func optionalArchetypeIndex(ctx context.Context, root, rel string) (map[string]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve archetype catalog root: %w", err)
	}
	path, err := util.ConfinePath(root, rel)
	if err != nil {
		return nil, err
	}
	index, err := indexArchetypesWithSnapshots(ctx, root, path, nil, false)
	if util.DirectoryAbsent(path, err) {
		return nil, nil
	}
	return index, err
}

// readLockSource refuses nonregular inputs before opening them, bounds the read,
// and checks cancellation before and after I/O. The caller confines path to root.
func readLockSource(ctx context.Context, path string) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%s is missing or unreadable: %w", filepath.Base(path), err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, expected a regular file", filepath.Base(path))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	// #nosec G304 -- all callers confine path to the audited repository.
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	data, err = io.ReadAll(io.LimitReader(f, maxArchetypeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxArchetypeBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxArchetypeBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
