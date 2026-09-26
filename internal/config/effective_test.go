package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"gopkg.in/yaml.v3"
)

func policyLimit(value int) *int { return &value }

func policyTestLayer(id string, loc int) PolicyLayer {
	return PolicyLayer{Source: PolicySource{ID: id, SHA256: policyDigest([]byte(id))},
		Complexity: ComplexityOverride{MaxFuncLOC: policyLimit(loc)}}
}

func TestResolvePolicyStrictnessPresenceAndOwnership(t *testing.T) {
	layers := []PolicyLayer{policyTestLayer("fleet", 50), policyTestLayer("repository", 75), policyTestLayer("deployment", 50)}
	result, err := ResolvePolicy(t.Context(), layers)
	if err != nil {
		t.Fatal(err)
	}
	if result.Policy.Complexity.MaxFuncLOC != 50 || result.Policy.Complexity.MaxCognitive != 20 {
		t.Fatalf("unexpected limits: %+v", result.Policy.Complexity)
	}
	if !reflect.DeepEqual(result.Fields["max_func_loc"], []string{"fleet", "deployment"}) {
		t.Fatalf("missing tied contributors: %+v", result.Fields)
	}
	*layers[0].Complexity.MaxFuncLOC = 5
	layers[0].Source.ID = "changed"
	if result.Policy.Complexity.MaxFuncLOC != 50 || result.Sources[1].ID != "fleet" {
		t.Fatal("result aliases mutable input")
	}
	if !strings.Contains(result.Evidence(), "max_func_loc=50") || !strings.Contains(result.Evidence(), result.SHA256) {
		t.Fatalf("missing shared evidence: %s", result.Evidence())
	}
	var absent *EffectivePolicy
	if absent.Evidence() != "effective complexity policy unavailable" {
		t.Fatal("nil evidence must be explicit")
	}
}

