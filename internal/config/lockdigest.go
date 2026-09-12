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

	"gopkg.in/yaml.v3"

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
	GeneratedAt   string      `yaml:"generated_at"`
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

// indexArchetypes maps the declared id of every archetype YAML in dir to its path.
func indexArchetypes(ctx context.Context, root, dir string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read archetype directory %s: %w", dir, err)
	}
	if len(entries) > maxLockEntries {
		return nil, fmt.Errorf("archetype directory %s exceeds %d entries", dir, maxLockEntries)
	}
	index := make(map[string]string, len(entries))
	for i := 0; i < len(entries) && i < maxLockEntries; i++ {
		name := entries[i].Name()
		if entries[i].IsDir() || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		path, err := confinedArchetypePath(root, dir, name)
		if err != nil {
			return nil, err
		}
		id, idErr := archetypeID(ctx, path)
		if idErr != nil {
			return nil, idErr
		}
		if _, duplicate := index[id]; duplicate {
			return nil, fmt.Errorf("duplicate archetype ID %q in %s", id, dir)
		}
		index[id] = path
	}
	return index, nil
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

// archetypeID reads the declared id of an archetype file, defaulting to its base name.
func archetypeID(ctx context.Context, path string) (string, error) {
	data, err := readLockSource(ctx, path)
	if err != nil {
		return "", fmt.Errorf("read archetype %s: %w", path, err)
	}
	var doc struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
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

// verifyLockEntries checks every declared id against the lockfile and, when the archetype
// sources are present in the repository, against the recomputed content digest.
func verifyLockEntries(ctx context.Context, declared []string, entries []lockEntry, sources map[string]string, kind string) error {
	pinned := make(map[string]lockEntry, len(entries))
	for i := 0; i < len(entries) && i < maxLockEntries; i++ {
		pinned[entries[i].ID] = entries[i]
	}

	for i := 0; i < len(declared) && i < maxLockEntries; i++ {
		id := declared[i]
		entry, ok := pinned[id]
		if !ok {
			return fmt.Errorf("%w: %s %q", ErrLockEntryMissing, kind, id)
		}
		digest, err := normalizeDigest(entry.Digest)
		if err != nil {
			return fmt.Errorf("%s %q: %w", kind, id, err)
		}
		sourcePath, hasSource := sources[id]
		if !hasSource {
			continue
		}
		actual, err := fileDigest(ctx, sourcePath)
		if err != nil {
			return err
		}
		if actual != digest {
			return fmt.Errorf("%w: %s %q pins %s%s but %s hashes to %s%s",
				ErrLockDigestMismatch, kind, id, digestPrefix, digest, sourcePath, digestPrefix, actual)
		}
	}
	return nil
}

// archetypeSources indexes the repository's archetype and facet definitions when present.
// A repository consuming remote archetypes simply has no sources to recompute against.
func archetypeSources(ctx context.Context, rootDir string) (profiles, facets map[string]string, err error) {
	profiles, err = optionalArchetypeIndex(ctx, rootDir, archetypeDirName)
	if err != nil {
		return nil, nil, err
	}
	facets, err = optionalArchetypeIndex(ctx, rootDir, filepath.Join(archetypeDirName, facetDirName))
	return profiles, facets, err
}

func optionalArchetypeIndex(ctx context.Context, root, rel string) (map[string]string, error) {
	path, err := util.ConfinePath(root, rel)
	if err != nil {
		return nil, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect archetype directory %s: %w", path, err)
	}
	return indexArchetypes(ctx, root, path)
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
