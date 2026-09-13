package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

func TestAdoptMaterializesCatalogAndRechecksOffline(t *testing.T) {
	root := newTestRepo(t, "catalog-adoption")
	source := newAdoptLockSource(t)
	opts := AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: source}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	policy, err := config.LoadEffectivePolicyContext(t.Context(), config.EffectiveOptions{Root: root, Audit: true})
	if err != nil || len(policy.CatalogArtifacts) != 5 {
		t.Fatalf("fresh adoption cannot resolve its local audit policy: %v", err)
	}
	for _, artifact := range policy.CatalogArtifacts {
		if want := mustRead(t, filepath.Join(source, artifact.RelativePath)); want != string(artifact.Content) {
			t.Fatalf("pinned source was not preserved exactly: %s", artifact.RelativePath)
		}
	}
	before := snapshotTree(t, root)
	opts.LockSourceRoot = ""
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("repeat adoption must work from materialized local pins: %v", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, root))
}

func catalogSession(t *testing.T) (*adoptSession, *config.EffectivePolicy) {
	t.Helper()
	s := lockAdoptSession(t)
	s.opts.LockSourceRoot = newAdoptLockSource(t)
	mustWrite(t, filepath.Join(s.repoPath, manifestFile), "version: 1\nprofiles: [framework]\nfacets: ['security:high']\n")
	if err := reconcileLockfile(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	policy, err := config.LoadEffectivePolicyContext(t.Context(), config.EffectiveOptions{Root: s.repoPath, CatalogRoot: s.opts.LockSourceRoot})
	if err != nil || len(policy.CatalogArtifacts) != 2 {
		t.Fatalf("expected two pinned source snapshots: %v", err)
	}
	return s, policy
}

func TestCatalogPreflightPreservesConflictsAndAllowsExplicitForce(t *testing.T) {
	s, policy := catalogSession(t)
	first := filepath.Join(s.repoPath, policy.CatalogArtifacts[0].RelativePath)
	conflict := filepath.Join(s.repoPath, policy.CatalogArtifacts[1].RelativePath)
	mustWrite(t, conflict, "# preserve operator edits\n")
	if err := reconcilePolicyCatalog(t.Context(), s); err == nil {
		t.Fatal("conflicting catalog was overwritten without force")
	}
	if got := mustRead(t, conflict); got != "# preserve operator edits\n" {
		t.Fatal("conflicting bytes changed")
	}
	if _, err := os.Stat(first); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("catalog wrote an earlier file before detecting conflict: %v", err)
	}
	s.opts.Force = true
	if err := reconcilePolicyCatalog(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, conflict); got != string(policy.CatalogArtifacts[1].Content) {
		t.Fatal("explicit forced update did not publish pinned bytes")
	}
}

func TestCatalogRejectsChangedPinnedSourceBeforeMaterialization(t *testing.T) {
	s, policy := catalogSession(t)
	path := filepath.Join(s.opts.LockSourceRoot, policy.CatalogArtifacts[0].RelativePath)
	mustWrite(t, path, "id: framework\ncomplexity:\n  max_func_loc: 1\n")
	if err := reconcilePolicyCatalog(t.Context(), s); err == nil {
		t.Fatal("changed source was accepted against the original pin")
	}
	if _, err := os.Stat(filepath.Join(s.repoPath, ".config")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid source caused catalog writes: %v", err)
	}
}

func TestCatalogRejectsDuplicateIdentityBeforeAnyWrite(t *testing.T) {
	s, policy := catalogSession(t)
	mustWrite(t, filepath.Join(s.repoPath, ".config", "archetypes", "shadow.yaml"), string(policy.CatalogArtifacts[0].Content))
	before := snapshotTree(t, s.repoPath)
	for _, options := range []AdoptOptions{
		{LockSourceRoot: s.opts.LockSourceRoot},
		{LockSourceRoot: s.opts.LockSourceRoot, Force: true},
		{LockSourceRoot: s.opts.LockSourceRoot, DryRun: true},
	} {
		s.opts = options
		if err := reconcilePolicyCatalog(t.Context(), s); err == nil {
			t.Fatal("prospective duplicate policy ID was accepted")
		}
		assertTreeUnchanged(t, before, snapshotTree(t, s.repoPath))
	}
}

