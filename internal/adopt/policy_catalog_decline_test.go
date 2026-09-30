package adopt

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// signedTwoReviewerCatalog pins a framework profile whose branch protection differs from the
// built-in defaults: signed commits and two approving reviewers.
var signedTwoReviewerCatalog = map[string]string{"framework": "id: \"framework\"\nname: \"Adoption fixture framework\"\n" +
	"branch_protection:\n  require_signed_commits: true\n  required_approving_reviewers: 2\n"}

// declinedCatalogManifest is what the adoption fixture declares.
func declinedCatalogManifest() *config.Manifest {
	return &config.Manifest{Version: 1, Profiles: []string{"framework"}, Facets: []string{"security:high"}}
}

// adoptThenDeclineCatalog adopts a repository from a catalog pinning signedTwoReviewerCatalog,
// then declines policy-catalog in its manifest, and returns the repository and the lock source.
func adoptThenDeclineCatalog(t *testing.T) (string, string) {
	t.Helper()
	source := newCatalogLockSource(t, declinedCatalogManifest(), signedTwoReviewerCatalog)
	repo := newTestRepo(t, "catalog")
	if _, err := Adopt(t.Context(), AdoptOptions{Path: repo, Profile: "framework", Facets: []string{"security:high"},
		LockSourceRoot: source}); err != nil {
		t.Fatalf("first adoption: %v", err)
	}
	manifest := filepath.Join(repo, manifestFile)
	mustWrite(t, manifest, mustRead(t, manifest)+"adoption:\n  decline:\n    - policy-catalog\n")
	return repo, source
}

// Positive (#603): with policy-catalog declined, a re-adoption renders from the effective policy
// it reads from the pinned catalog on disk. The ruleset keeps the signature rule and two
// reviewers, with and without --facets; without --facets no documentation asset appears; the
// catalog keeps every byte; the report says what audit still requires; audit passes.
func TestAdoptDeclinedPolicyCatalog_Positive_RendersTheEffectivePolicy(t *testing.T) {
	repo, _ := adoptThenDeclineCatalog(t)
	ruleset := readRuleset(t, repo)
	if !strings.Contains(ruleset, "required_signatures") || !strings.Contains(ruleset, `"required_approving_review_count": 2`) {
		t.Fatalf("fixture precondition: the first ruleset lacks the profile's protection:\n%s", ruleset)
	}
	catalog := mustRead(t, filepath.Join(repo, ".config", "archetypes", "framework.yaml"))
	for _, facets := range [][]string{{"security:high"}, nil} {
		report, err := Adopt(t.Context(), AdoptOptions{Path: repo, Facets: facets})
		if err != nil {
			t.Fatalf("re-adoption (facets %v): %v", facets, err)
		}
		if got := readRuleset(t, repo); got != ruleset {
			t.Fatalf("re-adoption (facets %v) rewrote the ruleset:\n%s", facets, got)
		}
		detail := findActionDetail(report.ActionDetails, "policy-catalog")
		if !strings.Contains(detail, "Declined by adoption.decline in .standards.yaml; audit still requires the catalog the lock pins") {
			t.Fatalf("decline detail = %q", detail)
		}
		if report.EffectivePolicy == nil || !report.EffectivePolicy.Policy.BranchProtection.RequireSignedCommits {
			t.Fatalf("re-adoption (facets %v) reported no effective policy: %+v", facets, report.EffectivePolicy)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(DocumentationWorkflowFile))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("re-adoption without --facets wrote the documentation workflow: %v", err)
	}
	if got := mustRead(t, filepath.Join(repo, ".config", "archetypes", "framework.yaml")); got != catalog {
		t.Fatal("a declined policy-catalog step changed the catalog")
	}
	if summary, err := auditAdoptedRuleset(t, repo); err != nil || !strings.HasPrefix(summary, "[PASS]") {
		t.Fatalf("audit after re-adoption: %q, %v", summary, err)
	}
}

// Negative (#603): with policy-catalog declined, adoption refuses before its first write when the
// pinned catalog is gone, and when the lock this run writes pins a catalog the one on disk does
// not match (--force re-pins from --lock-source-root). Not one file of the repository changes.
func TestAdoptDeclinedPolicyCatalog_Negative_UnresolvableCatalogFailsBeforeWriting(t *testing.T) {
	repo, _ := adoptThenDeclineCatalog(t)
	if err := os.Remove(filepath.Join(repo, ".config", "archetypes", "framework.yaml")); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, repo)
	_, err := Adopt(t.Context(), AdoptOptions{Path: repo})
	if err == nil || !strings.Contains(err.Error(), "adoption.decline lists policy-catalog") ||
		!strings.Contains(err.Error(), "restore the catalog the lock pins") {
		t.Fatalf("missing catalog: err = %v, want a refusal naming the declined catalog", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repo))

	repinned, _ := adoptThenDeclineCatalog(t)
	moved := newCatalogLockSource(t, declinedCatalogManifest(), map[string]string{"framework": "id: \"framework\"\n" +
		"name: \"Adoption fixture framework\"\nbranch_protection:\n  require_signed_commits: true\n  required_approving_reviewers: 3\n"})
	before = snapshotTree(t, repinned)
	_, err = Adopt(t.Context(), AdoptOptions{Path: repinned, Force: true, LockSourceRoot: moved})
	if err == nil || !strings.Contains(err.Error(), "adoption.decline lists policy-catalog") {
		t.Fatalf("re-pinned lock over the catalog on disk: err = %v, want a refusal naming the declined catalog", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repinned))
}

// Boundary (#603): a dry run previews the ruleset the real run leaves, rendered from the catalog on
// disk, and writes nothing; only a session that resolved no policy falls back to the built-in
// defaults, and only in a dry run: a real run without a policy refuses to render the ruleset.
func TestAdoptDeclinedPolicyCatalog_Boundary_DryRunPreviewAndFallback(t *testing.T) {
	repo, _ := adoptThenDeclineCatalog(t)
	before := snapshotTree(t, repo)
	report, err := Adopt(t.Context(), AdoptOptions{Path: repo, DryRun: true})
	if err != nil {
		t.Fatalf("dry run with policy-catalog declined: %v", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repo))
	if len(report.Previews) != 1 || report.Previews[0].Action != PreviewUnchanged {
		t.Fatalf("dry-run ruleset preview = %+v, want the ruleset unchanged", report.Previews)
	}
	real := &adoptSession{repoPath: repo, report: &AdoptReport{}}
	if _, err := adoptionBranchPolicy(t.Context(), real); err == nil || !strings.Contains(err.Error(), "effective policy is unresolved") {
		t.Fatalf("real run without a policy: %v", err)
	}
	dry := &adoptSession{repoPath: repo, report: &AdoptReport{}, opts: AdoptOptions{DryRun: true}}
	if policy, err := adoptionBranchPolicy(t.Context(), dry); err != nil || policy.RequireSignedCommits {
		t.Fatalf("dry-run fallback = %+v, %v; want the built-in defaults", policy, err)
	}
}
