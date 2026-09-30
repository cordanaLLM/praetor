package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The .standards.lock header and docs/guides/archetype-authoring.md tell a maintainer to re-pin
// the lock with the digests audit reports. Audit resolves the effective policy before its lock
// gate, so effective policy has to report a moved pin as completely as lock validation does.

const mismatchFacetBody = "id: security:high\ncomplexity:\n  max_cognitive: 12\n"

// assertMismatchReport fails unless both the effective policy and lock validation reject root
// with ErrLockDigestMismatch and the same report naming the kind, id, pinned digest, source path
// and the digest the source hashes to.
func assertMismatchReport(t *testing.T, root, kind, id, pinned, path, actual string) {
	t.Helper()
	want := entryDigestMismatch(kind, id, pinned, path, actual).Error()
	for _, word := range []string{kind, id, digestPrefix + pinned, path, digestPrefix + actual} {
		if !strings.Contains(want, word) {
			t.Fatalf("the report omits %q: %s", word, want)
		}
	}
	_, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if !errors.Is(err, ErrLockDigestMismatch) || !strings.Contains(err.Error(), want) {
		t.Fatalf("effective policy report:\n got %v\nwant %s", err, want)
	}
	manifest, err := LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ValidateLockfileWithOptions(t.Context(), LockValidationOptions{Root: root, RequireSources: true}, manifest)
	if !errors.Is(err, ErrLockDigestMismatch) || !strings.Contains(err.Error(), want) {
		t.Fatalf("lock validation report:\n got %v\nwant %s", err, want)
	}
}

func TestLockMismatchReport_Positive_UnchangedCatalogResolves(t *testing.T) {
	root := policyFixture(t, "", "complexity:\n  max_cognitive: 12\n", "")
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root}); err != nil {
		t.Fatalf("pinned catalog rejected: %v", err)
	}
	// Rewriting a pinned file with its own bytes keeps its digest.
	writePolicyFile(t, root, ".config/archetypes/facets/security-high.yaml", mismatchFacetBody)
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root}); err != nil {
		t.Fatalf("identical rewrite rejected: %v", err)
	}
}

func TestLockMismatchReport_Negative_EditedFacetNamesBothDigests(t *testing.T) {
	root := policyFixture(t, "", "complexity:\n  max_cognitive: 12\n", "")
	edited := "id: security:high\ncomplexity:\n  max_cognitive: 11\n"
	path := writePolicyFile(t, root, ".config/archetypes/facets/security-high.yaml", edited)
	assertMismatchReport(t, root, "facet", "security:high",
		policyDigest([]byte(mismatchFacetBody)), path, policyDigest([]byte(edited)))
}

func TestLockMismatchReport_Negative_EditedProfileNamesBothDigests(t *testing.T) {
	root := policyFixture(t, "complexity:\n  max_func_loc: 75\n", "", "")
	edited := "id: framework\ncomplexity:\n  max_func_loc: 60\n"
	path := writePolicyFile(t, root, ".config/archetypes/framework.yaml", edited)
	assertMismatchReport(t, root, "profile", "framework",
		policyDigest([]byte("id: framework\ncomplexity:\n  max_func_loc: 75\n")), path, policyDigest([]byte(edited)))
}

// A one-byte edit that leaves every value in place still moves the digest, and the report
// names it.
func TestLockMismatchReport_Boundary_OneByteEditIsReported(t *testing.T) {
	root := policyFixture(t, "", "complexity:\n  max_cognitive: 12\n", "")
	edited := strings.TrimSuffix(mismatchFacetBody, "\n")
	path := writePolicyFile(t, root, ".config/archetypes/facets/security-high.yaml", edited)
	assertMismatchReport(t, root, "facet", "security:high",
		policyDigest([]byte(mismatchFacetBody)), path, policyDigest([]byte(edited)))
}
