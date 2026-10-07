package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// latticeProfile declares every non-complexity dimension an archetype can contribute.
const latticeProfile = `name: Framework
runtime: go
complexity:
  max_cyclomatic: 12
branch_protection:
  enforce_linear_history: true
  require_signed_commits: true
  required_approving_reviewers: 2
  dismiss_stale_reviews: true
supply_chain:
  slsa_level: 3
  enforce_cosign: true
  require_sbom: true
memory:
  zero_frame_malloc: true
error_unwraps: strict_ban
linters:
  - semgrep
  - govet
devcontainer_features:
  - ghcr.io/devcontainers/features/go:1
  - ghcr.io/devcontainers/features/node:1:
      version: "24"
`

func TestLoadEffectivePolicyJoinsEveryProfileDimension(t *testing.T) {
	facet := "linters: [gitleaks, semgrep]\nmemory:\n  banned_alloc_in_ticks: true\ndevcontainer_features:\n  - ghcr.io/devcontainers/features/go:1\n"
	result, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: policyFixture(t, latticeProfile, facet, "")})
	if err != nil {
		t.Fatal(err)
	}
	policy := result.Policy
	wantBranch := BranchProtectionPolicy{EnforceLinearHistory: true, RequireSignedCommits: true,
		RequiredApprovingReviewers: 2, DismissStaleReviews: true, ReviewMode: BranchReviewModeIndependent}
	if policy.BranchProtection != wantBranch {
		t.Fatalf("profile branch protection did not reach the policy: %+v", policy.BranchProtection)
	}
	if policy.SupplyChain != (SupplyChainPolicy{SLSALevel: 3, EnforceCosign: true, RequireSBOM: true}) {
		t.Fatalf("profile supply chain did not reach the policy: %+v", policy.SupplyChain)
	}
	if policy.Memory != (MemoryPolicy{ZeroFrameMalloc: true, BannedAllocInTicks: true}) || policy.ErrorUnwraps != ErrorUnwrapsStrictBan {
		t.Fatalf("memory or error unwraps did not join: %+v / %q", policy.Memory, policy.ErrorUnwraps)
	}
	if !reflect.DeepEqual(policy.Linters, []string{"govet", "semgrep", "gitleaks"}) {
		t.Fatalf("linters are not the deduplicated union: %v", policy.Linters)
	}
	wantFeatures := []string{"common-utils", "ghcr.io/devcontainers/features/go:1", "ghcr.io/devcontainers/features/node:1"}
	if !reflect.DeepEqual(policy.DevFeatures, wantFeatures) {
		t.Fatalf("devcontainer features are not the deduplicated union: %v", policy.DevFeatures)
	}
	if policy.Complexity.MaxCyclomatic != 12 {
		t.Fatalf("complexity join lost: %+v", policy.Complexity)
	}
	if err := result.VerifyDigest(); err != nil {
		t.Fatalf("joined policy must verify: %v", err)
	}
}

func TestLoadEffectivePolicyRepositoryOverridesApplyAfterTheJoin(t *testing.T) {
	overrides := "overrides:\n  branch_protection:\n    required_approving_reviewers: 1\n    review_mode: single_maintainer\n  supply_chain:\n    slsa_level: 1\n"
	result, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: policyFixture(t, latticeProfile, "", overrides)})
	if err != nil {
		t.Fatal(err)
	}
	branch := result.Policy.BranchProtection
	if branch.RequiredApprovingReviewers != 2 || branch.ReviewMode != BranchReviewModeSingleMaintainer {
		t.Fatalf("override loosened the reviewer minimum or lost the explicit review mode: %+v", branch)
	}
	if result.Policy.SupplyChain.SLSALevel != 3 {
		t.Fatalf("override loosened the profile SLSA level: %+v", result.Policy.SupplyChain)
	}
}

