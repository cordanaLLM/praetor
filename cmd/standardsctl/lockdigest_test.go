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
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// lockFixture is a temporary repository carrying archetype sources and a lockfile.
type lockFixture struct {
	dir      string
	manifest *config.Manifest
}

// newLockFixture writes one profile archetype and one facet archetype.
func newLockFixture(t *testing.T) *lockFixture {
	t.Helper()
	dir := t.TempDir()
	facetDir := filepath.Join(dir, ".config", "archetypes", "facets")
	if err := os.MkdirAll(facetDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(filepath.Join(dir, ".config", "archetypes", "framework.yaml"), "id: \"framework\"\nname: \"Framework\"\n")
	write(filepath.Join(facetDir, "security-high.yaml"), "id: \"security:high\"\nname: \"High security\"\n")

	return &lockFixture{
		dir: dir,
		manifest: &config.Manifest{
			Version:  1,
			Profiles: []string{"framework"},
			Facets:   []string{"security:high"},
		},
	}
}

func (f *lockFixture) digestOf(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeLock renders a .standards.lock with the given entry digests and a top-level
// digest computed the way auditLockDigests expects, unless topLevel is overridden.
func (f *lockFixture) writeLock(t *testing.T, profileDigest, facetDigest, topLevel string) {
	t.Helper()
	if topLevel == "" {
		lines := []string{
			"profile:framework=sha256:" + profileDigest,
			"facet:security:high=sha256:" + facetDigest,
		}
		sort.Strings(lines)
		sum := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
		topLevel = hex.EncodeToString(sum[:])
	}
	body := fmt.Sprintf(`version: 1
pinned_version: "v1.0.0"
digest: "sha256:%s"
profiles:
  - id: "framework"
    version: "v1.0.0"
    digest: "sha256:%s"
facets:
  - id: "security:high"
    version: "v1.0.0"
    digest: "sha256:%s"
`, topLevel, profileDigest, facetDigest)
	if err := os.WriteFile(filepath.Join(f.dir, ".standards.lock"), []byte(body), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
}

func TestAuditLockDigests_Positive(t *testing.T) {
	f := newLockFixture(t)
	f.writeLock(t,
		f.digestOf(t, ".config/archetypes/framework.yaml"),
		f.digestOf(t, ".config/archetypes/facets/security-high.yaml"),
		"")

	if err := auditLockDigests(f.manifest, f.dir); err != nil {
		t.Fatalf("expected recomputed digests to verify, got: %v", err)
	}
}

func TestAuditLockDigests_Negative(t *testing.T) {
	profile := ".config/archetypes/framework.yaml"
	facet := ".config/archetypes/facets/security-high.yaml"

	// A hand-typed placeholder digest is rejected even before the file is hashed.
	placeholder := newLockFixture(t)
	placeholder.writeLock(t, strings.Repeat("1234567890abcdef", 4), placeholder.digestOf(t, facet), "")
	if err := auditLockDigests(placeholder.manifest, placeholder.dir); !errors.Is(err, ErrLockDigestPlaceholder) {
		t.Errorf("expected ErrLockDigestPlaceholder, got %v", err)
	}

	// The sha256 of empty input is the other classic placeholder.
	empty := newLockFixture(t)
	empty.writeLock(t, emptyInputDigest, empty.digestOf(t, facet), "")
	if err := auditLockDigests(empty.manifest, empty.dir); !errors.Is(err, ErrLockDigestPlaceholder) {
		t.Errorf("expected ErrLockDigestPlaceholder for the empty-input digest, got %v", err)
	}

	// A real but stale digest is caught by recomputation.
	stale := newLockFixture(t)
	stale.writeLock(t, stale.digestOf(t, profile), stale.digestOf(t, facet), "")
	if err := os.WriteFile(filepath.Join(stale.dir, filepath.FromSlash(profile)),
		[]byte("id: \"framework\"\nname: \"Framework (edited)\"\n"), 0o600); err != nil {
		t.Fatalf("edit archetype: %v", err)
	}
	if err := auditLockDigests(stale.manifest, stale.dir); !errors.Is(err, ErrLockDigestMismatch) {
		t.Errorf("expected ErrLockDigestMismatch after editing an archetype, got %v", err)
	}

	// A declared facet the lockfile does not pin at all.
	missing := newLockFixture(t)
	missing.writeLock(t, missing.digestOf(t, profile), missing.digestOf(t, facet), "")
	missing.manifest.Facets = append(missing.manifest.Facets, "perf:hotpath")
	if err := auditLockDigests(missing.manifest, missing.dir); !errors.Is(err, ErrLockEntryMissing) {
		t.Errorf("expected ErrLockEntryMissing, got %v", err)
	}

	// A malformed digest (no algorithm prefix).
	malformed := newLockFixture(t)
	malformed.writeLock(t, "deadbeef", malformed.digestOf(t, facet), "")
	if err := auditLockDigests(malformed.manifest, malformed.dir); !errors.Is(err, ErrLockDigestMalformed) {
		t.Errorf("expected ErrLockDigestMalformed, got %v", err)
	}
}

func TestAuditLockDigests_Boundary(t *testing.T) {
	profile := ".config/archetypes/framework.yaml"
	facet := ".config/archetypes/facets/security-high.yaml"

	// Boundary: entry digests are correct but the top-level digest is not.
	f := newLockFixture(t)
	f.writeLock(t, f.digestOf(t, profile), f.digestOf(t, facet), strings.Repeat("ab", 32))
	err := auditLockDigests(f.manifest, f.dir)
	if err == nil || !strings.Contains(err.Error(), "top-level digest") {
		t.Errorf("expected a top-level digest mismatch, got %v", err)
	}

	// Boundary: a repository consuming remote archetypes has no sources to recompute
	// against, so well-formed non-placeholder digests are accepted.
	remote := newLockFixture(t)
	remote.writeLock(t, remote.digestOf(t, profile), remote.digestOf(t, facet), "")
	if err := os.RemoveAll(filepath.Join(remote.dir, ".config", "archetypes")); err != nil {
		t.Fatalf("remove archetypes: %v", err)
	}
	if err := auditLockDigests(remote.manifest, remote.dir); err != nil {
		t.Errorf("expected a source-less repository to pass shape validation, got %v", err)
	}

	// Boundary: a missing lockfile is an error, not a silent pass.
	if err := os.Remove(filepath.Join(remote.dir, ".standards.lock")); err != nil {
		t.Fatalf("remove lock: %v", err)
	}
	if err := auditLockDigests(remote.manifest, remote.dir); err == nil {
		t.Error("expected an error for a missing lockfile")
	}
}

func TestNormalizeDigest_3D(t *testing.T) {
	// A plausible real digest: 64 hex characters with no repeating 16-character block.
	real := "0123456789abcdef" + "fedcba9876543210" + "00112233445566778899aabbccddeeff"

	// Positive: a well-formed digest is normalised to bare lowercase hex.
	got, err := normalizeDigest("sha256:" + strings.ToUpper(real))
	if err != nil {
		t.Fatalf("normalizeDigest: %v", err)
	}
	if got != real {
		t.Errorf("normalizeDigest = %q, want %q", got, real)
	}

	// Negative: wrong prefix, wrong length, non-hex.
	for _, bad := range []string{real, "md5:" + real, "sha256:" + real[:10], "sha256:" + strings.Repeat("z", 64)} {
		if _, err := normalizeDigest(bad); !errors.Is(err, ErrLockDigestMalformed) {
			t.Errorf("normalizeDigest(%q): expected ErrLockDigestMalformed, got %v", bad, err)
		}
	}

	// Boundary: placeholders of both known shapes.
	for _, placeholder := range []string{
		"sha256:" + emptyInputDigest,
		"sha256:" + strings.Repeat("1234567890abcdef", 4),
		"sha256:" + strings.Repeat("fedcba0987654321", 4),
	} {
		if _, err := normalizeDigest(placeholder); !errors.Is(err, ErrLockDigestPlaceholder) {
			t.Errorf("normalizeDigest(%q): expected ErrLockDigestPlaceholder, got %v", placeholder, err)
		}
	}

	// Boundary: whitespace around a valid digest is tolerated.
	if _, err := normalizeDigest("  sha256:" + real + "  "); err != nil {
		t.Errorf("expected surrounding whitespace to be tolerated, got %v", err)
	}
}

func TestResolvePreMigrationEpic_3D(t *testing.T) {
	// Positive: the working-directory copy is preferred.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".workingdir"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	wdEpic := filepath.Join(dir, ".workingdir", preMigrationEpicFile)
	if err := os.WriteFile(wdEpic, []byte("epic"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	rootEpic := filepath.Join(dir, preMigrationEpicFile)
	if err := os.WriteFile(rootEpic, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := resolvePreMigrationEpic(dir); got != wdEpic {
		t.Errorf("resolvePreMigrationEpic = %q, want %q", got, wdEpic)
	}

	// Negative: nothing anywhere.
	if got := resolvePreMigrationEpic(t.TempDir()); got != "" {
		t.Errorf("expected an empty path when no epic exists, got %q", got)
	}

	// Boundary: only the legacy repository-root location exists.
	legacyOnly := t.TempDir()
	legacyPath := filepath.Join(legacyOnly, preMigrationEpicFile)
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := resolvePreMigrationEpic(legacyOnly); got != legacyPath {
		t.Errorf("resolvePreMigrationEpic = %q, want %q", got, legacyPath)
	}
}

func TestAuditPreMigrationTracking_Negative_IncompleteEpic(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".workingdir"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// An epic that stops at task 3 must fail rather than silently pass.
	body := "[TASK 1/5] a\n[TASK 2/5] b\n[TASK 3/5] c\n"
	if err := os.WriteFile(filepath.Join(dir, ".workingdir", preMigrationEpicFile), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := auditPreMigrationTracking(dir)
	if err == nil || !strings.Contains(err.Error(), "[TASK 4/5]") {
		t.Errorf("expected a missing-stage failure, got %v", err)
	}
}
