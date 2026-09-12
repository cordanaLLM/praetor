package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// rulesetName is the single name of the declarative branch protection ruleset, shared by
// the synthesized .github/rulesets/main.json and the remote reconciliation so that both
// describe the same object.
const rulesetName = "praetor-main-protection"

func reconcileLabels() error {
	if util.FileExists(".config/labels.yaml") {
		fmt.Println("  [OK] Labels verified (.config/labels.yaml)")
		return nil
	}
	fmt.Println("  [FIX] Synthesizing missing .config/labels.yaml...")
	if err := synthesizeDefaultLabels(".config/labels.yaml"); err != nil {
		return fmt.Errorf("failed creating labels manifest: %w", err)
	}
	fmt.Println("  [OK] Labels synthesized (.config/labels.yaml)")
	return nil
}

func reconcileRuleset(bp config.BranchProtectionPolicy) error {
	rulesetDir := filepath.Join(".github", "rulesets")
	rulesetPath := filepath.Join(rulesetDir, "main.json")
	if !util.FileExists(rulesetPath) {
		fmt.Println("  [FIX] Synthesizing declarative branch protection ruleset (.github/rulesets/main.json)...")
		if err := synthesizeRuleset(rulesetPath, bp); err != nil {
			return fmt.Errorf("failed synthesizing ruleset: %w", err)
		}
		fmt.Println("  [OK] Branch protection ruleset synthesized (.github/rulesets/main.json)")
	} else {
		fmt.Println("  [OK] Branch protection ruleset verified (.github/rulesets/main.json)")
	}
	return nil
}

// resolveSyncRepository determines the repository the remote reconciliation will mutate.
// It never guesses: without explicit coordinates the sync refuses to present the operator's
// token to an arbitrary repository.
func resolveSyncRepository(ctx context.Context, manifest *config.Manifest) (string, string, error) {
	if manifest != nil && manifest.Repository.Owner != "" && manifest.Repository.Name != "" {
		return manifest.Repository.Owner, manifest.Repository.Name, nil
	}
	if envRepo := os.Getenv("GITHUB_REPOSITORY"); envRepo != "" {
		parts := strings.SplitN(envRepo, "/", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
	}
	owner, repo, err := util.ResolveRepoIdentity(ctx, ".")
	if err != nil {
		return "", "", fmt.Errorf("cannot determine the repository to reconcile: set repository.owner "+
			"and repository.name in .standards.yaml: %w", err)
	}
	return owner, repo, nil
}

// reconcileRemoteForge converges the remote branch protection ruleset. A configured token
// plus an unresolved or non-converging target is a hard failure, never a warning.
func reconcileRemoteForge(manifest *config.Manifest, bp *config.BranchProtectionPolicy) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token := util.ResolveAuthTokenContext(ctx, "")
	if token == "" {
		fmt.Println("  [INFO] Remote sync skipped (GITHUB_TOKEN not set; local files reconciled)")
		return nil
	}

	owner, repo, err := resolveSyncRepository(ctx, manifest)
	if err != nil {
		return fmt.Errorf("remote branch protection sync refused: %w", err)
	}

	gh := forge.NewGitHubDriver(token, "")
	gh.SetRepository(owner, repo)
	gh.RulesetName = rulesetName
	gh.RequiredStatusChecks = forge.DefaultRequiredStatusChecks()
	gh.StrictStatusChecks = true

	fmt.Printf("  [SYNC] Reconciling remote branch protection ruleset %q on %s/%s...\n",
		rulesetName, owner, repo)
	if err := gh.ReconcileProtection(ctx, "main", bp); err != nil {
		return fmt.Errorf("remote branch protection sync failed for %s/%s: %w", owner, repo, err)
	}
	fmt.Printf("  [OK] Remote branch protection synchronized on %s/%s\n", owner, repo)
	return nil
}

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml")

	if err := fs.Parse(args); err != nil {
		return err
	}

	manifest, err := config.LoadManifest(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)

	fmt.Printf("Reconciling configuration for %s/%s...\n", manifest.Repository.Owner, manifest.Repository.Name)

	if err := reconcileLabels(); err != nil {
		return err
	}

	if util.FileExists(".standards.lock") {
		fmt.Println("  [OK] Lockfile .standards.lock verified")
	} else {
		fmt.Println("  [WARN] Lockfile .standards.lock missing. Run 'standardsctl init' to create.")
	}

	if util.FileExists("AGENTS.md") {
		fmt.Println("  [OK] Context harness AGENTS.md verified")
	} else {
		fmt.Println("  [WARN] Context harness AGENTS.md missing. Run 'standardsctl init' to create.")
	}

	if err := reconcileRuleset(policy.BranchProtection); err != nil {
		return err
	}

	if err := reconcileRemoteForge(manifest, &policy.BranchProtection); err != nil {
		return err
	}

	fmt.Println("Synchronization complete.")
	return nil
}

