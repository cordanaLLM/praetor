package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// repoManifestPath is this repository's own manifest, relative to this package. It is used as the
// fixture because it is the exact shape the defect was reported against: a governed repository
// whose archetype declares a looser function-length bound than the audit ceiling, so the surfaces
// had something to disagree about. A synthetic fixture cannot reproduce it without hand-building a
// lockfile, and a hand-built lock would be testing the fixture rather than the loader.
const repoManifestPath = "../../.standards.yaml"

// TestPlanEffectivePolicy_Positive_MatchesWhatAuditEnforces is the regression.
//
// plan used to report config.DefaultPolicy() with only the repository's own overrides applied, so
// it never saw the pinned profile at all. For one repository the three surfaces an adopter reads
// gave three answers: the archetype declared 75, plan printed 100, and audit enforced 60. A dry run
// that does not preview the real run is worse than none, because it is believed.
func TestPlanEffectivePolicy_Positive_MatchesWhatAuditEnforces(t *testing.T) {
	manifest, err := config.LoadManifest(repoManifestPath)
	if err != nil {
		t.Skipf("repository manifest unavailable: %v", err)
	}
	planned, notice, err := planEffectivePolicy(t.Context(), repoManifestPath, "", manifest)
	if err != nil {
		t.Fatalf("plan policy: %v", err)
	}
	if notice != "" {
		t.Fatalf("this repository is governed, so no fallback should apply; got %q", notice)
	}

	root, err := filepath.Abs(filepath.Dir(repoManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	audited, err := config.LoadEffectivePolicyContext(context.Background(), config.EffectiveOptions{
		Root: root, ManifestPath: repoManifestPath, Audit: true,
	})
	if err != nil {
		t.Fatalf("audit policy: %v", err)
	}

	if planned.Complexity != audited.Policy.Complexity {
		t.Errorf("plan and audit disagree.\n  plan:  %+v\n  audit: %+v\na dry run must preview the real run",
			planned.Complexity, audited.Policy.Complexity)
	}
	// Function length alone cannot tell: the built-in default is the audit's own length since
	// BUG-309. The resolved cyclomatic, cognitive and statement limits still differ from it.
	if planned.Complexity == config.DefaultPolicy().Complexity {
		t.Errorf("plan reports the built-in defaults %+v, so it is not resolving the pinned profile",
			planned.Complexity)
	}
}

// TestPlanEffectivePolicy_Boundary_NoLockFallsBackAndSaysSo covers planning a repository before it
// is adopted. With no lockfile there are no pinned profiles to resolve, so defaults plus the
// repository's own overrides is the whole policy rather than a degraded stand-in -- and the reader
// is told which they got, because an unannounced fallback is indistinguishable from a resolved
// answer, which is the defect this change removes.
func TestPlanEffectivePolicy_Boundary_NoLockFallsBackAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".standards.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nrepository:\n  owner: f\n  name: f\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := config.LoadManifest(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	policy, notice, err := planEffectivePolicy(t.Context(), path, "", manifest)
	if err != nil {
		t.Fatalf("an unadopted repository must still plan: %v", err)
	}
	if notice == "" {
		t.Error("the fallback must announce itself")
	}
	if policy.Complexity.MaxFuncLOC != config.DefaultPolicy().Complexity.MaxFuncLOC {
		t.Errorf("with no lock the policy is the defaults, got %d", policy.Complexity.MaxFuncLOC)
	}
}

// TestPlanEffectivePolicy_Negative_CorruptLockIsNotSwallowed guards the fallback itself.
//
// Absence is detected by checking whether the file exists, not by treating a read failure as
// absence. The latter would also swallow a corrupt or unreadable lock and report built-in defaults
// as though they were the resolved policy -- reintroducing, inside the fix, the exact failure the
// fix exists to remove.
func TestPlanEffectivePolicy_Negative_CorruptLockIsNotSwallowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".standards.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nrepository:\n  owner: f\n  name: f\nprofiles:\n  - framework\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".standards.lock"), []byte("{[ not yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := config.LoadManifest(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if policy, notice, err := planEffectivePolicy(t.Context(), path, "", manifest); err == nil {
		t.Errorf("a corrupt lock must fail, got policy=%+v notice=%q", policy.Complexity, notice)
	}
}

// A manifest that vanished between load and resolution is an error, never a nil policy.
func TestPlanEffectivePolicy_Negative_MissingManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".standards.yaml")
	if policy, _, err := planEffectivePolicy(t.Context(), path, "", &config.Manifest{}); err == nil || policy != nil {
		t.Fatalf("missing manifest = %+v, %v", policy, err)
	}
}

// A pinned catalog that is not materialized plans only through --catalog-root, the catalog
// selection sync and audit already take, instead of failing where they can resolve it.
func TestPlanEffectivePolicy_Boundary_CatalogRootSelectsThePinnedCatalog(t *testing.T) {
	f := strictSyncFixture(t)
	catalog := detachCatalog(t, f.dir)
	manifest, err := config.LoadManifest(f.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// Negative: the planned root holds no catalog, so the lock does not resolve.
	if policy, _, err := planEffectivePolicy(t.Context(), f.manifestPath, "", manifest); err == nil {
		t.Fatalf("unmaterialized catalog planned %+v", policy.BranchProtection)
	}
	// Positive: the selected catalog resolves the pinned profile's branch protection.
	policy, notice, err := planEffectivePolicy(t.Context(), f.manifestPath, catalog, manifest)
	if err != nil || notice != "" {
		t.Fatalf("selected catalog: notice %q, err %v", notice, err)
	}
	if bp := policy.BranchProtection; !bp.RequireSignedCommits || bp.RequiredApprovingReviewers != 2 {
		t.Fatalf("selected catalog planned %+v, want signed commits and 2 reviewers", bp)
	}
	// Boundary: the flag reaches the resolver, and a relative spelling resolves against the
	// working directory, as it does for sync.
	t.Chdir(filepath.Dir(catalog))
	out, err := captureStdout(t, func() error {
		return runPlan([]string{"--config=" + f.manifestPath, "--catalog-root=" + filepath.Base(catalog)})
	})
	if err != nil {
		t.Fatalf("relative --catalog-root: %v\n%s", err, out)
	}
	mustContain(t, out, "Signed Commits Required:   true", "Approving Reviewers:       2")
}
