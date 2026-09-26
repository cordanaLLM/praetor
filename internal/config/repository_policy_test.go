package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fallback states the function length the audit enforces, not the 75 HISS-04 documents:
// the audit-compat layer caps every adopted repository at AuditMaxFuncLOC (BUG-445).
func TestHISSComplexityCeiling_Positive_MatchesAuditLength(t *testing.T) {
	ceiling := HISSComplexityCeiling()
	want := ComplexityPolicy{MaxCyclomatic: 10, MaxCognitive: 15, MaxFuncLOC: AuditMaxFuncLOC, MaxStatements: 50}
	if ceiling != want {
		t.Fatalf("ceiling = %+v, want %+v", ceiling, want)
	}
}

func TestWithHISSDefaults_FillsOnlyUnsetLimits(t *testing.T) {
	ceiling := HISSComplexityCeiling()
	cases := []struct {
		name string
		in   ComplexityPolicy
		want ComplexityPolicy
	}{
		{"zero value is the ceiling", ComplexityPolicy{}, ceiling},
		{"negative limits are unset", ComplexityPolicy{MaxCyclomatic: -1, MaxCognitive: -5, MaxFuncLOC: -60, MaxStatements: -1}, ceiling},
		{"resolved limits are kept", ComplexityPolicy{MaxCyclomatic: 7, MaxCognitive: 9, MaxFuncLOC: 42, MaxStatements: 30},
			ComplexityPolicy{MaxCyclomatic: 7, MaxCognitive: 9, MaxFuncLOC: 42, MaxStatements: 30}},
		{"a looser resolved limit is not narrowed", ComplexityPolicy{MaxFuncLOC: 100},
			ComplexityPolicy{MaxCyclomatic: ceiling.MaxCyclomatic, MaxCognitive: ceiling.MaxCognitive, MaxFuncLOC: 100, MaxStatements: ceiling.MaxStatements}},
		{"one is the smallest resolved limit", ComplexityPolicy{MaxCyclomatic: 1, MaxCognitive: 1, MaxFuncLOC: 1, MaxStatements: 1},
			ComplexityPolicy{MaxCyclomatic: 1, MaxCognitive: 1, MaxFuncLOC: 1, MaxStatements: 1}},
	}
	for _, tc := range cases {
		if got := tc.in.WithHISSDefaults(); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// A lock-backed repository resolves through the audit-compat layer, so the answer is the one
// `praetorctl audit` enforces, not the pinned profile's or the manifest's own number.
func TestResolveRepositoryPolicy_Positive_LockedMatchesAudit(t *testing.T) {
	root := policyFixture(t, "complexity:\n  max_func_loc: 80\n  max_cyclomatic: 9\n", "",
		"overrides:\n  complexity:\n    max_func_loc: 75\n")
	manifestPath := filepath.Join(root, ManifestFileName)
	policy, notice, err := ResolveRepositoryPolicy(t.Context(), manifestPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if notice != "" {
		t.Fatalf("a locked repository must not report the no-lock fallback: %q", notice)
	}
	audited, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, ManifestPath: manifestPath, Audit: true})
	if err != nil {
		t.Fatal(err)
	}
	if policy.Complexity != audited.Policy.Complexity {
		t.Fatalf("resolved %+v, audit enforces %+v", policy.Complexity, audited.Policy.Complexity)
	}
	if policy.Complexity.MaxFuncLOC != AuditMaxFuncLOC || policy.Complexity.MaxCyclomatic != 9 {
		t.Fatalf("pinned profile or audit cap not applied: %+v", policy.Complexity)
	}

	complexity, warning, err := ResolveRepositoryComplexity(t.Context(), root)
	if err != nil || warning != "" || complexity != audited.Policy.Complexity.WithHISSDefaults() {
		t.Fatalf("complexity = %+v warning=%q err=%v, want %+v", complexity, warning, err, audited.Policy.Complexity)
	}
}

func TestResolveRepositoryPolicy_Boundary_NoLockAndNoManifest(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, ManifestFileName)

	policy, notice, err := ResolveRepositoryPolicy(t.Context(), manifestPath, nil)
	if policy != nil || notice != "" || err != nil {
		t.Fatalf("absent manifest = (%+v, %q, %v), want (nil, \"\", nil)", policy, notice, err)
	}
	complexity, warning, err := ResolveRepositoryComplexity(t.Context(), root)
	if err != nil || warning != "" || complexity != HISSComplexityCeiling() {
		t.Fatalf("unadopted workspace = %+v warning=%q err=%v, want the ceiling", complexity, warning, err)
	}

	writePolicyFile(t, root, ManifestFileName,
		"version: 1\nrepository:\n  owner: example\n  name: demo\noverrides:\n  complexity:\n    max_func_loc: 42\n")
	policy, notice, err = ResolveRepositoryPolicy(t.Context(), manifestPath, nil)
	if err != nil || notice != NoLockNotice {
		t.Fatalf("no-lock manifest = notice %q err=%v", notice, err)
	}
	// The plan preview shows the lock-less repository against the built-in baseline ...
	if policy.Complexity.MaxFuncLOC != 42 || policy.Complexity.MaxCyclomatic != DefaultPolicy().Complexity.MaxCyclomatic {
		t.Fatalf("no-lock policy must be defaults plus overrides, got %+v", policy.Complexity)
	}
	// ... but a projection states the HISS-04 ceiling tightened by the overrides, never that
	// baseline's looser cyclomatic 15, cognitive 20 and 75 statements.
	complexity, warning, err = ResolveRepositoryComplexity(t.Context(), root)
	want := HISSComplexityCeiling()
	want.MaxFuncLOC = 42
	if err != nil || warning != "" || complexity != want {
		t.Fatalf("no-lock projection = %+v warning=%q err=%v, want %+v", complexity, warning, err, want)
	}

	// A caller that already decoded the manifest is not re-read from disk.
	decoded := &Manifest{Overrides: Overrides{Complexity: &ComplexityPolicy{MaxFuncLOC: 33}}}
	policy, _, err = ResolveRepositoryPolicy(t.Context(), manifestPath, decoded)
	if err != nil || policy.Complexity.MaxFuncLOC != 33 {
		t.Fatalf("supplied manifest ignored: %+v err=%v", policy, err)
	}
}

