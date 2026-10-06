package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// UnidentifiedLockVersion is the pinned_version of something that can prove no identity:
	// the zero version, deliberately never a plausible release number.
	UnidentifiedLockVersion = "v0.0.0"
	// catalogIdentityTag is the build-metadata identifier that precedes a catalog's digest.
	catalogIdentityTag = "catalog"
	// catalogIdentityLen is how many hex digits of the catalog digest the version carries, the
	// length an unreleased build's revision is shortened to as well.
	catalogIdentityLen = 12
)

// errUnidentifiedCatalog refuses a source catalog that holds nothing to hash.
var errUnidentifiedCatalog = errors.New("lock source catalog defines no archetypes, so no version can identify it")

// UnreleasedLockVersion is the pinned_version of anything without a release version: the zero
// version with identifiers as SemVer build metadata, where a dot separates identifiers and a
// second plus sign would be invalid. Each identifier must be non-empty [0-9A-Za-z-]; no
// identifiers yield UnidentifiedLockVersion. init (a build revision) and BuildLockfile (a
// catalog digest) both render their versions here, so the two shapes cannot drift apart.
func UnreleasedLockVersion(identifiers ...string) string {
	if len(identifiers) == 0 {
		return UnidentifiedLockVersion
	}
	return UnidentifiedLockVersion + "+" + strings.Join(identifiers, ".")
}

// BuildLockfile pins target profiles and facets to actual files in an explicitly selected
// Praetor source bundle. The bundle's own lock must validate, but its pinned_version is not
// copied: that is whatever the bundle last declared, and Praetor's declared v1.0.0 through
// every catalog change (#595). The version names the catalog content instead
// (catalogLockVersion), so locks pinned to different catalogs state different versions and
// locks built from the same catalog state the same one. The deterministic result does not
// modify either root.
func BuildLockfile(ctx context.Context, sourceRoot string, target *Manifest) ([]byte, error) {
	if err := validateLockBuildInputs(ctx, sourceRoot, target); err != nil {
		return nil, err
	}
	source, err := readLockBuildSources(ctx, sourceRoot)
	if err != nil {
		return nil, err
	}
	lock := &standardsLock{Version: 1, PinnedVersion: source.version}
	lock.Profiles, err = buildLockEntries(target.Profiles, source.profiles, source.version, "profile")
	if err != nil {
		return nil, missingFromSource(sourceRoot, source.version, err)
	}
	lock.Facets, err = buildLockEntries(target.Facets, source.facets, source.version, "facet")
	if err != nil {
		return nil, missingFromSource(sourceRoot, source.version, err)
	}
	if err := validateLockMetadata(lock, target); err != nil {
		return nil, err
	}
	lock.Digest = digestPrefix + canonicalLockDigest(lock)
	// The lock lands in the adopter's tree beside the YAML its own lint may cover, so it is one
	// lint-clean document too (BUG-782).
	return util.EncodeYAMLDocument(lock)
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

// lockBuildSources is a validated source bundle: its catalog version and, per kind, every
// catalog id with its sha256:<hex> content digest.
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
	if err := validateSourcePins(ctx, filepath.Dir(path), source, path); err != nil {
		return nil, err
	}
	profiles, facets, err := hashLockCatalog(ctx, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	version, err := catalogLockVersion(profiles, facets)
	if err != nil {
		return nil, err
	}
	return &lockBuildSources{version: version, profiles: profiles, facets: facets}, nil
}

// validateSourcePins requires the source bundle's own lock to validate against its catalog,
// unchanged while it is checked, before any pin is built from that catalog.
func validateSourcePins(ctx context.Context, root string, manifest *Manifest, path string) error {
	before, err := readLockSource(ctx, path)
	if err != nil {
		return err
	}
	if _, err := ValidateLockfileWithOptions(ctx, LockValidationOptions{Root: root, RequireSources: true}, manifest); err != nil {
		return fmt.Errorf("validate lock source: %w", err)
	}
	after, err := readLockSource(ctx, path)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) {
		return errors.New("lock source changed during validation")
	}
	return nil
}