// A profile that releases nothing declares SLSA Build Level 0 and resolves to it: the built-in
// default no longer joins Level 1 over it, which the supply-chain audit would then demand from
// a repository with no release (#330). Boundary: a profile declaring Level 1 keeps it.
func TestLoadEffectivePolicyKeepsADeclaredSLSALevelZero(t *testing.T) {
	for level, profile := range map[int]string{
		0: "name: Health\nsupply_chain:\n  slsa_level: 0\n",
		1: "name: Seed\nsupply_chain:\n  slsa_level: 1\n",
	} {
		result, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: policyFixture(t, profile, "", "")})
		if err != nil {
			t.Fatal(err)
		}
		if got := result.Policy.SupplyChain.SLSALevel; got != level {
			t.Fatalf("profile declaring slsa_level %d resolved to %d", level, got)
		}
	}
	if DefaultPolicy().SupplyChain.SLSALevel != 0 {
		t.Fatalf("default SLSA level = %d; want 0", DefaultPolicy().SupplyChain.SLSALevel)
	}
}

func TestLoadEffectivePolicyLooserFacetNeverLoosens(t *testing.T) {
	loose := strings.Join([]string{
		"complexity: {max_cyclomatic: 0, max_func_loc: 0}",
		"branch_protection: {enforce_linear_history: false, require_signed_commits: false, required_approving_reviewers: 0, dismiss_stale_reviews: false}",
		"supply_chain: {slsa_level: 0, enforce_cosign: false, require_sbom: false}",
		"memory: {zero_frame_malloc: false, banned_alloc_in_ticks: false}",
		"error_unwraps: allow_with_comment",
		"",
	}, "\n")
	strict, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: policyFixture(t, latticeProfile, "", "")})
	if err != nil {
		t.Fatal(err)
	}
	joined, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: policyFixture(t, latticeProfile, loose, "")})
	if err != nil {
		t.Fatalf("a zero complexity bound must mean no bound, not an error: %v", err)
	}
	if !reflect.DeepEqual(strict.Policy, joined.Policy) {
		t.Fatalf("a looser facet changed the policy:\nstrict %+v\njoined %+v", strict.Policy, joined.Policy)
	}
	if !reflect.DeepEqual(joined.Fields["max_cyclomatic"], []string{"profile:framework"}) {
		t.Fatalf("an unbounded limit claimed provenance: %v", joined.Fields)
	}
}

func TestLoadEffectivePolicyRejectsInvalidArchetypes(t *testing.T) {
	for _, body := range []string{
		"complxity: {max_func_loc: 10}\n",
		"templates: [go]\n",
		"branch_protection: {required_reviewers: 2}\n",
		"branch_protection: {review_mode: single_maintainer}\n",
		"branch_protection: {required_approving_reviewers: -1}\n",
		"supply_chain: {slsa: 3}\n",
		"supply_chain: {slsa_level: -1}\n",
		"memory: {zero_malloc: true}\n",
		"memory: [zero_frame_malloc]\n",
		"error_unwraps: maybe\n",
		"error_unwraps: \"\"\n",
		"error_unwraps: [strict_ban]\n",
		"linters: \"semgrep\"\n",
		"linters: [\"\"]\n",
		"linters: [\" semgrep\"]\n",
		"devcontainer_features: ghcr.io/devcontainers/features/go:1\n",
		"complexity: {max_func_loc: -1}\n",
	} {
		root := policyFixture(t, body, "", "")
		if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root}); err == nil {
			t.Errorf("invalid archetype accepted: %q", body)
		}
	}
}

func TestCatalogIndexRejectsUnknownKeyInAnUnselectedArchetype(t *testing.T) {
	root := policyFixture(t, "", "", "")
	writePolicyFile(t, root, ".config/archetypes/other.yaml", "id: other\nlinter: [semgrep]\n")
	_, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if err == nil || !strings.Contains(err.Error(), "linter") {
		t.Fatalf("catalog index accepted a misspelled key: %v", err)
	}
}

// archetypeKeyProbes holds one value of the right type for every key ArchetypeKeys lists.
var archetypeKeyProbes = map[string]string{
	"id": `"probe"`, "name": `"Probe"`, "description": `"probe"`, "runtime": `"go"`,
	"complexity.max_cyclomatic": "8", "complexity.max_cognitive": "8", "complexity.max_func_loc": "40",
	"complexity.max_statements": "30", "memory.zero_frame_malloc": "true", "memory.banned_alloc_in_ticks": "true",
	"error_unwraps": "strict_ban", "branch_protection.enforce_linear_history": "true",
	"branch_protection.require_signed_commits": "true", "branch_protection.required_approving_reviewers": "2",
	"branch_protection.dismiss_stale_reviews": "true", "branch_protection.review_mode": "independent",
	"supply_chain.slsa_level": "2", "supply_chain.enforce_cosign": "true", "supply_chain.require_sbom": "true",
	"linters": "[semgrep]", "devcontainer_features": `["ghcr.io/devcontainers/features/go:1"]`,
	"backlog.caps.defects.max": "80", "backlog.caps.defects.action": "gate",
	"backlog.caps.tasks.max": "40", "backlog.caps.tasks.action": "batch",
	"backlog.caps.questions.max": "10", "backlog.caps.questions.action": "report",
	"backlog.caps.forge_alerts.max": "20", "backlog.caps.forge_alerts.action": "report",
}