func synthesizeRuleset(targetPath string, bp config.BranchProtectionPolicy) error {
	if err := util.MkdirSecure(filepath.Dir(targetPath), 0o750); err != nil {
		return err
	}

	ruleset := map[string]any{
		"name":        rulesetName,
		"target":      "branch",
		"enforcement": "active",
		"conditions": map[string]any{
			"ref_name": map[string]any{
				"include": []string{"refs/heads/main", "refs/heads/lts-*"},
				"exclude": []string{},
			},
		},
		"rules": []map[string]any{
			{"type": "deletion"},
			{"type": "non_fast_forward"},
			{"type": "required_linear_history"},
			{"type": "required_signatures"},
			{
				"type": "pull_request",
				"parameters": map[string]any{
					"required_approving_review_count":   bp.RequiredApprovingReviewers,
					"dismiss_stale_reviews_on_push":     bp.DismissStaleReviews,
					"require_code_owner_review":         true,
					"require_last_push_approval":        false,
					"required_review_thread_resolution": true,
				},
			},
			{
				"type": "required_status_checks",
				"parameters": map[string]any{
					"strict_required_status_checks_policy": true,
					"required_status_checks":               requiredStatusCheckContexts(),
				},
			},
		},
	}

	data, err := json.MarshalIndent(ruleset, "", "  ")
	if err != nil {
		return err
	}
	return util.WriteFileSecure(targetPath, data, 0o600)
}

// requiredStatusCheckContexts renders the canonical required check contexts in the shape
// the GitHub ruleset API expects.
func requiredStatusCheckContexts() []map[string]string {
	names := forge.DefaultRequiredStatusChecks()
	contexts := make([]map[string]string, 0, len(names))
	for i := 0; i < len(names); i++ {
		contexts = append(contexts, map[string]string{"context": names[i]})
	}
	return contexts
}

func synthesizeDefaultLabels(targetPath string) error {
	if err := util.MkdirSecure(filepath.Dir(targetPath), 0o750); err != nil {
		return err
	}

	defaultLabels := `# Canonical Repository Label Taxonomy
version: 1
labels:
  - name: "hiss-violation"
    color: "d73a4a"
    description: "Code introduces a regression against HISS-16 invariants"

  - name: "hiss-waiver"
    color: "fbca04"
    description: "Requires cryptographically signed waiver approval"

  - name: "standards-sync"
    color: "0075ca"
    description: "Automated configuration sync generated by cordana-standards[bot]"

  - name: "flavor:bleeding"
    color: "e99695"
    description: "Dependencies or assets targeting bleeding-edge branch"

  - name: "flavor:latest"
    color: "0e8a16"
    description: "Dependencies or assets targeting latest stable release"

  - name: "flavor:lts"
    color: "5319e7"
    description: "Dependencies or assets targeting long-term support release"

  - name: "security:high"
    color: "b60205"
    description: "High-security facet: SLSA-3, Cosign, zero-CVE invariant"

  - name: "breaking-change"
    color: "b60205"
    description: "Breaking API change requiring mandatory Migration: footer"
`
	return util.WriteFileSecure(targetPath, []byte(defaultLabels), 0o600)
}
