package main

import (
	"context"
	"encoding/json"
	"errors"
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

const (
	// syncTimeout bounds the whole reconciliation, including the forge round trips.
	syncTimeout = 2 * time.Minute
	// syncDirPerm and syncFilePerm are the modes of the synthesized, tracked files.
	syncDirPerm  os.FileMode = 0o755
	syncFilePerm os.FileMode = 0o644
)

// ErrRemoteTokenMissing reports a --remote sync without any usable credential.
var ErrRemoteTokenMissing = errors.New("--remote requires --token, GITHUB_TOKEN or GH_TOKEN")

// remoteSyncOptions carries the explicit inputs of a --remote reconciliation.
type remoteSyncOptions struct {
	token    string
	endpoint string
}

func reconcileLabels(rootDir string) error {
	labelsPath := filepath.Join(rootDir, ".config", "labels.yaml")
	if util.FileExists(labelsPath) {
		fmt.Println("  [OK] Labels verified (.config/labels.yaml)")
		return nil
	}
	fmt.Println("  [FIX] Synthesizing missing .config/labels.yaml...")
	if err := synthesizeDefaultLabels(labelsPath); err != nil {
		return fmt.Errorf("failed creating labels manifest: %w", err)
	}
	fmt.Println("  [OK] Labels synthesized (.config/labels.yaml)")
	return nil
}

func reconcileRuleset(rootDir string, bp config.BranchProtectionPolicy) error {
	rulesetPath := filepath.Join(rootDir, ".github", "rulesets", "main.json")
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

// reportCompanion prints whether a companion file exists next to the manifest.
func reportCompanion(rootDir, name, label string) {
	if util.FileExists(filepath.Join(rootDir, name)) {
		fmt.Printf("  [OK] %s %s verified\n", label, name)
		return
	}
	fmt.Printf("  [WARN] %s %s missing. Run 'praetorctl init' to create.\n", label, name)
}

// resolveSyncToken returns the explicit token or the CI environment token. The gh CLI
// session is deliberately never consulted: a forge write must use a credential the
// operator handed over on purpose.
func resolveSyncToken(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return tok
	}
	return os.Getenv("GH_TOKEN")
}

// verifyOriginIdentity refuses to write to any forge repository other than the one the
// checkout's origin remote points at, so a foreign manifest cannot redirect the ruleset.
func verifyOriginIdentity(ctx context.Context, rootDir, owner, name string) error {
	out, err := util.RunGit(ctx, rootDir, "config", "--get", "remote.origin.url")
	if err != nil || strings.TrimSpace(out) == "" {
		return fmt.Errorf("cannot verify manifest repository %s/%s: no origin remote in %s", owner, name, rootDir)
	}
	remoteOwner, remoteRepo := util.ExtractOwnerAndRepo(out)
	if !strings.EqualFold(remoteOwner, owner) || !strings.EqualFold(remoteRepo, name) {
		return fmt.Errorf("manifest declares %s/%s but origin points at %s/%s; refusing to modify a foreign repository",
			owner, name, remoteOwner, remoteRepo)
	}
	return nil
}

// reconcileRemoteForge pushes the branch protection ruleset and returns every failure:
// a missing credential, an unset or foreign repository identity, a fixture token that
// would perform no request, or a rejected API call.
func reconcileRemoteForge(ctx context.Context, rootDir string, manifest *config.Manifest, bp *config.BranchProtectionPolicy, remote remoteSyncOptions) error {
	token := resolveSyncToken(remote.token)
	if token == "" {
		return ErrRemoteTokenMissing
	}
	owner, name := manifest.Repository.Owner, manifest.Repository.Name
	if owner == "" || name == "" {
		return errors.New("manifest repository.owner and repository.name must be set before writing to the forge")
	}
	if err := verifyOriginIdentity(ctx, rootDir, owner, name); err != nil {
		return err
	}

	gh := forge.NewGitHubDriver(token, remote.endpoint)
	if gh.DryRun {
		return fmt.Errorf("token selects the forge fixture mode (prefix %q); no remote request was made", forge.FixtureTokenPrefix)
	}
	gh.SetRepository(owner, name)
	fmt.Printf("  [SYNC] Reconciling branch protection ruleset on GitHub for %s/%s...\n", owner, name)
	if err := gh.ReconcileProtection(ctx, "main", bp); err != nil {
		return err
	}
	fmt.Println("  [OK] Remote branch protection synchronized on GitHub")
	return nil
}

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml; its directory is the reconciled root")
	remote := fs.Bool("remote", false, "Also reconcile branch protection on GitHub (an explicit opt-in; nothing is pushed without it)")
	token := fs.String("token", "", "Forge API token for --remote (default: GITHUB_TOKEN, then GH_TOKEN; the gh CLI is never consulted)")
	endpoint := fs.String("endpoint", "", "Forge API endpoint for --remote (default: https://api.github.com)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("sync accepts no positional arguments, got %q", fs.Args())
	}
	remoteOpts := remoteSyncOptions{token: *token, endpoint: *endpoint}

	// HISS-02: local reconciliation and the forge round trips share one deadline.
	ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
	defer cancel()

	manifest, err := config.LoadManifest(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)
	rootDir := filepath.Dir(*configPath)

	fmt.Printf("Reconciling configuration for %s/%s...\n", manifest.Repository.Owner, manifest.Repository.Name)

	if err := reconcileLabels(rootDir); err != nil {
		return err
	}
	reportCompanion(rootDir, ".standards.lock", "Lockfile")
	reportCompanion(rootDir, "AGENTS.md", "Context harness")
	if err := reconcileRuleset(rootDir, policy.BranchProtection); err != nil {
		return err
	}

	if *remote {
		if err := reconcileRemoteForge(ctx, rootDir, manifest, &policy.BranchProtection, remoteOpts); err != nil {
			return fmt.Errorf("remote branch protection sync failed: %w", err)
		}
	} else {
		fmt.Println("  [INFO] Remote forge untouched (pass --remote to reconcile branch protection on GitHub)")
	}

	fmt.Println("Synchronization complete.")
	return nil
}

