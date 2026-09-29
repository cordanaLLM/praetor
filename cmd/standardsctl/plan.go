package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
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

// planFlags are the parsed command-line inputs of plan.
type planFlags struct {
	configPath  string
	catalogRoot string
	offline     bool
	remote      bool
	remoteOpts  remoteSyncOptions
}

func parsePlanFlags(args []string) (planFlags, error) {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml; its directory is the planned root")
	catalogRoot := fs.String("catalog-root", "", "Root containing pinned .config/archetypes (default: planned root)")
	offline := fs.Bool("offline", false, offlineFlagUsage)
	remote := fs.Bool("remote", false, "Also read the branch protection GitHub enforces on the default branch and compare it with the declared policy (read-only; an explicit opt-in)")
	remoteOpts := addRemoteFlags(fs)

	if _, err := parseInterspersed(fs, args); err != nil {
		return planFlags{}, err
	}
	if fs.NArg() > 0 {
		return planFlags{}, fmt.Errorf("plan accepts no positional arguments, got %q", fs.Args())
	}
	if *offline && *remote {
		return planFlags{}, errors.New("plan: --offline skips every forge read and --remote reads live branch protection; pass one of them")
	}
	return planFlags{configPath: *configPath, catalogRoot: *catalogRoot, offline: *offline, remote: *remote, remoteOpts: remoteOpts()}, nil
}

func runPlan(args []string) error {
	flags, err := parsePlanFlags(args)
	if err != nil {
		return err
	}

	manifest, err := config.LoadManifest(flags.configPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	ctx, cancel := commandContext(planTimeout)
	defer cancel()
	policy, _, err := planEffectivePolicy(ctx, flags.configPath, flags.catalogRoot, manifest)
	if err != nil {
		return err
	}

	if err := printPlanHeader(manifest, policy); err != nil {
		return err
	}
	// The companion files are inspected next to the manifest, never the cwd.
	missing, drift, err := adopt.PlanDrift(ctx, filepath.Dir(flags.configPath), policy)
	if err != nil {
		return err
	}
	fmt.Println(adopt.FormatPlanStatus(missing, drift))
	printPlanActionsPermissions(ctx, manifest, filepath.Dir(flags.configPath), flags.offline)
	if !flags.remote {
		fmt.Println("[INFO] Live branch protection not read: pass --remote to compare what GitHub enforces with the declared policy")
		return nil
	}
	return planRemoteProtection(ctx, filepath.Dir(flags.configPath), manifest, policy.BranchProtection, flags.remoteOpts)
}

// planRemoteProtection reads what GitHub enforces on the default branch, from its rulesets and
// its legacy protection object, and compares it with the declared policy and the status checks
// sync --remote would require there (remoteStatusContexts). It only reads: the origin remote
// must name the manifest's repository, as for sync --remote, and nothing is written. Drift is
// reported with the command that reconciles it; like the local drift above it, it leaves the
// exit status of this preview at zero.
func planRemoteProtection(ctx context.Context, rootDir string, manifest *config.Manifest, policy config.BranchProtectionPolicy, remote remoteSyncOptions) error {
	token := resolveSyncToken(remote.token)
	if token == "" {
		return ErrRemoteTokenMissing
	}
	if err := verifyRemoteRepository(ctx, rootDir, manifest.Repository, remote.host); err != nil {
		return err
	}
	branch, err := forge.RepositoryDefaultBranch(ctx, rootDir, manifest)
	if err != nil {
		return err
	}
	repository := manifest.Repository.Owner + "/" + manifest.Repository.Name
	contexts, _, err := remoteStatusContexts(ctx, rootDir, repository, nil)
	if err != nil {
		return err
	}
	gh := forge.NewGitHubDriver(token, remote.endpoint)
	gh.SetRepository(manifest.Repository.Owner, manifest.Repository.Name)
	target := protectionTarget{repository: repository, branch: branch, policy: policy, contexts: contexts}
	drifted, err := reportLiveProtection(ctx, gh, target, "compared with the declared policy", false)
	if err != nil {
		return err
	}
	if len(drifted) == 0 {
		fmt.Println("\nStatus: GitHub enforces every declared branch protection property.")
		return nil
	}
	fmt.Printf("\n[DRIFT] GitHub does not enforce the declared %s on %s.\n", strings.Join(drifted, ", "), branch)
	fmt.Println("Action: Run 'praetorctl sync --remote' to reconcile branch protection on GitHub.")
	return nil
}