func TestResolvePolicyNegativeAndBoundary(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ResolvePolicy(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	var noContext context.Context
	if _, err := ResolvePolicy(noContext, nil); err == nil {
		t.Fatal("nil context accepted")
	}
	for _, limit := range []int{0, -1} {
		if _, err := ResolvePolicy(t.Context(), []PolicyLayer{policyTestLayer("bad", limit)}); err == nil {
			t.Fatalf("explicit limit %d accepted", limit)
		}
	}
	for _, layers := range [][]PolicyLayer{
		{policyTestLayer("duplicate", 20), policyTestLayer("duplicate", 10)},
		{{Source: PolicySource{ID: "no-digest"}}},
		{policyTestLayer("line\nbreak", 10)},
	} {
		if _, err := ResolvePolicy(t.Context(), layers); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
	layers := make([]PolicyLayer, maxPolicyLayers)
	for i := range layers {
		layers[i] = policyTestLayer(fmt.Sprintf("layer:%d", i), 1)
	}
	if _, err := ResolvePolicy(t.Context(), layers); err != nil {
		t.Fatalf("exact layer bound: %v", err)
	}
	layers = append(layers, policyTestLayer("over", 1))
	if _, err := ResolvePolicy(t.Context(), layers); err == nil {
		t.Fatal("excess layers accepted")
	}
}

func TestResolvePolicyDigestStableAcrossMountsAndSensitiveToInputs(t *testing.T) {
	layer := policyTestLayer("deployment", 55)
	layer.Source.Path = "/first/mount/config.yaml"
	one, err := ResolvePolicy(t.Context(), []PolicyLayer{layer})
	if err != nil {
		t.Fatal(err)
	}
	layer.Source.Path = "/other/mount/config.yaml"
	two, err := ResolvePolicy(t.Context(), []PolicyLayer{layer})
	if err != nil || one.SHA256 != two.SHA256 {
		t.Fatalf("mount changed identity: %v", err)
	}
	layer.Source.SHA256 = policyDigest([]byte("same limit, different input"))
	three, err := ResolvePolicy(t.Context(), []PolicyLayer{layer})
	if err != nil || three.SHA256 == one.SHA256 {
		t.Fatalf("source change must change evidence: %v", err)
	}
}

func writePolicyFile(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func policyFixture(t *testing.T, profile, facet, overrides string) string {
	t.Helper()
	root := t.TempDir()
	manifest := "version: 1\nrepository:\n  owner: example\n  name: demo\nprofiles: [framework]\n"
	lock := &standardsLock{Version: 1, PinnedVersion: "v1.0.0"}
	profile = "id: framework\n" + profile
	writePolicyFile(t, root, ".config/archetypes/framework.yaml", profile)
	lock.Profiles = []lockEntry{{ID: "framework", Version: "v1.0.0", Digest: digestPrefix + policyDigest([]byte(profile))}}
	if facet != "" {
		manifest += "facets: [security:high]\n"
		facet = "id: security:high\n" + facet
		writePolicyFile(t, root, ".config/archetypes/facets/security-high.yaml", facet)
		lock.Facets = []lockEntry{{ID: "security:high", Version: "v1.0.0", Digest: digestPrefix + policyDigest([]byte(facet))}}
	}
	writePolicyFile(t, root, ".standards.yaml", manifest+overrides)
	lock.Digest = digestPrefix + canonicalLockDigest(lock)
	data, err := yaml.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	writePolicyFile(t, root, ".standards.lock", string(data))
	return root
}

func TestLoadEffectivePolicyPinnedLayersAndExternalScopes(t *testing.T) {
	root := policyFixture(t, "complexity:\n  max_func_loc: 80\n", "complexity:\n  max_cognitive: 12\n",
		"overrides:\n  complexity:\n    max_func_loc: 75\n  branch_protection:\n    require_signed_commits: true\n  ci:\n    diff_aware_filtering: true\nreceipt:\n  public_key: example\nneeds:\n  required: []\n")
	external := t.TempDir()
	opts := EffectiveOptions{Root: root, Audit: true,
		FleetPath:        writePolicyFile(t, external, "fleet.yaml", "complexity:\n  max_func_loc: 70\nrunners: {}\n"),
		OrganizationPath: writePolicyFile(t, external, "org.yaml", "complexity:\n  max_func_loc: 65\n"),
		DeploymentPath:   writePolicyFile(t, external, "deployment.yaml", "complexity:\n  max_func_loc: 50\n"),
		WorkstationPath:  writePolicyFile(t, external, "workstation.yaml", "complexity:\n  max_func_loc: 90\n")}
	result, err := LoadEffectivePolicyContext(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Policy.Complexity.MaxFuncLOC != 50 || result.Policy.Complexity.MaxCognitive != 12 {
		t.Fatalf("layers not applied: %+v", result.Policy)
	}
	if !result.Policy.BranchProtection.RequireSignedCommits || result.Manifest.Repository.Name != "demo" {
		t.Fatal("legacy manifest behavior lost")
	}
	want := []string{"builtin:defaults-v1", "lock", "profile:framework", "facet:security:high", "fleet", "organization", "deployment", "workstation", "repository", "builtin:audit-compat-v1"}
	var got []string
	for _, source := range result.Sources {
		got = append(got, source.ID)
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(result.Fields["max_func_loc"], []string{"deployment"}) {
		t.Fatalf("provenance: %+v / %+v", got, result.Fields)
	}
}

// TestLoadEffectivePolicyIgnoresRegisterSection pins what ADR-0010 relies on: the register
// is a choice, not a bound, so it never reaches the resolved policy or its field
// provenance. The sealed digest still moves with the manifest bytes, as it does for any
// edit of that file, and only through the repository source.
func TestLoadEffectivePolicyIgnoresRegisterSection(t *testing.T) {
	overrides := "overrides:\n  complexity:\n    max_func_loc: 70\n"
	register := "register:\n  surfaces:\n    forge: docs\n  tasks:\n    ci_debugging: {register: social, max_tokens: 512}\n  evidence:\n    inline_max_lines: 40\n"
	plain, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: policyFixture(t, "", "", overrides)})
	if err != nil {
		t.Fatal(err)
	}
	withRegister, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: policyFixture(t, "", "", overrides+register)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plain.Policy, withRegister.Policy) || !reflect.DeepEqual(plain.Fields, withRegister.Fields) {
		t.Fatalf("register section changed the resolved policy: %+v vs %+v", plain.Policy, withRegister.Policy)
	}
	if len(plain.Sources) != len(withRegister.Sources) {
		t.Fatalf("register section changed the layer set: %+v vs %+v", plain.Sources, withRegister.Sources)
	}
	for i, source := range plain.Sources {
		other := withRegister.Sources[i]
		if source.ID != other.ID || (source.ID != "repository" && source.SHA256 != other.SHA256) {
			t.Fatalf("source %d moved: %+v vs %+v", i, source, other)
		}
	}
	if withRegister.Manifest.Register == nil || withRegister.Manifest.EffectiveRegister().Surfaces[SurfaceForge] != TextRegisterDocs {
		t.Fatalf("register section must still decode: %+v", withRegister.Manifest.Register)
	}
}

func TestLoadEffectivePolicyAuditCompatibilityAndCatalogRoot(t *testing.T) {
	root := policyFixture(t, "complexity:\n  max_func_loc: 75\n", "", "")
	catalog := t.TempDir()
	if err := os.Rename(filepath.Join(root, ".config"), filepath.Join(catalog, ".config")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root}); err == nil {
		t.Fatal("missing materialized profile accepted")
	}
	// The built-in default already is the audit ceiling (BUG-309), so the looser pinned 75
	// resolves to it either way; the audit layer adds only its provenance as a tied source.
	for _, audit := range []bool{false, true} {
		result, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, CatalogRoot: catalog, Audit: audit})
		if err != nil {
			t.Fatal(err)
		}
		if result.Policy.Complexity.MaxFuncLOC != hiss.DefaultMaxFuncLOC {
			t.Fatalf("audit=%v: got %d want %d", audit, result.Policy.Complexity.MaxFuncLOC, hiss.DefaultMaxFuncLOC)
		}
		want := []string{"builtin:defaults-v1"}
		if audit {
			want = append(want, "builtin:audit-compat-v1")
		}
		if got := result.Fields["max_func_loc"]; !reflect.DeepEqual(got, want) {
			t.Fatalf("audit=%v: contributors %v want %v", audit, got, want)
		}
	}
	writePolicyFile(t, catalog, ".config/archetypes/framework.yaml", "id: framework\ncomplexity:\n  max_func_loc: 10\n")
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, CatalogRoot: catalog}); !errors.Is(err, ErrLockDigestMismatch) {
		t.Fatalf("changed pinned source accepted: %v", err)
	}
}

