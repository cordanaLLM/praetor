package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
)

// planTimeout bounds one plan run: resolving the effective policy from the pinned catalog
// and inspecting the companion files it governs (HISS-02).
const planTimeout = 2 * time.Minute

func printPlanHeader(manifest *config.Manifest, policy *config.ResolvedPolicy) error {
	reviewCount, _, err := policy.BranchProtection.EffectiveReviewRequirements()
	if err != nil {
		return fmt.Errorf("resolve branch protection reviews: %w", err)
	}
	// The heading names the tool, never a repository: it used to print this product's own
	// owner/name in every adopted repository, contradicting the manifest-backed identity on
	// the very next line (issue #361).
	fmt.Println("=== Praetor Reconcile Plan (Dry Run) ===")
	fmt.Printf("Repository: %s/%s\n", manifest.Repository.Owner, manifest.Repository.Name)
	fmt.Printf("Profiles:   %v\n", manifest.Profiles)
	fmt.Printf("Facets:     %v\n", manifest.Facets)
	fmt.Println("\nTarget Invariants & Policy:")
	fmt.Printf("  - Max Cyclomatic Complexity: <= %d\n", policy.Complexity.MaxCyclomatic)
	fmt.Printf("  - Max Function LOC:          <= %d\n", policy.Complexity.MaxFuncLOC)
	fmt.Printf("  - Linear History Required:    %t\n", policy.BranchProtection.EnforceLinearHistory)
	fmt.Printf("  - Signed Commits Required:   %t\n", policy.BranchProtection.RequireSignedCommits)
	fmt.Printf("  - Approving Reviewers:       %d\n", reviewCount)
	fmt.Printf("  - Configured Reviewer Minimum: %d\n", policy.BranchProtection.RequiredApprovingReviewers)
	fmt.Printf("  - Review Mode:               %s\n", policy.BranchProtection.ReviewMode)
	fmt.Printf("  - Dismiss Stale Reviews:     %t\n", policy.BranchProtection.DismissStaleReviews)
	fmt.Printf("  - SLSA Provenance Level:     %d\n", policy.SupplyChain.SLSALevel)
	fmt.Printf("  - Cosign Attestation:        %t\n", policy.SupplyChain.EnforceCosign)
	fmt.Printf("  - SBOM Generation Required:  %t\n", policy.SupplyChain.RequireSBOM)
	return nil
}

// planEffectivePolicy resolves the same policy the audit will enforce.
//
// plan previously reported config.DefaultPolicy() with only the repository's own overrides applied,
// so it never saw the pinned profiles and facets at all. For one repository that produced three
// different answers to one question: the archetype file declared max_func_loc 75, plan printed 100
// and audit enforced 60. A dry run that does not preview what the real run will do is worse than no
// dry run, because it is believed.
//
// The resolution itself is config.ResolveRepositoryPolicy, shared with the editor projections
// and the language server so those cannot disagree with this preview either (issue #360). The
// no-lock notice is printed here, as it always was, and also returned for callers that test it.
// catalogRoot selects the pinned catalog as sync's --catalog-root does; empty is the planned root.
func planEffectivePolicy(ctx context.Context, configPath, catalogRoot string, manifest *config.Manifest) (*config.ResolvedPolicy, string, error) {
	policy, notice, err := config.ResolveRepositoryPolicyFromCatalog(ctx, configPath, catalogRoot, manifest)
	if err != nil {
		return nil, "", err
	}
	if policy == nil {
		return nil, "", fmt.Errorf("no manifest to plan at %s", configPath)
	}
	if notice != "" {
		fmt.Printf("[INFO] %s\n", notice)
	}
	return policy, notice, nil
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml; its directory is the planned root")
	catalogRoot := fs.String("catalog-root", "", "Root containing pinned .config/archetypes (default: planned root)")

	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("plan accepts no positional arguments, got %q", fs.Args())
	}

	manifest, err := config.LoadManifest(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	ctx, cancel := commandContext(planTimeout)
	defer cancel()
	policy, _, err := planEffectivePolicy(ctx, *configPath, *catalogRoot, manifest)
	if err != nil {
		return err
	}

	if err := printPlanHeader(manifest, policy); err != nil {
		return err
	}
	// The companion files are inspected next to the manifest, never the cwd.
	missing, drift, err := adopt.PlanDrift(ctx, filepath.Dir(*configPath), policy)
	if err != nil {
		return err
	}
	fmt.Println(adopt.FormatPlanStatus(missing, drift))
	return nil
}
