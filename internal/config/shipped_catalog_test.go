package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shippedCatalogDir is the repository's own archetype catalog, relative to this package.
const shippedCatalogDir = "../../.config/archetypes"

// requiredArchetypeKeys are the fields every shipped profile must carry. A profile missing one
// still loads -- the decoder tolerates absent sections -- and then contributes nothing where a
// reader expected a bound, which is the silent shape this repository keeps finding.
var requiredArchetypeKeys = []string{"id:", "name:", "description:", "runtime:", "complexity:"}

// TestShippedArchetypes_Positive_EveryProfileLoadsAndIsSelfConsistent reads the catalog this
// repository actually ships rather than a synthetic fixture.
//
// Every other test in this package builds its own archetypes in a temporary root, so until now
// nothing asserted that the files under .config/archetypes are well formed at all. A malformed
// or mis-titled profile would have been found by whichever downstream repository selected it
// first, which is the wrong place to find it.
func TestShippedArchetypes_Positive_EveryProfileLoadsAndIsSelfConsistent(t *testing.T) {
	names := shippedProfileNames(t)
	if len(names) == 0 {
		t.Fatal("no archetypes found; the catalog path is wrong")
	}
	for _, name := range names {
		body := readShippedProfile(t, name)
		id := archetypeID(body)
		want := strings.TrimSuffix(name, ".yaml")
		if id != want {
			t.Errorf("%s declares id %q; the id must match the filename, because the loader keys the catalog by id while the lockfile pins by path", name, id)
		}
		for _, key := range requiredArchetypeKeys {
			if !strings.Contains(body, "\n"+key) && !strings.HasPrefix(body, key) {
				t.Errorf("%s omits %q", name, strings.TrimSuffix(key, ":"))
			}
		}
	}
}

// TestShippedArchetypes_Negative_NoDuplicateIDs guards the one collision the loader rejects at
// runtime. Catching it here names the offending pair; catching it there fails every command.
func TestShippedArchetypes_Negative_NoDuplicateIDs(t *testing.T) {
	seen := make(map[string]string)
	for _, name := range shippedProfileNames(t) {
		id := archetypeID(readShippedProfile(t, name))
		if first, dup := seen[id]; dup {
			t.Errorf("%s and %s both declare id %q", first, name, id)
		}
		seen[id] = name
	}
}

// TestShippedArchetypes_Boundary_OSImageCoversBootArtifacts pins the profile added for the OS
// and kernel forges. The bound is the boundary: os-image exists because those repositories
// build bootable artifacts under stricter limits than the interim gitops-infra profile they
// were forced onto, so a silent relaxation back towards the defaults would defeat the point of
// having added it.
func TestShippedArchetypes_Boundary_OSImageCoversBootArtifacts(t *testing.T) {
	body := readShippedProfile(t, "os-image.yaml")
	for _, want := range []string{"max_func_loc: 60", "slsa_level: 3", "enforce_cosign: true", "require_sbom: true"} {
		if !strings.Contains(body, want) {
			t.Errorf("os-image.yaml no longer declares %q", want)
		}
	}
}

// shippedProfileNames lists the profile files, excluding the facets subdirectory.
func shippedProfileNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(shippedCatalogDir)
	if err != nil {
		t.Fatalf("read shipped catalog: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			names = append(names, entry.Name())
		}
	}
	return names
}

// readShippedProfile returns one profile's text.
func readShippedProfile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(shippedCatalogDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// archetypeID extracts the quoted or bare id without pulling in a decoder the loader does not
// use for this purpose.
func archetypeID(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "id:") {
			continue
		}
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "id:")), `"`)
	}
	return ""
}

// TestShippedArchetypes_Boundary_UpstreamForkDeclaresNoBoundsOnPurpose guards a profile whose
// correctness looks like a mistake.
//
// upstream-fork declares zero for every complexity bound and disables linear history and signed
// commits. Each of those reads as an unfinished profile, and the obvious "fix" is to fill in real
// numbers. Doing so would break the profile: a contribution fork must match the upstream it submits
// to, so any gate that rewrites the tree makes every patch unmergeable and every diff unreviewable.
// The looseness is the feature, and this test is where that is written down in executable form.
func TestShippedArchetypes_Boundary_UpstreamForkDeclaresNoBoundsOnPurpose(t *testing.T) {
	body := readShippedProfile(t, "upstream-fork.yaml")
	for _, want := range []string{
		"max_cyclomatic: 0", "max_cognitive: 0", "max_func_loc: 0", "max_statements: 0",
		"enforce_linear_history: false", "require_signed_commits: false",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("upstream-fork no longer declares %q; a contribution fork must not be reformatted to local standards", want)
		}
	}
	// The scanners it does keep are the ones that read without rewriting.
	for _, keep := range []string{"gitleaks", "reuse"} {
		if !strings.Contains(body, keep) {
			t.Errorf("upstream-fork must keep %q: it inspects the tree without altering it", keep)
		}
	}
	// A formatter or complexity linter here would defeat the profile.
	for _, banned := range []string{"prettier", "clang-format", "gofmt", "clippy"} {
		if strings.Contains(body, banned) {
			t.Errorf("upstream-fork must not run %q; it rewrites source a fork has to keep matching upstream", banned)
		}
	}
}

// TestShippedArchetypes_Negative_NoProfileRequiresLegacyESLintConfig pins the lesson from the
// frontend-svelte and typescript-node defect: ESLint v10 states that "the old configuration format
// is no longer supported", so naming .eslintrc.* anywhere requires a file the linter cannot load.
func TestShippedArchetypes_Negative_NoProfileRequiresLegacyESLintConfig(t *testing.T) {
	for _, name := range shippedProfileNames(t) {
		if strings.Contains(declarationsOnly(readShippedProfile(t, name)), ".eslintrc") {
			t.Errorf("%s names a legacy eslintrc file; ESLint v10 cannot load that format", name)
		}
	}
}

// declarationsOnly strips comment lines so a rule that inspects what a profile *declares* is not
// tripped by prose explaining the rule. web-package's own comment names .eslintrc.* precisely to
// record why it must not be required, and an earlier version of this test failed on that comment --
// which is the check being wrong about where to look, not the profile being wrong.
func declarationsOnly(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// TestShippedArchetypes_Positive_NoCodeProfilesClaimNoProvenance checks that profiles governing
// repositories which build nothing do not assert supply-chain guarantees over artifacts that are
// never produced. Claiming SLSA over a directory of Markdown is the same defect class as a flavor
// audit scoring a repository against a stack it does not have.
func TestShippedArchetypes_Positive_NoCodeProfilesClaimNoProvenance(t *testing.T) {
	for _, name := range []string{"org-health.yaml", "upstream-fork.yaml"} {
		body := readShippedProfile(t, name)
		for _, want := range []string{"slsa_level: 0", "enforce_cosign: false", "require_sbom: false"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s declares %q; it builds no artifact to attest", name, strings.TrimSuffix(want, ": false"))
			}
		}
	}
}
