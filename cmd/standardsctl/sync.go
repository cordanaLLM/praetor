package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	// syncTimeout bounds the whole reconciliation, including the forge round trips.
	syncTimeout = 2 * time.Minute
	// syncDirPerm and syncFilePerm are the modes of the synthesized, tracked files.
	syncDirPerm  os.FileMode = 0o750
	syncFilePerm os.FileMode = 0o600
)

// rulesetName is shared by the local declaration and remote reconciliation.
const rulesetName = "praetor-main-protection"

// ErrRemoteTokenMissing reports a --remote sync without any usable credential.
var ErrRemoteTokenMissing = errors.New("--remote requires --token, GITHUB_TOKEN or GH_TOKEN")

// remoteSyncOptions carries the explicit inputs of a --remote reconciliation.
type remoteSyncOptions struct {
	token    string
	endpoint string
}

func reconcileLabels(ctx context.Context, rootDir string) error {
	labelsPath := filepath.Join(rootDir, ".config", "labels.yaml")
	data, exists, err := contextopt.ObserveSnapshot(ctx, labelsPath)
	if err != nil {
		return fmt.Errorf("labels observation failed: %w", err)
	}
	if !exists {
		fmt.Println("  [FIX] Synthesizing missing .config/labels.yaml...")
		if err := synthesizeDefaultLabels(labelsPath); err != nil {
			return fmt.Errorf("failed creating labels manifest: %w", err)
		}
		data, err = contextopt.ReadSnapshot(ctx, labelsPath)
		if err != nil {
			return fmt.Errorf("labels readback failed: %w", err)
		}
	}
	count, err := validateSyncLabels(data)
	if err != nil {
		return fmt.Errorf(".config/labels.yaml validation failed: %w", err)
	}
	if exists {
		if data, err = reconcileLabelDescriptions(ctx, labelsPath, data); err != nil {
			return fmt.Errorf("failed updating managed label descriptions: %w", err)
		}
	}
	fmt.Printf("  [OK] Labels verified (.config/labels.yaml: schema and %d unique labels; remote labels not checked)\n", count)
	return nil
}

// managedLabelDescriptions holds the canonical description sync.go itself authors for a
// label, keyed by name. A repository's own labels, colors and comments are never touched;
// only a drifted managed description is rewritten, and only that description's bytes.
var managedLabelDescriptions = map[string]string{
	"hiss-violation": "Code introduces a regression against HISS invariants",
}

// reconcileLabelDescriptions rewrites a managed label's description in place when the text
// on disk has drifted from canonical (e.g. wording from before the HISS standard rename),
// leaving every other byte of the file untouched. It returns the bytes now on disk so the
// caller reports against what it actually wrote rather than re-reading. Idempotent: nothing
// to replace once the canonical text is already present.
func reconcileLabelDescriptions(ctx context.Context, labelsPath string, data []byte) ([]byte, error) {
	var taxonomy struct {
		Labels []forge.Label `yaml:"labels"`
	}
	if err := yaml.Unmarshal(data, &taxonomy); err != nil {
		return data, fmt.Errorf("labels parse for description reconciliation: %w", err)
	}
	updated := data
	changed := false
	for _, label := range taxonomy.Labels {
		canonical, managed := managedLabelDescriptions[label.Name]
		if !managed || label.Description == canonical || !bytes.Contains(updated, []byte(label.Description)) {
			continue
		}
		updated = bytes.Replace(updated, []byte(label.Description), []byte(canonical), 1)
		changed = true
	}
	if !changed {
		return data, nil
	}
	if err := contextopt.ReplaceSnapshot(ctx, labelsPath, updated, contextopt.ReplaceOptions{Expected: data, Exists: true, Mode: syncFilePerm}); err != nil {
		return data, err
	}
	fmt.Println("  [FIX] Updated managed label description(s) in .config/labels.yaml")
	return updated, nil
}

func reconcileRuleset(ctx context.Context, rootDir string, bp config.BranchProtectionPolicy, contexts []string) error {
	rulesetPath := filepath.Join(rootDir, ".github", "rulesets", "main.json")
	data, exists, err := contextopt.ObserveSnapshot(ctx, rulesetPath)
	if err != nil {
		return fmt.Errorf("ruleset observation failed: %w", err)
	}
	if !exists {
		fmt.Println("  [FIX] Synthesizing declarative branch protection ruleset (.github/rulesets/main.json)...")
		if err := synthesizeRuleset(rulesetPath, bp, contexts); err != nil {
			return fmt.Errorf("failed synthesizing ruleset: %w", err)
		}
		data, err = contextopt.ReadSnapshot(ctx, rulesetPath)
		if err != nil {
			return fmt.Errorf("ruleset readback failed: %w", err)
		}
	}
	if err := validateSyncRuleset(data, bp, contexts); err != nil {
		return fmt.Errorf(".github/rulesets/main.json validation failed: %w", err)
	}
	fmt.Println("  [OK] Branch protection ruleset verified (.github/rulesets/main.json: matches declared policy)")
	return nil
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
// a missing credential, an unset or foreign repository identity, or a rejected API call.
func reconcileRemoteForge(ctx context.Context, rootDir string, manifest *config.Manifest, bp *config.BranchProtectionPolicy, remote remoteSyncOptions, contexts []string) error {
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
	gh.SetRepository(owner, name)
	gh.RulesetName = rulesetName
	gh.RequiredStatusChecks = append([]string(nil), contexts...)
	gh.StrictStatusChecks = true
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
	contexts, err := forge.RequiredStatusContexts(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("discover repository workflow checks: %w", err)
	}

	fmt.Printf("Reconciling configuration for %s/%s...\n", manifest.Repository.Owner, manifest.Repository.Name)

	if err := reconcileLabels(ctx, rootDir); err != nil {
		return err
	}
	missing, err := verifySyncCompanions(ctx, rootDir, manifest)
	if err != nil {
		return err
	}
	if err := reconcileRuleset(ctx, rootDir, policy.BranchProtection, contexts); err != nil {
		return err
	}
	if missing > 0 {
		return fmt.Errorf("local sync verification incomplete: %d companion checks missing; generated labels and ruleset retained", missing)
	}

	if *remote {
		if err := reconcileRemoteForge(ctx, rootDir, manifest, &policy.BranchProtection, remoteOpts, contexts); err != nil {
			return fmt.Errorf("remote branch protection sync failed: %w", err)
		}
	} else {
		fmt.Println("  [INFO] Remote forge untouched (pass --remote to reconcile branch protection on GitHub)")
	}

	fmt.Printf("Local sync checks finished: labels and ruleset verified; %d companion checks missing.\n", missing)
	return nil
}

func synthesizeRuleset(targetPath string, bp config.BranchProtectionPolicy, contexts []string) error {
	if err := util.MkdirSecure(filepath.Dir(targetPath), syncDirPerm); err != nil {
		return err
	}

	data, err := forge.RenderRepositoryRuleset(bp, contexts)
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
    description: "Code introduces a regression against HISS invariants"

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
