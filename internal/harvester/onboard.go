package harvester

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// onboardFilePerm is the mode of every file onboarding scaffolds into a repository.
const onboardFilePerm os.FileMode = 0o600

// OnboardPlan captures planned or applied onboarding actions for a repository.
type OnboardPlan struct {
	RepoPath  string   `json:"repo_path"`
	Archetype string   `json:"archetype"`
	Facets    []string `json:"facets"`
	Actions   []string `json:"actions"`
	DryRun    bool     `json:"dry_run"`
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
	plan.Actions = append(plan.Actions, "Initialize .standards.lock (pinned_version: v1.0.0)")
	plan.Actions = append(plan.Actions, "Transpile AGENTS.md -> CLAUDE.md, Cursor, Windsurf, Copilot, Gemini")
	plan.Actions = append(plan.Actions, "Generate IDE settings (.vscode, .idea, .nvim.lua)")

	if dryRun {
		return plan, nil
	}

	if err := executeOnboarding(ctx, repoPath, repoName, arch, facets); err != nil {
		return nil, err
	}

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
	if err := ensureOnboardingBaseline(repoPath); err != nil {
		return err
	}
	if err := ensureOnboardingHarness(repoPath, repoName); err != nil {
		return err
	}

	agentsPath := filepath.Join(repoPath, "AGENTS.md")
	tr := compiler.NewTranspiler()
	res, cErr := tr.Compile(agentsPath)
	if cErr != nil {
		return fmt.Errorf("compile %s: %w", agentsPath, cErr)
	}
	if err := tr.WriteOutputs(res, repoPath); err != nil {
		return fmt.Errorf("write transpiled outputs: %w", err)
	}

	opts := editor.DefaultOptions()
	opts.WorkspaceRoot = repoPath
	set, sErr := editor.Synthesize(opts)
	if sErr != nil {
		return fmt.Errorf("synthesize editor configs: %w", sErr)
	}
	if err := editor.Write(set, repoPath); err != nil {
		return fmt.Errorf("write editor configs: %w", err)
	}
	return nil
}

// ensureOnboardingBaseline writes an empty baseline when the repository has none.
func ensureOnboardingBaseline(repoPath string) error {
	baselinePath := filepath.Join(repoPath, ".standards-baseline.json")
	if util.PathExists(baselinePath) {
		return nil
	}
	base := &baseline.Baseline{Version: 1, TotalInfractions: 0, Infractions: []baseline.Infraction{}}
	if err := baseline.SaveBaseline(baselinePath, base); err != nil {
		return fmt.Errorf("save baseline: %w", err)
	}
	return nil
}

// ensureOnboardingHarness writes the lockfile and the initial AGENTS.md. The scaffolded
// AGENTS.md names the commands praetorctl actually provides; onboarding writes no Makefile,
// so it must not instruct agents to run a make target that does not exist.
func ensureOnboardingHarness(repoPath, repoName string) error {
	lockPath := filepath.Join(repoPath, ".standards.lock")
	if !util.PathExists(lockPath) {
		lockBody := []byte("# SemVer lockfile\nversion: 1\npinned_version: \"v1.0.0\"\n")
		if err := util.WriteFileSecure(lockPath, lockBody, onboardFilePerm); err != nil {
			return fmt.Errorf("write lockfile: %w", err)
		}
	}

	agentsPath := filepath.Join(repoPath, "AGENTS.md")
	if util.PathExists(agentsPath) {
		return nil
	}
	initialAgentsMD := fmt.Sprintf(
		"# %s Agent Operating Harness\n\nRun verification before concluding any turn:\n"+
			"```bash\npraetorctl audit\npraetorctl compile-context --verify\n```\n",
		repoName)
	if err := util.WriteFileSecure(agentsPath, []byte(initialAgentsMD), onboardFilePerm); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}
	return nil
}

// resolveGitIdentity reads the owner and repository name from the target's own origin
// remote. It deliberately does not fall back to the directory layout: an onboarding
// manifest must never claim an owner the repository did not itself declare.
func resolveGitIdentity(ctx context.Context, repoPath string) (owner, name string) {
	remote, err := util.RunGit(ctx, repoPath, "config", "--get", "remote.origin.url")
	if err != nil || remote == "" {
		return "", ""
	}
	return util.ExtractOwnerAndRepo(remote)
}

// ensureOnboardingManifest writes .standards.yaml when the repository has none. The owner
// is resolved from the repository's own git identity; it is never invented, and the
// visibility is left blank for the operator to declare rather than defaulted to "public".
func ensureOnboardingManifest(ctx context.Context, repoPath, repoName, arch string, facets []string) error {
	manifestPath := filepath.Join(repoPath, ".standards.yaml")
	if util.PathExists(manifestPath) {
		return nil
	}

	owner, resolvedName := resolveGitIdentity(ctx, repoPath)
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
	if err := util.WriteFileSecure(manifestPath, data, onboardFilePerm); err != nil {
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}
	return nil
}
