package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

func printPlanHeader(manifest *config.Manifest, policy *config.ResolvedPolicy) error {
	reviewCount, _, err := policy.BranchProtection.EffectiveReviewRequirements()
	if err != nil {
		return fmt.Errorf("resolve branch protection reviews: %w", err)
	}
	fmt.Println("=== cordanaLLM/praetor Reconcile Plan (Dry Run) ===")
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
// Audit is set, so this resolves through the same audit-compatibility ceiling the audit path
// applies. Leaving it unset would report the uncapped value and reproduce the same disagreement
// one number further along.
func planEffectivePolicy(configPath string, manifest *config.Manifest) (*config.ResolvedPolicy, string, error) {
	root, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return nil, "", fmt.Errorf("resolve planned root: %w", err)
	}
	// Without a lockfile there are no pinned profiles to resolve, so defaults plus the
	// repository's own overrides is the whole policy rather than a degraded stand-in. This is
	// the ungoverned case -- planning a repository before it is adopted -- and it must keep
	// working, so the absence is checked for explicitly instead of being inferred from a read
	// error, which would also swallow a corrupt or unreadable lock.
	if !util.PathExists(filepath.Join(root, ".standards.lock")) {
		policy := config.DefaultPolicy()
		policy.ApplyOverrides(manifest.Overrides)
		const notice = "no .standards.lock: built-in defaults and repository overrides only"
		fmt.Printf("[INFO] %s\n", notice)
		return policy, notice, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), planPolicyTimeout)
	defer cancel()
	effective, err := config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{
		Root:         root,
		ManifestPath: configPath,
		Audit:        true,
	})
	if err != nil {
		return nil, "", fmt.Errorf("resolve effective policy: %w", err)
	}
	return &effective.Policy, "", nil
}

// planPolicyTimeout bounds the policy read (HISS-02).
const planPolicyTimeout = 30 * time.Second

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
