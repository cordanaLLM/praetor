package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func lockBuildSource(t *testing.T) (string, *Manifest) {
	t.Helper()
	root, manifest := writeConfigLockFixture(t, lockTestDocument())
	for rel, body := range map[string]string{
		".standards.yaml":                      "version: 1\nprofiles: [framework]\n",
		".config/archetypes/framework.yaml":    lockTestSource,
		".config/archetypes/facets/extra.yaml": "id: test:extra\nname: Extra\n",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, manifest
}

func TestBuildLockfileRealContentAndDeterministicReplay(t *testing.T) {
	root, manifest := lockBuildSource(t)
	manifest.Facets = []string{"test:extra"}
	first, err := BuildLockfile(context.Background(), root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildLockfile(context.Background(), root, manifest)
	if err != nil || string(first) != string(second) {
		t.Fatalf("non-deterministic generation: %v", err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, ".standards.lock"), first, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ValidateLockfile(context.Background(), target, manifest)
	if err != nil || result.Profiles != 1 || result.Facets != 1 {
		t.Fatalf("generated pins invalid: %+v, %v", result, err)
	}
	if !strings.Contains(string(first), lockTestDigest("id: test:extra\nname: Extra\n")) {
		t.Fatal("facet pin does not hash actual source")
	}
	if strings.Contains(string(first), "generated_at") {
		t.Fatalf("deterministic lock must not declare an always-empty generated_at:\n%s", first)
	}
	// yamllint's default rules, which an adopter's lint may apply (BUG-782): a document start,
	// and each digest, 71 columns alone, on its own line below its key at every depth.
	if !strings.HasPrefix(string(first), "---\n") {
		t.Errorf("the lock must open with a document start:\n%s", first)
	}
	for _, line := range strings.Split(string(first), "\n") {
		if len(line) > 80 && strings.Contains(strings.TrimLeft(line, " "), " ") {
			t.Errorf("line past 80 columns: %q", line)
		}
	}
}

func TestBuildLockfileRequiresVerifiableSourceBundle(t *testing.T) {
	root, manifest := lockBuildSource(t)
	if err := os.RemoveAll(filepath.Join(root, ".config")); err != nil {
		t.Fatal(err)
	}
	if data, err := BuildLockfile(context.Background(), root, manifest); !errors.Is(err, ErrLockUnverifiable) || data != nil {
		t.Fatalf("source bundle without a catalog returned pins: %q / %v", data, err)
	}
}

func TestBuildLockfileRefusesUnverifiableSources(t *testing.T) {
	for _, kind := range []string{"missing", "corrupt", "changed", "duplicate", "outside"} {
		t.Run(kind, func(t *testing.T) {
			root, manifest := lockBuildSource(t)
			switch kind {
			case "missing":
				manifest.Facets = []string{"unknown"}
			case "corrupt":
				if err := os.WriteFile(filepath.Join(root, ".standards.lock"), []byte("version: 1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "changed":
				if err := os.WriteFile(filepath.Join(root, ".config/archetypes/framework.yaml"), []byte(lockTestSource+"description: changed\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				manifest.Profiles = []string{"framework", "framework"}
			case "outside":
				manifest.Facets = []string{"outside"}
				if err := os.Symlink(filepath.Join(t.TempDir(), "outside.yaml"), filepath.Join(root, ".config/archetypes/facets/outside.yaml")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if data, err := BuildLockfile(context.Background(), root, manifest); err == nil || data != nil {
				t.Fatalf("unverifiable %s source returned pins: %q / %v", kind, data, err)
			}
		})
	}
}

func TestBuildLockfileBoundsAndCancellation(t *testing.T) {
	root, manifest := lockBuildSource(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildLockfile(ctx, root, manifest); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	manifest.Profiles = make([]string, maxLockEntries+1)
	if data, err := BuildLockfile(context.Background(), root, manifest); err == nil || data != nil {
		t.Fatalf("oversized selection returned pins: %q / %v", data, err)
	}
	for _, tc := range []struct {
		root     string
		manifest *Manifest
	}{
		{"", &Manifest{Version: 1}}, {root, nil}, {root, &Manifest{Version: 2}},
	} {
		if _, err := BuildLockfile(context.Background(), tc.root, tc.manifest); err == nil {
			t.Fatal("missing or invalid inputs accepted")
		}
	}
}

// lockBuildExtraFacet is the unselected facet lockBuildSource writes beside framework.
const lockBuildExtraFacet = "id: test:extra\nname: Extra\n"

// expectedCatalogVersion hashes a catalog's "<kind>:<id>=<digest>" lines independently of the
// builder, as the lock aggregate digest is documented in .standards.lock.
func expectedCatalogVersion(lines ...string) string {
	sorted := append([]string(nil), lines...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n") + "\n"))
	return "v0.0.0+catalog." + hex.EncodeToString(sum[:])[:12]
}

func decodeBuiltLock(t *testing.T, data []byte) *standardsLock {
	t.Helper()
	lock, err := decodeStandardsLock(data)
	if err != nil {
		t.Fatalf("built lock does not decode: %v\n%s", err, data)
	}
	return lock
}

// Positive (#595): the version names the whole source catalog, every entry carries it, and the
// source bundle's own declared pinned_version (v1.0.0 in this fixture) is not copied.
func TestBuildLockfileVersionNamesTheSourceCatalog(t *testing.T) {
	root, manifest := lockBuildSource(t)
	data, err := BuildLockfile(context.Background(), root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	lock := decodeBuiltLock(t, data)
	want := expectedCatalogVersion("profile:framework="+lockTestDigest(lockTestSource),
		"facet:test:extra="+lockTestDigest(lockBuildExtraFacet))
	if lock.PinnedVersion != want || lock.PinnedVersion == "v1.0.0" {
		t.Fatalf("pinned_version = %q, want the catalog version %q", lock.PinnedVersion, want)
	}
	if !lockVersionPattern.MatchString(lock.PinnedVersion) {
		t.Fatalf("catalog version %q does not satisfy the lock validator", lock.PinnedVersion)
	}
	if len(lock.Profiles) != 1 || lock.Profiles[0].Version != want {
		t.Fatalf("entries must carry the catalog version %q: %+v", want, lock.Profiles)
	}
}

// Positive (#595): re-pinning against a catalog with different content writes a different
// version, even when the repository's own selection, and so its aggregate digest, is unchanged.
func TestBuildLockfileVersionMovesWithTheCatalog(t *testing.T) {
	root, manifest := lockBuildSource(t)
	before, err := BuildLockfile(context.Background(), root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	for step, change := range []struct{ rel, body string }{
		{"facets/extra.yaml", lockBuildExtraFacet + "description: changed\n"},
		{"facets/added.yaml", "id: test:added\nname: Added\n"},
	} {
		writeLockTestCatalog(t, root, change.rel, change.body)
		after, err := BuildLockfile(context.Background(), root, manifest)
		if err != nil {
			t.Fatal(err)
		}
		old, next := decodeBuiltLock(t, before), decodeBuiltLock(t, after)
		if old.PinnedVersion == next.PinnedVersion {
			t.Fatalf("step %d: catalog changed but pinned_version stayed %q", step, next.PinnedVersion)
		}
		if old.Digest != next.Digest {
			t.Fatalf("step %d: an unselected catalog change moved the aggregate digest", step)
		}
		before = after
	}
}

// Negative (#595): two adoptions from the same catalog content write the same version and the
// same bytes, wherever the source bundle lives.
func TestBuildLockfileVersionIgnoresTheBundleLocation(t *testing.T) {
	first, manifest := lockBuildSource(t)
	second, _ := lockBuildSource(t)
	a, err := BuildLockfile(context.Background(), first, manifest)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildLockfile(context.Background(), second, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("the same catalog in two places built different locks:\n%s\n%s", a, b)
	}
}

// Boundary (#595): a source bundle whose catalog defines nothing cannot identify itself, so
// lock generation fails instead of writing any version, v1.0.0 included.
func TestBuildLockfileRefusesAnUnidentifiableCatalog(t *testing.T) {
	root, _ := writeConfigLockFixture(t, map[string]any{
		"version": 1, "pinned_version": "v1.0.0", "digest": lockTestDigest("\n"),
		"profiles": []map[string]any{}, "facets": []map[string]any{},
	})
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".config", "archetypes"), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := BuildLockfile(context.Background(), root, &Manifest{Version: 1})
	if !errors.Is(err, errUnidentifiedCatalog) || data != nil {
		t.Fatalf("an empty catalog must not be named: %q / %v", data, err)
	}
	if version, err := catalogLockVersion(nil, map[string]string{}); !errors.Is(err, errUnidentifiedCatalog) || version != "" {
		t.Fatalf("catalogLockVersion named an empty catalog %q: %v", version, err)
	}
}

// UnreleasedLockVersion: positive (identifiers become build metadata), boundary (none is the
// zero version), negative (an empty identifier is outside its contract and the validator
// rejects the result, so a caller cannot slip one into a lock unnoticed).
func TestUnreleasedLockVersion(t *testing.T) {
	for _, tc := range []struct {
		identifiers []string
		want        string
		valid       bool
	}{
		{[]string{"catalog", "0123456789ab"}, "v0.0.0+catalog.0123456789ab", true},
		{[]string{"abc123def456", "dirty"}, "v0.0.0+abc123def456.dirty", true},
		{nil, UnidentifiedLockVersion, true},
		{[]string{""}, "v0.0.0+", false},
	} {
		got := UnreleasedLockVersion(tc.identifiers...)
		if got != tc.want || lockVersionPattern.MatchString(got) != tc.valid {
			t.Errorf("UnreleasedLockVersion(%q) = %q (valid=%v), want %q (valid=%v)",
				tc.identifiers, got, lockVersionPattern.MatchString(got), tc.want, tc.valid)
		}
	}
}
