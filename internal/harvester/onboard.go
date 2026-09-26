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

// ErrOnboardingIncomplete identifies a scaffold whose dependency lock is missing or
// invalid. A valid lock without a local catalog is reported through LockStatus instead.
var ErrOnboardingIncomplete = errors.New("harvester: onboarding scaffold requires a valid dependency lock")

// onboardFilePerm is the mode of every file onboarding scaffolds into a repository.
const (
	onboardFilePerm         os.FileMode = 0o600
	maxOnboardDocumentBytes             = 8 * 1024 * 1024
	maxOnboardOutputs                   = 50
)

// OnboardPlan captures planned or applied onboarding actions for a repository.
// LockVerified is true only when every pinned content digest was hashed against the
// repository's catalog; LockStatus names the outcome of a live run.
type OnboardPlan struct {
	RepoPath     string            `json:"repo_path"`
	Archetype    string            `json:"archetype"`
	Facets       []string          `json:"facets"`
	Actions      []string          `json:"actions"`
	DryRun       bool              `json:"dry_run"`
	LockVerified bool              `json:"lock_verified"`
	LockStatus   config.LockStatus `json:"lock_status,omitempty"`
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
	lock, err := verifyOnboardLock(ctx, repoPath)
	if err != nil {
		return plan, fmt.Errorf("%w; scaffold files have been written: %w", ErrOnboardingIncomplete, err)
	}
	plan.LockStatus, plan.LockVerified = lock.Status, lock.Verified()
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
	// The scaffolded harness tells its reader to run compile-context --verify, which requires
	// the text register block; splice it first so that instruction holds from the start.
	if _, err := compiler.SyncRegisterBlock(ctx, repoPath, agentsPath, true); err != nil {
		return fmt.Errorf("splice text register into %s: %w", agentsPath, err)
	}
	// An existing manifest may select editors and agent clients (#202); the manifest
	// onboarding scaffolds itself selects neither, so every one applies.
	declared, err := config.LoadDeclaredTooling(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("read editors and agent_clients selection: %w", err)
	}
	tr := compiler.NewTranspiler()
	tr.Clients = declared.AgentClients
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
	return writeOnboardEditors(ctx, repoPath, declared.Editors)
}

func writeOnboardEditors(ctx context.Context, repoPath string, declared []string) error {
	selection, err := editor.SelectEditors(declared)
	if err != nil {
		return fmt.Errorf("editors in .standards.yaml: %w", err)
	}
	if len(selection.Editors) == 0 {
		return ctx.Err()
	}
	opts := editor.DefaultOptions()
	opts.WorkspaceRoot = repoPath
	opts.Editors = selection.Editors
	// Complexity is deliberately left at config.HISSComplexityCeiling here. Onboarding writes
	// the manifest and lock before the pinned profile catalog is materialized in the target
	// repository, so the declared policy is not resolvable at this point in the scaffold;
	// `praetorctl adopt` and `praetorctl editors generate` restate the resolved ceilings once
	// it is (issue #360).
	set, err := editor.SynthesizeContext(ctx, opts)
	if err != nil {
		return fmt.Errorf("synthesize editor configs: %w", err)
	}
	if len(set.Files) > maxOnboardOutputs {
		return fmt.Errorf("too many editor outputs: %d", len(set.Files))
	}
	for i := 0; i < len(set.Files) && i < maxOnboardOutputs; i++ {
		f := set.Files[i]
		if (f.Path == ".clang-tidy" || f.Path == ".editorconfig") && util.FileExists(filepath.Join(repoPath, f.Path)) {
			continue
		}
		if err := writeOnboardFile(ctx, repoPath, f.Path, []byte(f.Content)); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// writeOnboardFile checks cancellation before each mutation, then creates rel's directory
// and replaces rel atomically, both confined to root through a pinned handle on it, so no
// output can be redirected outside the repository, not even by a link swapped in after a
// check (BUG-826).
func writeOnboardFile(ctx context.Context, root, rel string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("onboarding cancelled before writing %s: %w", rel, err)
	}
	if err := util.MkdirConfined(root, filepath.Dir(rel), 0o700); err != nil {
		return fmt.Errorf("create onboarding output directory for %s: %w", rel, err)
	}
	if err := util.WriteFileConfined(root, rel, data, onboardFilePerm); err != nil {
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
// never invent a lockfile or report unhashed content digests as verified.
func verifyOnboardLock(ctx context.Context, repoPath string) (*config.LockValidation, error) {
	path, err := util.ConfinePath(repoPath, ".standards.yaml")
	if err != nil {
		return nil, err
	}
	data, err := readOnboardDocument(ctx, path)
	if err != nil {
		return nil, err
	}
	var manifest config.Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse onboarding manifest: %w", err)
	}
	return config.ValidateLockfile(ctx, repoPath, &manifest)
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
