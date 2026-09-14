package harvester

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// ErrNotADirectory is returned when an onboarding target exists but is not a directory.
var ErrNotADirectory = errors.New("harvester: onboarding target is not a directory")

// ErrOnboardingIncomplete identifies a scaffold whose dependency lock is not verified.
var ErrOnboardingIncomplete = errors.New("harvester: onboarding scaffold requires a verified dependency lock")

// onboardFilePerm is the mode of every file onboarding scaffolds into a repository.
const (
	onboardFilePerm         os.FileMode = 0o600
	maxOnboardDocumentBytes             = 8 * 1024 * 1024
	maxOnboardOutputs                   = 50
)

// OnboardPlan captures planned or applied onboarding actions for a repository.
type OnboardPlan struct {
	RepoPath     string   `json:"repo_path"`
	Archetype    string   `json:"archetype"`
	Facets       []string `json:"facets"`
	Actions      []string `json:"actions"`
	DryRun       bool     `json:"dry_run"`
	LockVerified bool     `json:"lock_verified"`
}

// OnboardRepository scaffolds standards governance and agent harnesses into a repo.
func OnboardRepository(ctx context.Context, repoPath string, dryRun bool) (*OnboardPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before onboarding: %w", err)
	}

	info, err := os.Stat(repoPath)
	if err != nil {
		return nil, fmt.Errorf("invalid repository path %q: %w", repoPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %q", ErrNotADirectory, repoPath)
	}

	repoName := filepath.Base(repoPath)
	arch := detectRepoArchetype(repoPath)
	facets := []string{"security:high", "api:public-contract", "docs:seo-portal"}

	plan := &OnboardPlan{
		RepoPath:  repoPath,
		Archetype: arch,
		Facets:    facets,
		Actions:   make([]string, 0),
		DryRun:    dryRun,
	}

	plan.Actions = append(plan.Actions, fmt.Sprintf("Scaffold .standards.yaml (Profile: %s, Facets: %v)", arch, facets))
	plan.Actions = append(plan.Actions, "Initialize .standards-baseline.json (0 infractions)")
	plan.Actions = append(plan.Actions, "Verify existing .standards.lock pins and digests (source pin resolution is required if absent)")
	plan.Actions = append(plan.Actions, "Transpile AGENTS.md -> CLAUDE.md, Cursor, Windsurf, Copilot, Gemini")
	plan.Actions = append(plan.Actions, "Generate IDE settings (.vscode, .idea, .nvim.lua)")

	if dryRun {
		return plan, nil
	}

	if err := executeOnboarding(ctx, repoPath, repoName, arch, facets); err != nil {
		return plan, err
	}
	if err := verifyOnboardLock(ctx, repoPath); err != nil {
		return plan, fmt.Errorf("%w; scaffold files have been written: %w", ErrOnboardingIncomplete, err)
	}
	plan.LockVerified = true
	return plan, nil
}

// archetypeMarkers maps a repository marker file to the archetype it implies.
var archetypeMarkers = []struct {
	file      string
	archetype string
}{
	{"go.mod", "framework"},
	{"Cargo.toml", "native-gpu-systems"},
	{"pubspec.yaml", "app-service"},
	{"pom.xml", "app-service"},
	{"build.gradle", "app-service"},
	{"package.json", "app-service"},
	{"pyproject.toml", "app-service"},
}

// detectRepoArchetype infers the archetype from the build manifests present in the repo.
func detectRepoArchetype(repoPath string) string {
	for _, marker := range archetypeMarkers {
		if util.FileExists(filepath.Join(repoPath, marker.file)) {
			return marker.archetype
		}
	}
	return "template-seed"
}

// executeOnboarding writes the governance scaffold and the compiled agent harnesses. Every
// step propagates its failure: the returned plan claims these actions were performed, so a
// swallowed transpile or editor error would make the plan a lie.
func executeOnboarding(ctx context.Context, repoPath, repoName, arch string, facets []string) error {
	if err := ensureOnboardingManifest(ctx, repoPath, repoName, arch, facets); err != nil {
		return err
	}
	if err := ensureOnboardingBaseline(ctx, repoPath); err != nil {
		return err
	}
	if err := ensureOnboardingHarness(ctx, repoPath, repoName); err != nil {
		return err
	}

	return writeAgentHarness(ctx, repoPath)
}

// writeAgentHarness writes generated outputs with the caller's context and confinement.
// The compiler/editor convenience writers do not accept that context.
func writeAgentHarness(ctx context.Context, repoPath string) error {
	agentsPath, err := util.ConfinePath(repoPath, "AGENTS.md")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tr := compiler.NewTranspiler()
	data, err := readOnboardDocument(ctx, agentsPath)
	if err != nil {
		return err
	}
	res, err := tr.CompileContent(string(data))
	if err != nil {
		return fmt.Errorf("compile %s: %w", agentsPath, err)
	}
	if len(res.Files) > maxOnboardOutputs {
		return fmt.Errorf("too many compiled outputs: %d", len(res.Files))
	}
	for i := 0; i < len(res.Files) && i < maxOnboardOutputs; i++ {
		f := res.Files[i]
		if err := writeOnboardFile(ctx, repoPath, f.RelativePath, []byte(f.Content)); err != nil {
			return err
		}
	}
	return writeOnboardEditors(ctx, repoPath)
}

