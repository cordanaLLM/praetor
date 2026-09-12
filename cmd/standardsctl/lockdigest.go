package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/config"
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
func loadStandardsLock(path string) (*standardsLock, error) {
	// #nosec G304 -- path is the repository's own lockfile, derived from the manifest path.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var lock standardsLock
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &lock, nil
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
func fileDigest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() > maxArchetypeBytes {
		return "", fmt.Errorf("archetype %s exceeds %d bytes", path, maxArchetypeBytes)
	}
	// #nosec G304 -- path is built from the repository's own .config/archetypes tree.
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// indexArchetypes maps the declared id of every archetype YAML in dir to its path.
func indexArchetypes(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read archetype directory %s: %w", dir, err)
	}
	index := make(map[string]string, len(entries))
	for i := 0; i < len(entries) && i < maxLockEntries; i++ {
		name := entries[i].Name()
		if entries[i].IsDir() || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		path := filepath.Join(dir, name)
		id, idErr := archetypeID(path)
		if idErr != nil {
			return nil, idErr
		}
		index[id] = path
	}
	return index, nil
}

// archetypeID reads the declared id of an archetype file, defaulting to its base name.
func archetypeID(path string) (string, error) {
	// #nosec G304 -- path comes from a directory listing of the repository's own tree.
	data, err := os.ReadFile(path)
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
func verifyLockEntries(declared []string, entries []lockEntry, sources map[string]string, kind string) error {
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
		actual, err := fileDigest(sourcePath)
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

// auditLockDigests validates .standards.lock: every declared profile and facet is pinned,
// no digest is a placeholder, and each digest matches the archetype source whenever the
// repository carries one.
func auditLockDigests(manifest *config.Manifest, rootDir string) error {
	lockPath := filepath.Join(rootDir, ".standards.lock")
	lock, err := loadStandardsLock(lockPath)
	if err != nil {
		return fmt.Errorf("[FAIL] %w", err)
	}

	profileSources, facetSources := archetypeSources(rootDir)
	if err := verifyLockEntries(manifest.Profiles, lock.Profiles, profileSources, "profile"); err != nil {
		return fmt.Errorf("[FAIL] %s: %w", lockPath, err)
	}
	if err := verifyLockEntries(manifest.Facets, lock.Facets, facetSources, "facet"); err != nil {
		return fmt.Errorf("[FAIL] %s: %w", lockPath, err)
	}

	topLevel, err := normalizeDigest(lock.Digest)
	if err != nil {
		return fmt.Errorf("[FAIL] %s top-level digest: %w", lockPath, err)
	}
	if expected := canonicalLockDigest(lock); topLevel != expected {
		return fmt.Errorf("[FAIL] %s top-level digest is %s%s but the pinned entries hash to %s%s",
			lockPath, digestPrefix, topLevel, digestPrefix, expected)
	}

	fmt.Printf("[PASS] Lockfile digests verified (%d profiles, %d facets).\n", len(lock.Profiles), len(lock.Facets))
	return nil
}

// archetypeSources indexes the repository's archetype and facet definitions when present.
// A repository consuming remote archetypes simply has no sources to recompute against.
func archetypeSources(rootDir string) (profiles, facets map[string]string) {
	profiles = map[string]string{}
	facets = map[string]string{}

	archetypeDir := filepath.Join(rootDir, filepath.FromSlash(archetypeDirName))
	if !util.DirExists(archetypeDir) {
		return profiles, facets
	}
	if idx, err := indexArchetypes(archetypeDir); err == nil {
		profiles = idx
	}
	facetDir := filepath.Join(archetypeDir, facetDirName)
	if !util.DirExists(facetDir) {
		return profiles, facets
	}
	if idx, err := indexArchetypes(facetDir); err == nil {
		facets = idx
	}
	return profiles, facets
}