func TestLoadEffectivePolicyInvalidInputsAndCancellation(t *testing.T) {
	root := policyFixture(t, "", "", "")
	for _, body := range []string{
		"complexity: null\n", "complexity: []\n", "complexity: {max_func_loc: 0}\n",
		"complexity: {max_func_loc: -1}\n", "complexity: {max_func_loc: null}\n",
		"complexity: {max_func_loc: '10'}\n", "complexity: {max_func_lco: 10}\n",
		"complexity: {max_func_loc: 10, max_func_loc: 20}\n", "{}\n---\n{}\n",
		"complexity: &policy {max_func_loc: 10}\ncopy: *policy\n", "complexity: {max_func_loc: 1.5}\n",
		"complxity: {max_func_loc: 10}\n", "budget: {max_tokens: 100}\n", "{}\n",
	} {
		path := writePolicyFile(t, root, "external.yaml", body)
		if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, DeploymentPath: path}); err == nil {
			t.Fatalf("invalid source accepted: %q", body)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := LoadEffectivePolicyContext(ctx, EffectiveOptions{Root: root}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	var noContext context.Context
	if _, err := LoadEffectivePolicyContext(noContext, EffectiveOptions{Root: root}); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{}); err == nil {
		t.Fatal("missing root accepted")
	}
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, FleetPath: filepath.Join(root, "missing.yaml")}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit missing source ignored: %v", err)
	}
}

func TestLoadEffectivePolicyRejectsSymlinksAndOversize(t *testing.T) {
	root := policyFixture(t, "", "", "")
	source := writePolicyFile(t, t.TempDir(), "source.yaml", "complexity: {max_func_loc: 10}\n")
	link := filepath.Join(root, "link.yaml")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, FleetPath: link}); err == nil {
		t.Fatal("symlink accepted")
	}
	writePolicyFile(t, root, "oversize.yaml", "#"+strings.Repeat("x", 1<<20))
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, FleetPath: filepath.Join(root, "oversize.yaml")}); err == nil {
		t.Fatal("oversize source accepted")
	}
}