func writeOnboardEditors(ctx context.Context, repoPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	opts := editor.DefaultOptions()
	opts.WorkspaceRoot = repoPath
	set, err := editor.SynthesizeContext(ctx, opts)
	if err != nil {
		return fmt.Errorf("synthesize editor configs: %w", err)
	}
	if len(set.Files) > maxOnboardOutputs {
		return fmt.Errorf("too many editor outputs: %d", len(set.Files))
	}
	if err := editor.WriteContext(ctx, set, repoPath); err != nil {
		return fmt.Errorf("write editor configs: %w", err)
	}
	return ctx.Err()
}

// writeOnboardFile checks cancellation and output confinement before each mutation.
func writeOnboardFile(ctx context.Context, root, rel string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("onboarding cancelled before writing %s: %w", rel, err)
	}
	path, err := util.ConfinePath(root, rel)
	if err != nil {
		return fmt.Errorf("confine onboarding output %s: %w", rel, err)
	}
	if err := util.MkdirSecure(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create onboarding output directory: %w", err)
	}
	if err := util.WriteFileNoFollow(path, data, onboardFilePerm); err != nil {
		return fmt.Errorf("write onboarding output %s: %w", rel, err)
	}
	return nil
}

// ensureOnboardingBaseline writes an empty baseline when the repository has none.
func ensureOnboardingBaseline(ctx context.Context, repoPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	baselinePath, err := util.ConfinePath(repoPath, ".standards-baseline.json")
	if err != nil {
		return err
	}
	if util.PathExists(baselinePath) {
		return nil
	}
	base := &baseline.Baseline{Version: 1, TotalInfractions: 0, Infractions: []baseline.Infraction{}}
	if err := baseline.SaveBaseline(baselinePath, base); err != nil {
		return fmt.Errorf("save baseline: %w", err)
	}
	return nil
}

// ensureOnboardingHarness writes the initial AGENTS.md. The scaffolded
// AGENTS.md names the commands praetorctl actually provides; onboarding writes no Makefile,
// so it must not instruct agents to run a make target that does not exist.
func ensureOnboardingHarness(ctx context.Context, repoPath, repoName string) error {

	agentsPath := filepath.Join(repoPath, "AGENTS.md")
	if util.PathExists(agentsPath) {
		return nil
	}
	initialAgentsMD := fmt.Sprintf(
		"# %s Agent Operating Harness\n\nRun verification before concluding any turn:\n"+
			"```bash\npraetorctl audit\npraetorctl compile-context --verify\n```\n",
		repoName)
	if err := writeOnboardFile(ctx, repoPath, "AGENTS.md", []byte(initialAgentsMD)); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}
	return nil
}

// resolveGitIdentity reads the owner and repository name from the target's own origin
// remote. It deliberately does not fall back to the directory layout: an onboarding
// manifest must never claim an owner the repository did not itself declare.
func resolveGitIdentity(ctx context.Context, repoPath string) (owner, name string, err error) {
	remote, err := util.RunGit(ctx, repoPath, "config", "--get", "remote.origin.url")
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", "", fmt.Errorf("resolve onboarding identity: %w", ctxErr)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "", "", err
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", "", fmt.Errorf("read onboarding origin: %w", err)
		}
	}
	if remote == "" {
		return "", "", nil
	}
	owner, name = util.ExtractOwnerAndRepo(remote)
	return owner, name, nil
}

// ensureOnboardingManifest writes .standards.yaml when the repository has none. The owner
// is resolved from the repository's own git identity; it is never invented, and the
// visibility is left blank for the operator to declare rather than defaulted to "public".
func ensureOnboardingManifest(ctx context.Context, repoPath, repoName, arch string, facets []string) error {
	manifestPath := filepath.Join(repoPath, ".standards.yaml")
	if util.PathExists(manifestPath) {
		return nil
	}

	owner, resolvedName, err := resolveGitIdentity(ctx, repoPath)
	if err != nil {
		return err
	}
	if resolvedName == "" {
		resolvedName = repoName
	}

	manifest := config.Manifest{
		Version: 1,
		Repository: config.RepositoryMetadata{
			Owner: owner,
			Name:  resolvedName,
		},
		Profiles: []string{arch},
		Facets:   facets,
	}
	data, err := yaml.Marshal(&manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := writeOnboardFile(ctx, repoPath, ".standards.yaml", data); err != nil {
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}
	return nil
}

// verifyOnboardLock verifies real pins; onboarding has no source pin resolver and must
// never invent a lockfile or report an unverified scaffold as complete.
func verifyOnboardLock(ctx context.Context, repoPath string) error {
	path, err := util.ConfinePath(repoPath, ".standards.yaml")
	if err != nil {
		return err
	}
	data, err := readOnboardDocument(ctx, path)
	if err != nil {
		return err
	}
	var manifest config.Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse onboarding manifest: %w", err)
	}
	_, err = config.ValidateLockfile(ctx, repoPath, &manifest)
	return err
}

// readOnboardDocument bounds existing scaffold inputs and refuses special files before
// opening them, so a hostile AGENTS.md or manifest cannot block onboarding on a FIFO.
func readOnboardDocument(ctx context.Context, path string) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openRegularSource(path)
	if err != nil {
		return nil, fmt.Errorf("open onboarding document: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	data, err = io.ReadAll(io.LimitReader(file, maxOnboardDocumentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read onboarding document: %w", err)
	}
	if len(data) > maxOnboardDocumentBytes {
		return nil, fmt.Errorf("onboarding document exceeds %d bytes", maxOnboardDocumentBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
