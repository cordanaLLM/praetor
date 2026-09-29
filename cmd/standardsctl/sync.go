package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	syncDirPerm  os.FileMode = util.TrackedDirPerm
	syncFilePerm os.FileMode = util.TrackedFilePerm
	// syncLabelsRel and syncRulesetRel name the files sync synthesizes, relative to the
	// repository root.
	syncLabelsRel  = ".config/labels.yaml"
	syncRulesetRel = ".github/rulesets/main.json"
)

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
	labelsPath := filepath.Join(rootDir, syncLabelsRel)
	data, exists, err := contextopt.ObserveSnapshot(ctx, labelsPath)
	if err != nil {
		return nil, fmt.Errorf("labels observation failed: %w", err)
	}
	if !exists {
		fmt.Println("  [FIX] Synthesizing missing .config/labels.yaml...")
		if err := synthesizeDefaultLabels(rootDir); err != nil {
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

// reconcileRuleset writes the ruleset for the default branch branch when it is absent, then
// validates the one on disk against it.
func reconcileRuleset(ctx context.Context, rootDir, branch string, bp config.BranchProtectionPolicy, contexts []string) error {
	rulesetPath := filepath.Join(rootDir, syncRulesetRel)
	data, exists, err := contextopt.ObserveSnapshot(ctx, rulesetPath)
	if err != nil {
		return fmt.Errorf("ruleset observation failed: %w", err)
	}
	if !exists {
		fmt.Println("  [FIX] Synthesizing declarative branch protection ruleset (.github/rulesets/main.json)...")
		if err := synthesizeRuleset(rootDir, branch, bp, contexts); err != nil {
			return fmt.Errorf("failed synthesizing ruleset: %w", err)
		}
		data, err = contextopt.ReadSnapshot(ctx, rulesetPath)
		if err != nil {
			return fmt.Errorf("ruleset readback failed: %w", err)
		}
	}
	if err := forge.ValidateRepositoryRuleset(data, branch, bp, contexts); err != nil {
		return fmt.Errorf(".github/rulesets/main.json validation failed for default branch %s: %w", branch, err)
	}
	fmt.Printf("  [OK] Branch protection ruleset verified (.github/rulesets/main.json: matches declared policy for default branch %s)\n", branch)
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
// The remote is read through util.ReadOriginRemote, the one origin-remote reader; a
// missing remote, a non-network remote and a read git did not answer all refuse.
func verifyOriginIdentity(ctx context.Context, rootDir, host, owner, name string) error {
	remote, err := util.ReadOriginRemote(ctx, rootDir)
	if errors.Is(err, util.ErrGitRemoteNotNetwork) {
		return fmt.Errorf("cannot verify manifest repository %s/%s: origin is not a %s remote: %w", owner, name, host, err)
	}
	if err != nil {
		return fmt.Errorf("cannot verify manifest repository %s/%s: %w", owner, name, err)
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
	// branch is the default branch the local ruleset was verified for (forge.RepositoryDefaultBranch).
	branch   string
	policy   *config.BranchProtectionPolicy
	contexts []string
	// labels is the taxonomy from .config/labels.yaml, as forge.ParseLabelTaxonomy read it.
	labels []forge.Label
}

// preflightRemoteForge runs every check a --remote sync can decide before its first forge
// write: a set repository identity, topics GitHub accepts, a bare forge host, and an origin
// remote naming the manifest's repository. A failure here leaves GitHub untouched.
func preflightRemoteForge(ctx context.Context, rootDir string, repo config.RepositoryMetadata, host string) error {
	if repo.Owner == "" || repo.Name == "" {
		return errors.New("manifest repository.owner and repository.name must be set before writing to the forge")
	}
	if err := forge.ValidateRepositoryTopics(repo.Topics); err != nil {
		return fmt.Errorf("reconcile repository metadata: %w", err)
	}
	if err := validateForgeHost(host); err != nil {
		return err
	}
	return verifyOriginIdentity(ctx, rootDir, host, repo.Owner, repo.Name)
}

// reconcileRemoteForge pushes the branch protection ruleset, the label taxonomy and the
// repository metadata and returns every failure: a missing credential, an unset or foreign
// repository identity, a topic GitHub would refuse, or a rejected API call.
func reconcileRemoteForge(ctx context.Context, rootDir string, in remoteSyncInputs, remote remoteSyncOptions) error {
	token := resolveSyncToken(remote.token)
	if token == "" {
		return ErrRemoteTokenMissing
	}
	if err := preflightRemoteForge(ctx, rootDir, in.manifest.Repository, remote.host); err != nil {
		return err
	}
	gh := forge.NewGitHubDriver(token, remote.endpoint)
	gh.SetRepository(in.manifest.Repository.Owner, in.manifest.Repository.Name)
	if err := reconcileRemoteRuleset(ctx, gh, rootDir, in); err != nil {
		return err
	}
	fmt.Printf("  [SYNC] Reconciling %d labels from .config/labels.yaml on GitHub...\n", len(in.labels))
	if err := gh.ReconcileLabels(ctx, in.labels); err != nil {
		return fmt.Errorf("reconcile labels: %w", err)
	}
	fmt.Println("  [OK] Remote labels synchronized on GitHub (labels absent from .config/labels.yaml are left alone)")
	fmt.Println("  [SYNC] Reconciling repository description, homepage and topics on GitHub...")
	metadata, err := gh.ReconcileRepositoryMetadata(ctx, in.manifest.Repository)
	if err != nil {
		return fmt.Errorf("reconcile repository metadata: %w", err)
	}
	printRepositoryMetadataReport(metadata)
	return nil
}

// reconcileRemoteRuleset writes the local .github/rulesets/main.json ruleset to GitHub under the
// same name and refs, requiring the status checks of it that report in the repository gh writes
// to, and then names the checks it left off.
func reconcileRemoteRuleset(ctx context.Context, gh *forge.GitHubDriver, rootDir string, in remoteSyncInputs) error {
	repository := gh.Owner + "/" + gh.Repo
	contexts, omitted, err := remoteStatusContexts(ctx, rootDir, repository, in.contexts)
	if err != nil {
		return err
	}
	gh.RulesetName = forge.RepositoryRulesetName
	gh.ProtectedRefs = forge.RepositoryRulesetRefs(in.branch)
	gh.RequiredStatusChecks = contexts
	gh.StrictStatusChecks = true
	fmt.Printf("  [SYNC] Reconciling branch protection ruleset on GitHub for %s...\n", repository)
	if err := gh.ReconcileProtection(ctx, in.branch, in.policy); err != nil {
		return err
	}
	fmt.Printf("  [OK] Remote branch protection synchronized on GitHub (%s and lts-*, read back; live rules praetor does not render kept)\n", in.branch)
	return reportOmittedStatusChecks(ctx, gh, in.branch, omitted)
}

// remoteStatusContexts returns the required status checks a --remote sync writes to the forge
// repository named repository, which are the jobs that report on every pull request there
// (forge.RequiredStatusContextsIn), and the checks of the local ruleset, local, it leaves off.
// A check leaves when a repository guard keeps its job out of that repository, such as a
// Platform Neutrality leg in an operational fork: no run there reports it, so requiring it would
// block every pull request.
func remoteStatusContexts(ctx context.Context, rootDir, repository string, local []string) (contexts, omitted []string, err error) {
	contexts, err = forge.RequiredStatusContextsIn(ctx, rootDir, repository)
	if err != nil {
		return nil, nil, fmt.Errorf("discover the required status checks of %s: %w", repository, err)
	}
	for _, check := range local {
		if !slices.Contains(contexts, check) {
			omitted = append(omitted, check)
		}
	}
	return contexts, omitted, nil
}

// reportOmittedStatusChecks reads the live ruleset back and names each check a --remote sync left
// off. The merge never removes a live required check (forge.GitHubDriver.ReconcileProtection),
// so a check an earlier sync wrote stays required: that is a warning, because every pull request
// waits for a check no run reports until the operator removes it from the ruleset.
func reportOmittedStatusChecks(ctx context.Context, gh *forge.GitHubDriver, branch string, omitted []string) error {
	if len(omitted) == 0 {
		return nil
	}
	live, err := gh.LiveRuleset(ctx, branch)
	if err != nil {
		return fmt.Errorf("read back the status checks left off: %w", err)
	}
	var absent, stale []string
	for _, check := range omitted {
		required := false
		if live != nil {
			if required, err = forge.RulesetRequiresStatusContext(live, check); err != nil {
				return fmt.Errorf("read back status check %q: %w", check, err)
			}
		}
		if required {
			stale = append(stale, check)
		} else {
			absent = append(absent, check)
		}
	}
	printOmittedStatusChecks(gh.Owner+"/"+gh.Repo, absent, stale)
	return nil
}

// printOmittedStatusChecks prints the checks left off that GitHub does not require, and warns
// about those a live ruleset still requires.
func printOmittedStatusChecks(repository string, absent, stale []string) {
	if len(absent) > 0 {
		fmt.Printf("  [INFO] Not required on GitHub for %s, because a repository guard skips their jobs there by default: %s\n",
			repository, strings.Join(absent, ", "))
	}
	if len(stale) > 0 {
		fmt.Printf("  [WARN] Still required on GitHub for %s, although a repository guard skips their jobs there by default: %s. "+
			"Unless this repository enables those jobs (for example through a repository variable the guard reads), every pull request waits for them; "+
			"sync --remote never removes a live required check, so remove them from ruleset %q by hand\n",
			repository, strings.Join(stale, ", "), forge.RepositoryRulesetName)
	}
}

// printRepositoryMetadataReport prints what the metadata reconciliation wrote, and any
// visibility drift it left for the operator.
func printRepositoryMetadataReport(r *forge.RepositoryMetadataReport) {
	changes := make([]string, 0, 2)
	if len(r.Updated) > 0 {
		changes = append(changes, "updated "+strings.Join(r.Updated, ", "))
	}
	if len(r.AddedTopics) > 0 {
		changes = append(changes, "topics added: "+strings.Join(r.AddedTopics, ", "))
	}
	if len(changes) == 0 {
		changes = append(changes, "already matched .standards.yaml")
	}
	fmt.Printf("  [OK] Repository metadata synchronized on GitHub (%s; fields .standards.yaml leaves unset and topics it does not name are left alone)\n",
		strings.Join(changes, "; "))
	if r.VisibilityDrift != "" {
		fmt.Printf("  [DRIFT] Repository visibility: %s; left unchanged, because changing visibility is the operator's decision\n", r.VisibilityDrift)
	}
}

// syncFlags are the parsed command-line inputs of sync.
type syncFlags struct {
	configPath  string
	catalogRoot string
	remote      bool
	remoteOpts  remoteSyncOptions
}

func parseSyncFlags(args []string) (syncFlags, error) {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	configPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml; its directory is the reconciled root")
	remote := fs.Bool("remote", false, "Also reconcile branch protection, labels and repository metadata on GitHub (an explicit opt-in; nothing is pushed without it)")
	token := fs.String("token", "", "Forge API token for --remote (default: GITHUB_TOKEN, then GH_TOKEN; the gh CLI is never consulted)")
	endpoint := fs.String("endpoint", "", "Forge API endpoint for --remote (default: https://api.github.com)")
	catalogRoot := fs.String("catalog-root", "", "Root containing pinned .config/archetypes for lock digest verification (default: reconciled root)")
	forgeHost := fs.String("forge-host", defaultForgeHost, "Git host the origin remote must point at for --remote (GitHub Enterprise: the server's host name)")

	if _, err := parseInterspersed(fs, args); err != nil {
		return syncFlags{}, err
	}
	if fs.NArg() > 0 {
		return syncFlags{}, fmt.Errorf("sync accepts no positional arguments, got %q", fs.Args())
	}
	return syncFlags{
		configPath:  *configPath,
		catalogRoot: *catalogRoot,
		remote:      *remote,
		remoteOpts:  remoteSyncOptions{token: *token, endpoint: *endpoint, host: *forgeHost},
	}, nil
}

func runSync(args []string) error {
	flags, err := parseSyncFlags(args)
	if err != nil {
		return err
	}

	// HISS-02: local reconciliation and the forge round trips share one deadline.
	ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
	defer cancel()

	manifest, err := config.LoadManifest(flags.configPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	rootDir := filepath.Dir(flags.configPath)
	contexts, err := forge.RequiredStatusContexts(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("discover repository workflow checks: %w", err)
	}
	// One resolution serves the local ruleset and the remote one, so they protect the same branch.
	branch, err := forge.RepositoryDefaultBranch(ctx, rootDir, manifest)
	if err != nil {
		return err
	}

	fmt.Printf("Reconciling configuration for %s/%s...\n", manifest.Repository.Owner, manifest.Repository.Name)

	labels, err := reconcileLabels(ctx, rootDir)
	if err != nil {
		return err
	}
	policy, missing, err := verifySyncLocal(ctx, flags.configPath, flags.catalogRoot, manifest, branch, contexts)
	if err != nil {
		return err
	}
	if missing > 0 {
		return fmt.Errorf("local sync verification incomplete: %d companion checks missing or unverified; generated files retained", missing)
	}

	if flags.remote {
		in := remoteSyncInputs{manifest: manifest, branch: branch, policy: &policy.BranchProtection, contexts: contexts, labels: labels}
		if err := reconcileRemoteForge(ctx, rootDir, in, flags.remoteOpts); err != nil {
			return fmt.Errorf("remote forge sync failed: %w", err)
		}
	} else {
		fmt.Println("  [INFO] Remote forge untouched (pass --remote to reconcile branch protection, labels and repository metadata on GitHub)")
	}

	fmt.Printf("Local sync checks finished: labels and ruleset verified; %d companion checks missing.\n", missing)
	return nil
}

// verifySyncLocal verifies the companion files, then reconciles the ruleset of the default
// branch branch against the branch protection adopt renders, resolved through the resolver plan
// uses. It returns how many checks are missing or unverified.
//
// A policy that does not resolve has no stand-in: the ruleset is then neither synthesized
// nor validated, because a file checked against built-in defaults plus overrides would be
// reported as matching the declared policy and rejected by the next sync that resolves it.
// The cause is counted once: a lock the selected catalog cannot verify is also why its
// policy does not resolve, so it is not counted again for the ruleset. A nil policy is only
// returned with a positive count, so the caller never reaches the forge without one.
func verifySyncLocal(ctx context.Context, configPath, catalogRoot string, manifest *config.Manifest, branch string, contexts []string) (*config.ResolvedPolicy, int, error) {
	rootDir := filepath.Dir(configPath)
	companions, err := verifySyncCompanions(ctx, rootDir, catalogRoot, manifest)
	if err != nil {
		return nil, 0, err
	}
	policy, _, cause := config.ResolveRepositoryPolicyFromCatalog(ctx, configPath, catalogRoot, manifest)
	if cause == nil && policy != nil {
		return policy, companions.incomplete, reconcileRuleset(ctx, rootDir, branch, policy.BranchProtection, contexts)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, 0, fmt.Errorf("resolve effective policy: %w", ctxErr)
	}
	if cause == nil {
		cause = fmt.Errorf("%s no longer exists", configPath)
	}
	counted, note := 1, "verification incomplete"
	if companions.lockUnverified {
		// lockUnverified was counted in companions.incomplete, so the count stays positive.
		counted, note = 0, "same cause as the lockfile above, counted once"
	}
	fmt.Printf("  [UNVERIFIED] Branch protection ruleset .github/rulesets/main.json neither checked nor synthesized: effective policy unresolved (%v); %s.\n", cause, note)
	return nil, companions.incomplete + counted, nil
}

// synthesizeRuleset writes the ruleset bp renders for branch to .github/rulesets/main.json
// below rootDir.
func synthesizeRuleset(rootDir, branch string, bp config.BranchProtectionPolicy, contexts []string) error {
	data, err := forge.RenderRepositoryRuleset(branch, bp, contexts)
	if err != nil {
		return err
	}
	return writeSyncFile(rootDir, syncRulesetRel, data)
}

// synthesizeDefaultLabels writes the default label taxonomy to .config/labels.yaml below rootDir.
func synthesizeDefaultLabels(rootDir string) error {
	return writeSyncFile(rootDir, syncLabelsRel, forge.DefaultLabelTaxonomy())
}

// writeSyncFile creates rel and its directory below rootDir through a pinned handle on rootDir
// (util.MkdirConfined, util.WriteFileConfined), so a symlinked .github or .config cannot
// redirect a synthesized file outside the repository, and a link planted at the file is
// refused instead of written through (BUG-826).
func writeSyncFile(rootDir, rel string, data []byte) error {
	if err := util.MkdirConfined(rootDir, filepath.Dir(rel), syncDirPerm); err != nil {
		return err
	}
	return util.WriteFileConfined(rootDir, rel, data, syncFilePerm)
}