func TestJoinCopiesInputsWhenEitherOperandAbsent(t *testing.T) {
	for _, left := range []bool{false, true} {
		original := DefaultPolicy()
		var result *ResolvedPolicy
		if left {
			result = Join(original, nil)
		} else {
			result = Join(nil, original)
		}
		result.Linters[0] = "changed"
		result.DevFeatures[0] = "changed"
		result.Complexity.MaxFuncLOC = 1
		if original.Linters[0] == "changed" || original.DevFeatures[0] == "changed" || original.Complexity.MaxFuncLOC == 1 {
			t.Fatal("join aliases input")
		}
	}
}

func TestResolvePolicyDefaultsAndOrderProperties(t *testing.T) {
	defaults, err := ResolvePolicy(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := DefaultPolicy().Complexity
	encoded := fmt.Sprintf("complexity:%d,%d,%d,%d", c.MaxCyclomatic, c.MaxCognitive, c.MaxFuncLOC, c.MaxStatements)
	if defaults.Sources[0].SHA256 != policyDigest([]byte(encoded)) {
		t.Fatal("default source identity differs from actual default values")
	}
	for _, limits := range [][3]int{{90, 75, 60}, {1, 1, 1}, {20, 10, 15}, {1000, 2000, 3000}} {
		layers := []PolicyLayer{policyTestLayer("a", limits[0]), policyTestLayer("b", limits[1]), policyTestLayer("c", limits[2])}
		forward, err := ResolvePolicy(t.Context(), layers)
		if err != nil {
			t.Fatal(err)
		}
		layers[0], layers[2] = layers[2], layers[0]
		reversed, err := ResolvePolicy(t.Context(), layers)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(forward.Policy, reversed.Policy) || forward.Policy.Complexity.MaxFuncLOC != min(hiss.DefaultMaxFuncLOC, limits[0], limits[1], limits[2]) {
			t.Fatalf("order changed effective constraints: %+v vs %+v", forward.Policy, reversed.Policy)
		}
		if forward.SHA256 == reversed.SHA256 {
			t.Fatal("ordered source provenance lost")
		}
	}
}

func TestEffectiveCatalogArtifactsContainExactPinnedSnapshotsOnly(t *testing.T) {
	root := policyFixture(t, "complexity: {max_func_loc: 75}\n", "name: Security\n", "")
	writePolicyFile(t, root, ".config/archetypes/unselected.yaml", "id: unselected\n")
	external := writePolicyFile(t, t.TempDir(), "private.yaml", "complexity: {max_func_loc: 50}\nprivate_setting: retained-only-in-external-source\n")
	result, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, DeploymentPath: external})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.CatalogArtifacts) != 2 {
		t.Fatalf("expected two pinned artifacts, got %+v", result.CatalogArtifacts)
	}
	for _, artifact := range result.CatalogArtifacts {
		if !filepath.IsLocal(artifact.RelativePath) || !strings.HasPrefix(artifact.RelativePath, archetypeDirName+"/") {
			t.Fatalf("unsafe artifact path: %s", artifact.RelativePath)
		}
		if policyDigest(artifact.Content) != artifact.SHA256 || strings.Contains(string(artifact.Content), "private_setting") {
			t.Fatalf("wrong snapshot in %s", artifact.RelativePath)
		}
		before := string(artifact.Content)
		writePolicyFile(t, root, artifact.RelativePath, "changed after resolution\n")
		if string(artifact.Content) != before {
			t.Fatal("artifact did not retain the validated snapshot")
		}
	}
}

func TestEffectivePolicySourceByteBoundaryAndEvidenceBound(t *testing.T) {
	profile := "complexity: {}\n#"
	profile += strings.Repeat("x", (1<<20)-len("id: framework\n")-len(profile))
	root := policyFixture(t, profile, "", "")
	result, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if err != nil || len(result.CatalogArtifacts[0].Content) != 1<<20 {
		t.Fatalf("exact 1MiB source rejected: %v", err)
	}
	layers := make([]PolicyLayer, maxEvidenceSources+1)
	for i := range layers {
		layers[i] = policyTestLayer(fmt.Sprintf("source:%d", i), 50+i)
	}
	bounded, err := ResolvePolicy(t.Context(), layers)
	if err != nil {
		t.Fatal(err)
	}
	evidence := bounded.Evidence()
	if strings.Count(evidence, "\n  source=") != maxEvidenceSources || !strings.Contains(evidence, "2 additional sources") {
		t.Fatalf("missing explicit bounded provenance: %s", evidence)
	}
}