// probeDocument spells one dotted key as nested mappings holding value.
func probeDocument(key, value string) string {
	parts := strings.Split(key, ".")
	var document strings.Builder
	for depth, part := range parts {
		document.WriteString(strings.Repeat("  ", depth) + part + ":")
		if depth < len(parts)-1 {
			document.WriteString("\n")
		}
	}
	return document.String() + " " + value + "\n"
}

// ArchetypeKeys lists the closed schema with sections spelled dotted and never bare, and
// every key it lists decodes in a catalog file except review_mode, which validate refuses.
func TestArchetypeKeysListTheClosedSchema(t *testing.T) {
	keys := ArchetypeKeys()
	if len(keys) != len(archetypeKeyProbes) || !slices.IsSorted(keys) {
		t.Fatalf("keys = %v, want the %d probed keys in order", keys, len(archetypeKeyProbes))
	}
	for _, section := range []string{"complexity", "memory", "branch_protection", "supply_chain", "backlog", "backlog.caps"} {
		if slices.Contains(keys, section) {
			t.Errorf("section %s listed bare", section)
		}
	}
	for _, key := range keys {
		value, ok := archetypeKeyProbes[key]
		if !ok {
			t.Errorf("key %s has no probe value", key)
			continue
		}
		_, err := decodeArchetype(t.Context(), "catalog/probe.yaml", []byte(probeDocument(key, value)))
		if refused := key == "branch_protection.review_mode"; (err != nil) != refused {
			t.Errorf("%s: decode error %v, refused %t", key, err, refused)
		}
	}
}

func TestDecodeArchetypeHeaderAndIdentity(t *testing.T) {
	archetype, err := decodeArchetype(t.Context(), "catalog/app.yaml", []byte("id: \" app \"\nname: App\ndescription: Service\nruntime: go\n"))
	if err != nil {
		t.Fatal(err)
	}
	if archetype.ID != "app" || archetype.Name != "App" || archetype.Description != "Service" || archetype.Runtime != "go" {
		t.Fatalf("header not decoded: %+v", archetype)
	}
	stem, err := decodeArchetype(t.Context(), "catalog/stem.yaml", []byte("id: \"  \"\n"))
	if err != nil || stem.ID != "stem" {
		t.Fatalf("a blank id must default to the file stem: %+v / %v", stem, err)
	}
	empty, err := decodeArchetype(t.Context(), "catalog/empty.yaml", []byte("{}\n"))
	if err != nil || !reflect.DeepEqual(empty.Controls, ArchetypeControls{}) || empty.Complexity != (ComplexityOverride{}) {
		t.Fatalf("an empty archetype must contribute nothing: %+v / %v", empty, err)
	}
	if _, err := decodeArchetype(t.Context(), "catalog/two.yaml", []byte("id: a\n---\nid: b\n")); err == nil {
		t.Fatal("multiple documents accepted")
	}
}

