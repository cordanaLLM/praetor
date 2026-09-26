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

// defaultForgeHost is the git host a --remote sync expects origin to point at.
const defaultForgeHost = "github.com"

// remoteSyncOptions carries the explicit inputs of a --remote reconciliation.
type remoteSyncOptions struct {
	token    string
	endpoint string
	// host is the git host origin must name; the API endpoint alone cannot say which
	// host a checkout talks to (a test stub or a GitHub Enterprise API path differs).
	host string
}

// reconcileLabels verifies .config/labels.yaml, synthesizing it when missing, and returns
// the labels now on disk: the taxonomy a --remote sync writes to GitHub.
func reconcileLabels(ctx context.Context, rootDir string) ([]forge.Label, error) {
	labelsPath := filepath.Join(rootDir, ".config", "labels.yaml")
	data, exists, err := contextopt.ObserveSnapshot(ctx, labelsPath)
	if err != nil {
		return nil, fmt.Errorf("labels observation failed: %w", err)
	}
	if !exists {
		fmt.Println("  [FIX] Synthesizing missing .config/labels.yaml...")
		if err := synthesizeDefaultLabels(labelsPath); err != nil {
			return nil, fmt.Errorf("failed creating labels manifest: %w", err)
		}
		data, err = contextopt.ReadSnapshot(ctx, labelsPath)
		if err != nil {
			return nil, fmt.Errorf("labels readback failed: %w", err)
		}
	}
	labels, err := forge.ParseLabelTaxonomy(data)
	if err != nil {
		return nil, fmt.Errorf(".config/labels.yaml validation failed: %w", err)
	}
	if exists {
		updated, err := reconcileLabelDescriptions(ctx, labelsPath, data, labels)
		if err != nil {
			return nil, fmt.Errorf("failed updating managed label descriptions: %w", err)
		}
		if !bytes.Equal(updated, data) {
			if labels, err = forge.ParseLabelTaxonomy(updated); err != nil {
				return nil, fmt.Errorf(".config/labels.yaml validation after description update failed: %w", err)
			}
		}
	}
	fmt.Printf("  [OK] Labels verified (.config/labels.yaml: schema and %d unique labels)\n", len(labels))
	return labels, nil
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
// to replace once the canonical text is already present. labels is data already parsed
// by forge.ParseLabelTaxonomy.
func reconcileLabelDescriptions(ctx context.Context, labelsPath string, data []byte, labels []forge.Label) ([]byte, error) {
	updated := data
	changed := false
	for i := 0; i < len(labels) && i < forge.MaxLabelsLimit; i++ {
		label := labels[i]
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

// validateForgeHost accepts a bare host name such as github.com: no scheme, port, user
// info, path or whitespace, because it is compared with the host origin names.
func validateForgeHost(host string) error {
	if host == "" || strings.ContainsAny(host, "/:@\\ \t\r\n") {
		return fmt.Errorf("--forge-host must be a bare host name such as %s, got %q", defaultForgeHost, host)
	}
	return nil
}

// verifyOriginIdentity refuses to write to any forge repository other than the one the
// checkout's origin remote points at, so a foreign manifest cannot redirect the ruleset.
// The host is part of the identity: acme/widgets on gitlab.com, or a local directory
// whose path ends in acme/widgets, never authorizes a write to acme/widgets on GitHub.
func verifyOriginIdentity(ctx context.Context, rootDir, host, owner, name string) error {
	out, err := util.RunGit(ctx, rootDir, "config", "--get", "remote.origin.url")
	if err != nil || strings.TrimSpace(out) == "" {
		return fmt.Errorf("cannot verify manifest repository %s/%s: no origin remote in %s", owner, name, rootDir)
	}
	remote, err := util.ParseGitRemote(out)
	if err != nil {
		return fmt.Errorf("cannot verify manifest repository %s/%s: origin is not a %s remote: %w", owner, name, host, err)
	}
	if !strings.EqualFold(remote.Host, host) {
		return fmt.Errorf("manifest declares %s/%s on %s but origin points at host %s; refusing to modify a foreign repository",
			owner, name, host, remote.Host)
	}
	if !strings.EqualFold(remote.Path, owner+"/"+name) {
		return fmt.Errorf("manifest declares %s/%s but origin points at %s; refusing to modify a foreign repository",
			owner, name, remote.Path)
	}
	return nil
}

// remoteSyncInputs is the locally verified state a --remote sync writes to the forge.
type remoteSyncInputs struct {
	manifest *config.Manifest
	policy   *config.BranchProtectionPolicy
	contexts []string
	// labels is the taxonomy from .config/labels.yaml, as forge.ParseLabelTaxonomy read it.
	labels []forge.Label
}

// reconcileRemoteForge pushes the branch protection ruleset and the label taxonomy and
// returns every failure: a missing credential, an unset or foreign repository identity,
// or a rejected API call.
func reconcileRemoteForge(ctx context.Context, rootDir string, in remoteSyncInputs, remote remoteSyncOptions) error {
	token := resolveSyncToken(remote.token)
	if token == "" {
		return ErrRemoteTokenMissing
	}
	owner, name := in.manifest.Repository.Owner, in.manifest.Repository.Name
	if owner == "" || name == "" {
		return errors.New("manifest repository.owner and repository.name must be set before writing to the forge")
	}
	if err := validateForgeHost(remote.host); err != nil {
		return err
	}
	if err := verifyOriginIdentity(ctx, rootDir, remote.host, owner, name); err != nil {
		return err
	}

	gh := forge.NewGitHubDriver(token, remote.endpoint)
	gh.SetRepository(owner, name)
	gh.RulesetName = rulesetName
	gh.RequiredStatusChecks = append([]string(nil), in.contexts...)
	gh.StrictStatusChecks = true
	fmt.Printf("  [SYNC] Reconciling branch protection ruleset on GitHub for %s/%s...\n", owner, name)
	if err := gh.ReconcileProtection(ctx, "main", in.policy); err != nil {
		return err
	}
	fmt.Println("  [OK] Remote branch protection synchronized on GitHub")
	fmt.Printf("  [SYNC] Reconciling %d labels from .config/labels.yaml on GitHub...\n", len(in.labels))
	if err := gh.ReconcileLabels(ctx, in.labels); err != nil {
		return fmt.Errorf("reconcile labels: %w", err)
	}
	fmt.Println("  [OK] Remote labels synchronized on GitHub (labels absent from .config/labels.yaml are left alone)")
	return nil
}

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml; its directory is the reconciled root")
	remote := fs.Bool("remote", false, "Also reconcile branch protection and labels on GitHub (an explicit opt-in; nothing is pushed without it)")
	token := fs.String("token", "", "Forge API token for --remote (default: GITHUB_TOKEN, then GH_TOKEN; the gh CLI is never consulted)")
	endpoint := fs.String("endpoint", "", "Forge API endpoint for --remote (default: https://api.github.com)")
	catalogRoot := fs.String("catalog-root", "", "Root containing pinned .config/archetypes for lock digest verification (default: reconciled root)")
	forgeHost := fs.String("forge-host", defaultForgeHost, "Git host the origin remote must point at for --remote (GitHub Enterprise: the server's host name)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("sync accepts no positional arguments, got %q", fs.Args())
	}
	remoteOpts := remoteSyncOptions{token: *token, endpoint: *endpoint, host: *forgeHost}

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

	labels, err := reconcileLabels(ctx, rootDir)
	if err != nil {
		return err
	}
	missing, err := verifySyncCompanions(ctx, rootDir, *catalogRoot, manifest)
	if err != nil {
		return err
	}
	if err := reconcileRuleset(ctx, rootDir, policy.BranchProtection, contexts); err != nil {
		return err
	}
	if missing > 0 {
		return fmt.Errorf("local sync verification incomplete: %d companion checks missing or unverified; generated labels and ruleset retained", missing)
	}

	if *remote {
		in := remoteSyncInputs{manifest: manifest, policy: &policy.BranchProtection, contexts: contexts, labels: labels}
		if err := reconcileRemoteForge(ctx, rootDir, in, remoteOpts); err != nil {
			return fmt.Errorf("remote forge sync failed: %w", err)
		}
	} else {
		fmt.Println("  [INFO] Remote forge untouched (pass --remote to reconcile branch protection and labels on GitHub)")
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