func TestDryRunBaselineMatchesAppliedEffectivePolicy(t *testing.T) {
	s, _ := catalogSession(t)
	manifest := filepath.Join(s.repoPath, manifestFile)
	mustWrite(t, manifest, mustRead(t, manifest)+"overrides:\n  complexity:\n    max_func_loc: 5\n")
	mustWrite(t, filepath.Join(s.repoPath, "example.go"), "package example\nfunc example() int {\nx := 0\n"+strings.Repeat("x++\n", 8)+"return x\n}\n")
	s.opts.RecordBaseline = true
	s.opts.DryRun = true
	s.report.DebtBreakdown = map[string]int{}
	before := snapshotTree(t, s.repoPath)
	if err := reconcilePolicyCatalog(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if err := reconcileBaseline(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	planned := s.report.LegacyDebtCount
	assertTreeUnchanged(t, before, snapshotTree(t, s.repoPath))
	s.opts.DryRun = false
	if err := reconcilePolicyCatalog(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if err := reconcileBaseline(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if planned == 0 || planned != s.report.LegacyDebtCount {
		t.Fatalf("planned=%d actual=%d: dry run must use the applied policy", planned, s.report.LegacyDebtCount)
	}
}

func TestAdoptionBaselineUsesResolvedAuditCeiling(t *testing.T) {
	s, _ := catalogSession(t)
	manifest := filepath.Join(s.repoPath, manifestFile)
	mustWrite(t, manifest, mustRead(t, manifest)+"overrides:\n  complexity:\n    max_func_loc: 5\n")
	if err := reconcilePolicyCatalog(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(s.repoPath, "example.go"), "package example\nfunc example() int {\nx := 0\n"+strings.Repeat("x++\n", 8)+"return x\n}\n")
	legacy, err := hiss.Scan(t.Context(), s.repoPath, hiss.ScanOptions{MaxFuncLOC: defaultMaxFuncLOC})
	if err != nil || legacy.TotalInfractions != 0 {
		t.Fatalf("fixture must fit legacy scan ceiling: %v", err)
	}
	s.opts.RecordBaseline = true
	s.report.DebtBreakdown = map[string]int{}
	if err := reconcileBaseline(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	base, err := baseline.LoadBaseline(filepath.Join(s.repoPath, baselineFile))
	if err != nil || base.TotalInfractions == 0 || adoptionScanLimit(s) != 5 {
		t.Fatalf("baseline missed the tighter resolved policy: %v", err)
	}
}

func TestCatalogRejectsSymlinkDestination(t *testing.T) {
	s, _ := catalogSession(t)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(s.repoPath, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.repoPath, ".config", "archetypes")); err != nil {
		t.Fatal(err)
	}
	if err := reconcilePolicyCatalog(t.Context(), s); err == nil {
		t.Fatal("symlink catalog destination was accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside destination changed: %v", err)
	}
}

func TestCatalogDryRunCancellationAndBounds(t *testing.T) {
	s := lockAdoptSession(t)
	s.opts.DryRun = true
	s.opts.LockSourceRoot = newAdoptLockSource(t)
	before := snapshotTree(t, s.repoPath)
	if err := reconcilePolicyCatalog(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, s.repoPath))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := reconcilePolicyCatalog(ctx, s); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled catalog should fail: %v", err)
	}
	for _, artifacts := range [][]config.PolicyArtifact{
		make([]config.PolicyArtifact, maxAdoptPolicyFiles+1),
		{{RelativePath: ".config/archetypes/x.yaml", SHA256: "wrong", Content: []byte("id: x\n")}},
	} {
		if _, err := prepareCatalogWrites(t.Context(), s, artifacts); err == nil {
			t.Fatal("oversized or hash-mismatched catalog was accepted")
		}
	}
	assertTreeUnchanged(t, before, snapshotTree(t, s.repoPath))
}