func TestResolveRepositoryPolicy_Negative_Failures(t *testing.T) {
	var noContext context.Context
	if _, _, err := ResolveRepositoryPolicy(noContext, "unused", nil); err == nil {
		t.Fatal("nil context accepted")
	}

	corrupt := t.TempDir()
	writePolicyFile(t, corrupt, ManifestFileName, "version: [\n")
	if _, _, err := ResolveRepositoryPolicy(t.Context(), filepath.Join(corrupt, ManifestFileName), nil); err == nil {
		t.Fatal("corrupt manifest accepted")
	}

	// A lock that exists but cannot be resolved is an error, never the no-lock fallback.
	locked := policyFixture(t, "complexity:\n  max_func_loc: 50\n", "", "")
	if err := os.WriteFile(filepath.Join(locked, ".standards.lock"), []byte("not: [a lock\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, notice, err := ResolveRepositoryPolicy(t.Context(), filepath.Join(locked, ManifestFileName), nil)
	if err == nil || notice == NoLockNotice || !strings.Contains(err.Error(), "resolve effective policy") {
		t.Fatalf("corrupt lock = notice %q err=%v", notice, err)
	}
}

// An empty root is the working directory; this package directory has no manifest.
func TestResolveRepositoryComplexity_Boundary_EmptyRoot(t *testing.T) {
	complexity, warning, err := ResolveRepositoryComplexity(t.Context(), "")
	if err != nil || warning != "" || complexity != HISSComplexityCeiling() {
		t.Fatalf("empty root = %+v warning=%q err=%v, want the ceiling", complexity, warning, err)
	}
}

// A manifest without a lock never widens the ceiling: an override looser than it is ignored,
// exactly as the audit-compat layer would cap it after adoption, and one tighter is kept.
func TestResolveRepositoryComplexity_Boundary_NoLockNeverLooserThanCeiling(t *testing.T) {
	root := t.TempDir()
	writePolicyFile(t, root, ManifestFileName, "version: 1\nrepository:\n  owner: example\n  name: demo\n")
	complexity, warning, err := ResolveRepositoryComplexity(t.Context(), root)
	if err != nil || warning != "" || complexity != HISSComplexityCeiling() {
		t.Fatalf("no-lock, no overrides = %+v warning=%q err=%v, want the ceiling", complexity, warning, err)
	}

	writePolicyFile(t, root, ManifestFileName, "version: 1\nrepository:\n  owner: example\n  name: demo\n"+
		"overrides:\n  complexity:\n    max_cyclomatic: 20\n    max_func_loc: 100\n    max_statements: 49\n")
	complexity, _, err = ResolveRepositoryComplexity(t.Context(), root)
	want := HISSComplexityCeiling()
	want.MaxStatements = 49
	if err != nil || complexity != want {
		t.Fatalf("looser overrides = %+v err=%v, want %+v", complexity, err, want)
	}
}

// A policy that exists but cannot be resolved -- including the lock `praetorctl init` writes,
// which pins nothing and carries no digest -- falls back to the ceiling tightened by whatever
// overrides the manifest declares, and says so. It is never an error: the projections kept
// working in that state before they read policy at all.
func TestResolveRepositoryComplexity_Negative_UnresolvablePolicyWarns(t *testing.T) {
	const manifest = "version: 1\nrepository:\n  owner: example\n  name: demo\nprofiles: [framework]\n" +
		"overrides:\n  complexity:\n    max_func_loc: 42\n"
	ceiling := HISSComplexityCeiling()
	withOverride := ceiling
	withOverride.MaxFuncLOC = 42
	for name, tc := range map[string]struct {
		manifest, lock, cause string
		want                  ComplexityPolicy
	}{
		"init lock":        {manifest, "# SemVer lockfile\nversion: 1\npinned_version: \"v0.0.0\"\n", "lockfile digest", withOverride},
		"corrupt lock":     {manifest, "not: [a lock\n", "resolve effective policy", withOverride},
		"corrupt manifest": {"version: [\n", "", "manifest", ceiling},
	} {
		root := t.TempDir()
		writePolicyFile(t, root, ManifestFileName, tc.manifest)
		if tc.lock != "" {
			writePolicyFile(t, root, ".standards.lock", tc.lock)
		}
		complexity, warning, err := ResolveRepositoryComplexity(t.Context(), root)
		if err != nil || complexity != tc.want {
			t.Errorf("%s: %+v err=%v, want %+v", name, complexity, err, tc.want)
		}
		if !strings.Contains(warning, "repository policy unresolved") || !strings.Contains(warning, tc.cause) {
			t.Errorf("%s: warning %q does not name the cause %q", name, warning, tc.cause)
		}
	}
}

// A resolution the caller's context interrupted is an error, never a warning-backed fallback,
// and a nil context is refused before any read.
func TestResolveRepositoryComplexity_Negative_InterruptedResolution(t *testing.T) {
	var noContext context.Context
	if _, _, err := ResolveRepositoryComplexity(noContext, t.TempDir()); err == nil {
		t.Fatal("nil context accepted")
	}
	root := policyFixture(t, "complexity:\n  max_func_loc: 50\n", "", "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	complexity, warning, err := ResolveRepositoryComplexity(ctx, root)
	if err == nil || !errors.Is(err, context.Canceled) || warning != "" || complexity != (ComplexityPolicy{}) {
		t.Fatalf("cancelled resolution = %+v warning=%q err=%v", complexity, warning, err)
	}
}