// rulesetRules renders the ruleset rules for the resolved policy: linear history and
// signed commits are emitted only when the policy actually requires them, so the local
// ruleset never contradicts the branch protection reconciled on the forge.
func rulesetRules(bp config.BranchProtectionPolicy) []map[string]any {
	rules := []map[string]any{
		{"type": "deletion"},
		{"type": "non_fast_forward"},
	}
	if bp.EnforceLinearHistory {
		rules = append(rules, map[string]any{"type": "required_linear_history"})
	}
	if bp.RequireSignedCommits {
		rules = append(rules, map[string]any{"type": "required_signatures"})
	}
	return append(rules,
		map[string]any{
			"type": "pull_request",
			"parameters": map[string]any{
				"required_approving_review_count":   bp.RequiredApprovingReviewers,
				"dismiss_stale_reviews_on_push":     bp.DismissStaleReviews,
				"require_code_owner_review":         true,
				"require_last_push_approval":        false,
				"required_review_thread_resolution": true,
			},
		},
		map[string]any{
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
	)
}

func synthesizeRuleset(targetPath string, bp config.BranchProtectionPolicy) error {
	if err := util.MkdirSecure(filepath.Dir(targetPath), syncDirPerm); err != nil {
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
		"rules": rulesetRules(bp),
	}

	data, err := json.MarshalIndent(ruleset, "", "  ")
	if err != nil {
		return err
	}
	return util.WriteFileSecure(targetPath, data, syncFilePerm)
}

func synthesizeDefaultLabels(targetPath string) error {
	if err := util.MkdirSecure(filepath.Dir(targetPath), syncDirPerm); err != nil {
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
	return util.WriteFileSecure(targetPath, []byte(defaultLabels), syncFilePerm)
}