// hashLockCatalog hashes every archetype and facet the catalog under root defines, each file
// once, so the catalog version and the pins written beside it come from the same bytes. A
// root without a catalog is ErrLockUnverifiable, as lock validation reports it.
func hashLockCatalog(ctx context.Context, root string) (profiles, facets map[string]string, err error) {
	profilePaths, facetPaths, err := archetypeSources(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	if profilePaths == nil {
		return nil, nil, fmt.Errorf("lock source: %w", ErrLockUnverifiable)
	}
	if profiles, err = catalogDigests(ctx, profilePaths); err != nil {
		return nil, nil, err
	}
	if facets, err = catalogDigests(ctx, facetPaths); err != nil {
		return nil, nil, err
	}
	return profiles, facets, nil
}

// catalogDigests maps each id of sources, an archetype index, to its file's sha256:<hex>, the
// line-ending neutral digest verification compares (fileDigest).
func catalogDigests(ctx context.Context, sources map[string]string) (map[string]string, error) {
	if len(sources) > maxLockEntries {
		return nil, fmt.Errorf("lock source catalog exceeds %d entries per kind", maxLockEntries)
	}
	ids := slices.Sorted(maps.Keys(sources))
	digests := make(map[string]string, len(ids))
	for i := 0; i < len(ids) && i < maxLockEntries; i++ {
		digest, _, err := fileDigest(ctx, sources[ids[i]])
		if err != nil {
			return nil, err
		}
		digests[ids[i]] = digestPrefix + digest
	}
	return digests, nil
}

// catalogLockVersion names a catalog by its content, v0.0.0+catalog.<hex>: the first
// catalogIdentityLen hex digits of the lock aggregate digest (canonicalLockDigest) taken over
// every entry the catalog defines, not over one repository's selection. A changed, added or
// removed archetype or facet moves it; where the bundle lives never does. A catalog with
// nothing to hash is refused rather than named.
func catalogLockVersion(profiles, facets map[string]string) (string, error) {
	if len(profiles)+len(facets) == 0 {
		return "", errUnidentifiedCatalog
	}
	catalog := &standardsLock{}
	var err error
	if catalog.Profiles, err = buildLockEntries(slices.Sorted(maps.Keys(profiles)), profiles, "", "profile"); err != nil {
		return "", err
	}
	if catalog.Facets, err = buildLockEntries(slices.Sorted(maps.Keys(facets)), facets, "", "facet"); err != nil {
		return "", err
	}
	return UnreleasedLockVersion(catalogIdentityTag, canonicalLockDigest(catalog)[:catalogIdentityLen]), nil
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
	// The policy loader's decoder: a lock is never built from a manifest, or from the first
	// document of one, that LoadManifest refuses (BUG-857).
	manifest, err := DecodeManifest(data)
	if err != nil {
		return nil, fmt.Errorf("parse source manifest: %w", err)
	}
	if manifest.Version != 1 {
		return nil, errors.New("source manifest requires version 1")
	}
	return manifest, nil
}

// missingFromSource names the source bundle and its catalog version in a declared id the bundle
// does not define (ErrLockSourceMissing). An archetype newer than the bundle reads as absent
// from it, so the message says which bundle was read and how old its catalog is (#123).
func missingFromSource(sourceRoot, version string, err error) error {
	if !errors.Is(err, ErrLockSourceMissing) {
		return err
	}
	return fmt.Errorf("lock source %s, catalog %s: %w; select a Praetor source bundle whose catalog defines it",
		sourceRoot, version, err)
}

// buildLockEntries pins each id to its digest in digests, a catalogDigests result, at version.
// An id digests does not hold is ErrLockSourceMissing, as lock validation reports it.
func buildLockEntries(ids []string, digests map[string]string, version, kind string) ([]lockEntry, error) {
	entries := make([]lockEntry, 0, len(ids))
	for i := 0; i < len(ids) && i < maxLockEntries; i++ {
		digest, ok := digests[ids[i]]
		if !ok {
			return nil, fmt.Errorf("%s %q: %w", kind, ids[i], ErrLockSourceMissing)
		}
		entries = append(entries, lockEntry{ID: ids[i], Version: version, Digest: digest})
	}
	return entries, nil
}
