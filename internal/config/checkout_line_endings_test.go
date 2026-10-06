package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Windows checkout writes every text file under "* text=auto" with core.eol=native, CRLF, so
// an unmodified adopter read the archetype sources, .standards.yaml and .standards.lock as CRLF
// bytes, failed the pinned digests and reported another effective-policy digest than an LF
// clone (#781). These tests write the CRLF bytes directly, so they run alike on every platform.

// checkoutPolicyInputs are the policy fixture's files a CRLF checkout converts.
var checkoutPolicyInputs = []string{
	".standards.yaml", ".standards.lock",
	".config/archetypes/framework.yaml", ".config/archetypes/facets/security-high.yaml",
}

// convertToCRLF rewrites the LF file root/rel the way a core.eol=crlf checkout writes it.
func convertToCRLF(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\r") {
		t.Fatalf("fixture %s is not LF", rel)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "\n", "\r\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// crlfPolicyFixture is policyFixture with every policy input converted to CRLF, and the
// effective policy its LF form resolves to.
func crlfPolicyFixture(t *testing.T) (root string, lf *EffectivePolicy) {
	t.Helper()
	root = policyFixture(t, "complexity:\n  max_func_loc: 75\n", "complexity:\n  max_cognitive: 12\n", "")
	lf, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if err != nil {
		t.Fatalf("LF fixture: %v", err)
	}
	for _, rel := range checkoutPolicyInputs {
		convertToCRLF(t, root, rel)
	}
	return root, lf
}

func validateFixtureLock(t *testing.T, root string) error {
	t.Helper()
	manifest, err := LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ValidateLockfileWithOptions(t.Context(), LockValidationOptions{Root: root, RequireSources: true}, manifest)
	return err
}

// Positive: a uniformly CRLF checkout resolves the same effective policy as its LF clone, with
// the same digest and the same source lines, and its lock validates against the catalog.
func TestCheckoutLineEndings_Positive_CRLFCheckoutMatchesLFClone(t *testing.T) {
	root, lf := crlfPolicyFixture(t)
	crlf, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if err != nil {
		t.Fatalf("a CRLF checkout of unmodified sources must resolve: %v", err)
	}
	if crlf.SHA256 != lf.SHA256 || crlf.Evidence() != lf.Evidence() {
		t.Fatalf("CRLF checkout reports another policy:\n%s\nLF clone:\n%s", crlf.Evidence(), lf.Evidence())
	}
	if err := validateFixtureLock(t, root); err != nil {
		t.Fatalf("lock validation of a CRLF checkout: %v", err)
	}
}

// Positive: a lock built from a CRLF catalog checkout pins the digests an LF clone builds, so a
// lock written on Windows verifies on Linux.
func TestCheckoutLineEndings_Positive_LockBuiltFromCRLFCatalogMatchesLF(t *testing.T) {
	root, manifest := lockBuildSource(t)
	lf, err := BuildLockfile(t.Context(), root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	convertToCRLF(t, root, ".config/archetypes/framework.yaml")
	convertToCRLF(t, root, ".config/archetypes/facets/extra.yaml")
	crlf, err := BuildLockfile(t.Context(), root, manifest)
	if err != nil || string(crlf) != string(lf) {
		t.Fatalf("lock built from a CRLF catalog differs (%v):\n%s\nLF:\n%s", err, crlf, lf)
	}
}

// Negative: a real content change in a CRLF checkout still fails both checks with the pinned
// and the actual digest, and no byte-for-byte note, since its line endings were folded.
func TestCheckoutLineEndings_Negative_CRLFContentEditStillFails(t *testing.T) {
	root, _ := crlfPolicyFixture(t)
	edited := "id: framework\r\ncomplexity:\r\n  max_func_loc: 60\r\n"
	writePolicyFile(t, root, ".config/archetypes/framework.yaml", edited)
	_, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if !errors.Is(err, ErrLockDigestMismatch) || strings.Contains(err.Error(), "byte for byte") {
		t.Fatalf("effective policy of an edited CRLF profile: %v", err)
	}
	if err := validateFixtureLock(t, root); !errors.Is(err, ErrLockDigestMismatch) {
		t.Fatalf("lock validation of an edited CRLF profile: %v", err)
	}
}

// Boundary: a profile with mixed line endings is hashed byte for byte, so it fails against its
// LF pin and both reports say why. A mixed .standards.yaml keeps the digest of its exact bytes.
func TestCheckoutLineEndings_Boundary_MixedEndingsStayByteExact(t *testing.T) {
	root, lf := crlfPolicyFixture(t)
	writePolicyFile(t, root, ".config/archetypes/framework.yaml", "id: framework\r\ncomplexity:\n  max_func_loc: 75\n")
	_, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	for name, err := range map[string]error{"effective policy": err, "lock validation": validateFixtureLock(t, root)} {
		if !errors.Is(err, ErrLockDigestMismatch) || !strings.Contains(err.Error(), "compared byte for byte") ||
			!strings.Contains(err.Error(), "mixed") {
			t.Errorf("%s of a mixed-ending profile: %v", name, err)
		}
	}
	writePolicyFile(t, root, ".config/archetypes/framework.yaml", "id: framework\r\ncomplexity:\r\n  max_func_loc: 75\r\n")
	mixed := "version: 1\r\nrepository:\n  owner: example\n  name: demo\nprofiles: [framework]\nfacets: [security:high]\n"
	writePolicyFile(t, root, ".standards.yaml", mixed)
	policy, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if source := policySource(t, policy, "repository"); source.SHA256 != policyDigest([]byte(mixed)) ||
		source.SHA256 == policySource(t, lf, "repository").SHA256 {
		t.Fatalf("a mixed .standards.yaml must keep the digest of its bytes, got %s", source.SHA256)
	}
}

func policySource(t *testing.T, policy *EffectivePolicy, id string) PolicySource {
	t.Helper()
	for _, source := range policy.Sources {
		if source.ID == id {
			return source
		}
	}
	t.Fatalf("no %s source in %+v", id, policy.Sources)
	return PolicySource{}
}