// Every file the repository ships must decode through the one archetype reader, facets
// included; upstream-fork's zero bounds decode as no bound at all.
func TestShippedArchetypesDecodeThroughTheArchetypeSchema(t *testing.T) {
	var paths []string
	for _, pattern := range []string{"*.yaml", "facets/*.yaml"} {
		matches, err := filepath.Glob(filepath.Join(shippedCatalogDir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) < 20 {
		t.Fatalf("expected the shipped profiles and facets, found %d files", len(paths))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		archetype, err := decodeArchetype(t.Context(), path, data)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if strings.HasSuffix(path, "upstream-fork.yaml") && archetype.Complexity != (ComplexityOverride{}) {
			t.Errorf("upstream-fork zero bounds must decode as unbounded: %+v", archetype.Complexity)
		}
		if strings.HasSuffix(path, "native-gpu-systems.yaml") && !archetype.Controls.Memory.ZeroFrameMalloc {
			t.Error("native-gpu-systems memory section was not decoded")
		}
	}
}

func TestJoinErrorUnwrapsAndMemoryAreCommutativeSuprema(t *testing.T) {
	allow, strict := ErrorUnwrapsAllowWithComment, ErrorUnwrapsStrictBan
	cases := []struct{ a, b, want ErrorUnwrapMode }{
		{"", "", ""}, {"", allow, allow}, {allow, allow, allow},
		{allow, strict, strict}, {"", strict, strict}, {strict, strict, strict},
		{"bogus", strict, "bogus"},
	}
	for _, tc := range cases {
		for _, pair := range [][2]ErrorUnwrapMode{{tc.a, tc.b}, {tc.b, tc.a}} {
			got := Join(&ResolvedPolicy{ErrorUnwraps: pair[0]}, &ResolvedPolicy{ErrorUnwraps: pair[1]}).ErrorUnwraps
			if got != tc.want {
				t.Errorf("join(%q, %q) = %q, want %q", pair[0], pair[1], got, tc.want)
			}
		}
	}
	zero := MemoryPolicy{ZeroFrameMalloc: true}
	ticks := MemoryPolicy{BannedAllocInTicks: true}
	for _, pair := range [][2]MemoryPolicy{{zero, ticks}, {ticks, zero}} {
		if got := Join(&ResolvedPolicy{Memory: pair[0]}, &ResolvedPolicy{Memory: pair[1]}).Memory; got != (MemoryPolicy{true, true}) {
			t.Fatalf("memory join lost a ban: %+v", got)
		}
	}
}

// Boundary: two archetypes declaring the same strict memory and error-unwrap settings tie,
// and the tie resolves to exactly that setting whichever is pinned first.
func TestResolvePolicyTiedArchetypesOnMemoryAndErrorUnwraps(t *testing.T) {
	controls := ArchetypeControls{Memory: MemoryPolicy{ZeroFrameMalloc: true, BannedAllocInTicks: true}, ErrorUnwraps: ErrorUnwrapsStrictBan}
	first := PolicyLayer{Source: PolicySource{ID: "profile:a", SHA256: policyDigest([]byte("a"))}, Controls: controls}
	second := PolicyLayer{Source: PolicySource{ID: "facet:b", SHA256: policyDigest([]byte("b"))}, Controls: controls}
	forward, err := ResolvePolicy(t.Context(), []PolicyLayer{first, second})
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := ResolvePolicy(t.Context(), []PolicyLayer{second, first})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []*EffectivePolicy{forward, reversed} {
		if result.Policy.Memory != controls.Memory || result.Policy.ErrorUnwraps != ErrorUnwrapsStrictBan {
			t.Fatalf("tie did not resolve to the shared setting: %+v", result.Policy)
		}
	}
	defaults, err := ResolvePolicy(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Policy.ErrorUnwraps != ErrorUnwrapsAllowWithComment || defaults.Policy.Memory != (MemoryPolicy{}) {
		t.Fatalf("defaults must be the bottom of both dimensions: %+v", defaults.Policy)
	}
}

func TestResolvePolicyBoundsTheJoinedNameUnion(t *testing.T) {
	names := func(prefix string, count int) []string {
		result := make([]string, count)
		for i := range result {
			result[i] = fmt.Sprintf("%s-%d", prefix, i)
		}
		return result
	}
	layer := func(id string, linters []string) PolicyLayer {
		return PolicyLayer{Source: PolicySource{ID: id, SHA256: policyDigest([]byte(id))}, Controls: ArchetypeControls{Linters: linters}}
	}
	// The default policy already carries govet, so maxPolicyNames-1 new names fill the bound.
	exact, err := ResolvePolicy(t.Context(), []PolicyLayer{layer("a", names("lint", maxPolicyNames-1))})
	if err != nil || len(exact.Policy.Linters) != maxPolicyNames {
		t.Fatalf("exact name bound rejected: %v", err)
	}
	if err := exact.VerifyDigest(); err != nil {
		t.Fatalf("policy at the name bound must verify: %v", err)
	}
	over := []PolicyLayer{layer("a", names("lint", maxPolicyNames-1)), layer("b", []string{"one-more"})}
	if _, err := ResolvePolicy(t.Context(), over); err == nil {
		t.Fatal("joined linters beyond the bound accepted")
	}
	if _, err := ResolvePolicy(t.Context(), []PolicyLayer{layer("a", names("lint", maxPolicyNames+1))}); err == nil {
		t.Fatal("one layer beyond the bound accepted")
	}
	relaxed := layer("relax", nil)
	relaxed.Controls.BranchProtection.ReviewMode = BranchReviewModeSingleMaintainer
	if _, err := ResolvePolicy(t.Context(), []PolicyLayer{relaxed}); err == nil {
		t.Fatal("a policy layer enabled the repository-only review relaxation")
	}
}

// Retained snapshots from before the memory and error-unwrap dimensions carry neither
// member; the unset dimensions must encode exactly as before so those snapshots re-seal.
func TestResolvedPolicyOmitsUnsetDimensionsFromItsEncoding(t *testing.T) {
	legacy := ResolvedPolicy{Complexity: ComplexityPolicy{MaxFuncLOC: 60}, Linters: []string{"govet"}}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Memory") || strings.Contains(string(encoded), "ErrorUnwraps") {
		t.Fatalf("unset dimensions changed the legacy encoding: %s", encoded)
	}
	result, err := ResolvePolicy(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result.Policy.ErrorUnwraps = "bogus"
	if err := result.seal(); err != nil {
		t.Fatal(err)
	}
	if err := result.VerifyDigest(); err == nil {
		t.Fatal("an unknown error-unwrap mode verified")
	}
}

// The resolver sync uses reads the pinned catalog from an explicit root and reports the
// joined branch protection adopt renders.
func TestResolveRepositoryPolicyFromCatalogReadsTheSelectedCatalog(t *testing.T) {
	root := policyFixture(t, latticeProfile, "", "")
	catalog := t.TempDir()
	if err := os.Rename(filepath.Join(root, ".config"), filepath.Join(catalog, ".config")); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, ManifestFileName)
	// Negative: the repository root no longer carries the catalog.
	if _, _, err := ResolveRepositoryPolicyFromCatalog(t.Context(), manifestPath, "", nil); err == nil {
		t.Fatal("resolved a lock whose catalog is not at the repository root")
	}
	// Positive: the selected catalog resolves the joined policy.
	policy, notice, err := ResolveRepositoryPolicyFromCatalog(t.Context(), manifestPath, catalog, nil)
	if err != nil || notice != "" {
		t.Fatalf("selected catalog: notice %q err=%v", notice, err)
	}
	if policy.BranchProtection.RequiredApprovingReviewers != 2 || policy.ErrorUnwraps != ErrorUnwrapsStrictBan {
		t.Fatalf("joined dimensions missing: %+v", policy)
	}
	// Boundary: without a lock the catalog is irrelevant and defaults plus overrides apply.
	if err := os.Remove(filepath.Join(root, ".standards.lock")); err != nil {
		t.Fatal(err)
	}
	policy, notice, err = ResolveRepositoryPolicyFromCatalog(t.Context(), manifestPath, catalog, nil)
	if err != nil || notice != NoLockNotice || policy.BranchProtection != DefaultPolicy().BranchProtection {
		t.Fatalf("no-lock policy = %+v notice %q err=%v", policy, notice, err)
	}
}

func TestBranchProtectionRejectsUnknownKeysInTheManifest(t *testing.T) {
	base := "version: 1\nrepository: {owner: example, name: demo}\noverrides:\n  branch_protection:\n"
	if _, err := parseManifest("m", []byte(base+"    required_approving_reviewers: 2\n    review_mode: independent\n")); err != nil {
		t.Fatalf("known branch protection keys rejected: %v", err)
	}
	_, err := parseManifest("m", []byte(base+"    required_approving_reviewer: 2\n"))
	if err == nil || !strings.Contains(err.Error(), "required_approving_reviewer") {
		t.Fatalf("misspelled branch protection key accepted: %v", err)
	}
	if !slices.Contains(branchProtectionKeys, "review_mode") || len(branchProtectionKeys) != reflect.TypeFor[BranchProtectionPolicy]().NumField() {
		t.Fatal("branch protection key set drifted from its fields")
	}
}
