package config

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// Every HISS-04 function-length default derives from hiss.DefaultMaxFuncLOC. DefaultPolicy
// used to say 100 while the scanner and the audit said 60, so a lock-less plan or sync preview
// and every non-audit resolution promised a length no audit accepted (BUG-309).

func lockLessManifest(t *testing.T, overrides string) string {
	t.Helper()
	root := t.TempDir()
	writePolicyFile(t, root, ManifestFileName, "version: 1\nrepository:\n  owner: example\n  name: demo\n"+overrides)
	return root
}

func funcLOCOverride(loc int) string {
	return fmt.Sprintf("overrides:\n  complexity:\n    max_func_loc: %d\n", loc)
}

// resolvedFuncLOC returns the function length the plan preview (ResolveRepositoryPolicy) and
// the projections (ResolveRepositoryComplexity) state for root.
func resolvedFuncLOC(t *testing.T, root string) (preview, projection int) {
	t.Helper()
	policy, _, err := ResolveRepositoryPolicy(t.Context(), filepath.Join(root, ManifestFileName), nil)
	if err != nil || policy == nil {
		t.Fatalf("resolve policy: %+v err=%v", policy, err)
	}
	complexity, warning, err := ResolveRepositoryComplexity(t.Context(), root)
	if err != nil || warning != "" {
		t.Fatalf("resolve complexity: warning=%q err=%v", warning, err)
	}
	return policy.Complexity.MaxFuncLOC, complexity.MaxFuncLOC
}

func TestFuncLOCDefault_Positive_EveryDefaultIsTheScannerDefault(t *testing.T) {
	want := hiss.DefaultMaxFuncLOC
	defaults, err := ResolvePolicy(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	unadopted, _, err := ResolveRepositoryComplexity(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]int{
		"AuditMaxFuncLOC":           AuditMaxFuncLOC,
		"DefaultPolicy":             DefaultPolicy().Complexity.MaxFuncLOC,
		"ResolvePolicy defaults":    defaults.Policy.Complexity.MaxFuncLOC,
		"HISSComplexityCeiling":     HISSComplexityCeiling().MaxFuncLOC,
		"WithHISSDefaults":          ComplexityPolicy{}.WithHISSDefaults().MaxFuncLOC,
		"no-manifest workspace":     unadopted.MaxFuncLOC,
		"Join of two nil policies":  Join(nil, nil).Complexity.MaxFuncLOC,
		"lock-less manifest (plan)": func() int { p, _ := resolvedFuncLOC(t, lockLessManifest(t, "")); return p }(),
	} {
		if got != want {
			t.Errorf("%s function length = %d, want hiss.DefaultMaxFuncLOC %d", name, got, want)
		}
	}
}

// A resolution without the audit-compatibility layer (devcontainer, workstation) now starts
// from the same length as the audit, so a looser pinned profile no longer surfaces there.
func TestFuncLOCDefault_Positive_AuditLayerNoLongerChangesTheLength(t *testing.T) {
	root := policyFixture(t, fmt.Sprintf("complexity:\n  max_func_loc: %d\n", hiss.DefaultMaxFuncLOC+20), "", "")
	plain, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	audited, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: root, Audit: true})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Policy.Complexity.MaxFuncLOC != hiss.DefaultMaxFuncLOC || audited.Policy.Complexity.MaxFuncLOC != hiss.DefaultMaxFuncLOC {
		t.Fatalf("plain = %d, audited = %d, want both %d", plain.Policy.Complexity.MaxFuncLOC,
			audited.Policy.Complexity.MaxFuncLOC, hiss.DefaultMaxFuncLOC)
	}
}

// Overrides only tighten, so a lock-less manifest asking for more than the default is held to
// the default by the plan preview and the projections alike; before, the preview showed it.
func TestFuncLOCDefault_Negative_LooserOverrideDoesNotWiden(t *testing.T) {
	for _, loose := range []int{hiss.DefaultMaxFuncLOC + 1, 100} {
		preview, projection := resolvedFuncLOC(t, lockLessManifest(t, funcLOCOverride(loose)))
		if preview != hiss.DefaultMaxFuncLOC || projection != hiss.DefaultMaxFuncLOC {
			t.Errorf("override %d: preview %d, projection %d, want both %d", loose, preview, projection, hiss.DefaultMaxFuncLOC)
		}
	}
}

// An override equal to the default keeps it; one line tighter tightens every consumer.
func TestFuncLOCDefault_Boundary_OverrideAtAndBelowTheDefault(t *testing.T) {
	for _, loc := range []int{hiss.DefaultMaxFuncLOC, hiss.DefaultMaxFuncLOC - 1, 1} {
		preview, projection := resolvedFuncLOC(t, lockLessManifest(t, funcLOCOverride(loc)))
		if preview != loc || projection != loc {
			t.Errorf("override %d: preview %d, projection %d", loc, preview, projection)
		}
	}
}
