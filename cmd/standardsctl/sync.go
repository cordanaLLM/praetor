package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

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

func reconcileRemoteForge(bp *config.BranchProtectionPolicy) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		fmt.Println("  [INFO] Remote sync skipped (GITHUB_TOKEN not set; local files reconciled)")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	gh := forge.NewGitHubDriver(token, "")
	fmt.Println("  [SYNC] Reconciling remote branch protection rulesets on GitHub...")
	if err := gh.ReconcileProtection(ctx, "main", bp); err != nil {
		fmt.Printf("  [WARN] Remote branch protection sync failed: %v\n", err)
	} else {
		fmt.Println("  [OK] Remote branch protection synchronized on GitHub")
	}
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

	reconcileRemoteForge(&policy.BranchProtection)

	fmt.Println("Synchronization complete.")
	return nil
}

func synthesizeRuleset(targetPath string, bp config.BranchProtectionPolicy) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	ruleset := map[string]any{
		"name":        "praetor-main-protection",
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
					"required_status_checks": []map[string]string{
						{"context": "verify"},
						{"context": "Standards & Invariant Verification Gate"},
						{"context": "DCO 1.1 & REUSE Compliance Gate"},
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(ruleset, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(targetPath, data, 0644)
}

func synthesizeDefaultLabels(targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
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
	return os.WriteFile(targetPath, []byte(defaultLabels), 0644)
}
