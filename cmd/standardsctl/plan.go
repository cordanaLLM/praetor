package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

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

// checkPlanDrift inspects the companion files next to the manifest, never the cwd.
func checkPlanDrift(policy *config.ResolvedPolicy, rootDir string) ([]string, []string) {
	var missing []string
	var drift []string

	for _, rel := range []string{".standards.lock", "AGENTS.md", ".config/labels.yaml"} {
		if !util.FileExists(filepath.Join(rootDir, filepath.FromSlash(rel))) {
			missing = append(missing, rel)
		}
	}

	if policy.BranchProtection.EnforceLinearHistory || policy.BranchProtection.RequireSignedCommits {
		if !util.FileExists(filepath.Join(rootDir, ".github", "rulesets", "main.json")) {
			drift = append(drift, ".github/rulesets/main.json (Branch protection ruleset missing)")
		}
	}
	if policy.SupplyChain.RequireSBOM && !util.FileExists(filepath.Join(rootDir, ".github", "workflows", "sbom.yml")) {
		drift = append(drift, ".github/workflows/sbom.yml (SBOM & SLSA Level 3 workflow missing)")
	}

	return missing, drift
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
func planEffectivePolicy(configPath string, manifest *config.Manifest) (*config.ResolvedPolicy, string, error) {
	policy, notice, err := config.ResolveRepositoryPolicy(context.Background(), configPath, manifest)
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

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("plan accepts no positional arguments, got %q", fs.Args())
	}

	manifest, err := config.LoadManifest(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	policy, _, err := planEffectivePolicy(*configPath, manifest)
	if err != nil {
		return err
	}

	if err := printPlanHeader(manifest, policy); err != nil {
		return err
	}
	missing, drift := checkPlanDrift(policy, filepath.Dir(*configPath))

	if len(missing) > 0 || len(drift) > 0 {
		if len(missing) > 0 {
			fmt.Printf("\n[DRIFT] Missing baseline files: %s\n", strings.Join(missing, ", "))
		}
		if len(drift) > 0 {
			fmt.Printf("\n[DRIFT] Policy drift detected:\n")
			for _, d := range drift {
				fmt.Printf("  - %s\n", d)
			}
		}
		fmt.Println("\nAction: Run 'praetorctl sync' to reconcile repository configuration.")
	} else {
		fmt.Println("\nStatus: Local state matches declared policy. No changes required.")
	}

	return nil
}
