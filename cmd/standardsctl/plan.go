package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

func printPlanHeader(manifest *config.Manifest, policy *config.ResolvedPolicy) {
	fmt.Println("=== cordanaLLM/praetor Reconcile Plan (Dry Run) ===")
	fmt.Printf("Repository: %s/%s\n", manifest.Repository.Owner, manifest.Repository.Name)
	fmt.Printf("Profiles:   %v\n", manifest.Profiles)
	fmt.Printf("Facets:     %v\n", manifest.Facets)
	fmt.Println("\nTarget Invariants & Policy:")
	fmt.Printf("  - Max Cyclomatic Complexity: <= %d\n", policy.Complexity.MaxCyclomatic)
	fmt.Printf("  - Max Function LOC:          <= %d\n", policy.Complexity.MaxFuncLOC)
	fmt.Printf("  - Linear History Required:    %t\n", policy.BranchProtection.EnforceLinearHistory)
	fmt.Printf("  - Signed Commits Required:   %t\n", policy.BranchProtection.RequireSignedCommits)
	fmt.Printf("  - Approving Reviewers:       %d\n", policy.BranchProtection.RequiredApprovingReviewers)
	fmt.Printf("  - Dismiss Stale Reviews:     %t\n", policy.BranchProtection.DismissStaleReviews)
	fmt.Printf("  - SLSA Provenance Level:     %d\n", policy.SupplyChain.SLSALevel)
	fmt.Printf("  - Cosign Attestation:        %t\n", policy.SupplyChain.EnforceCosign)
	fmt.Printf("  - SBOM Generation Required:  %t\n", policy.SupplyChain.RequireSBOM)
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

	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)

	printPlanHeader(manifest, policy)
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
