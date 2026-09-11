package harvester

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/editor"
	"gopkg.in/yaml.v3"
)

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
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("invalid repository path %q: %w", repoPath, err)
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

	if err := executeOnboarding(repoPath, repoName, arch, facets); err != nil {
		return nil, err
	}

	return plan, nil
}

func detectRepoArchetype(repoPath string) string {
	if _, err := os.Stat(filepath.Join(repoPath, "go.mod")); err == nil {
		return "framework"
	}
	if _, err := os.Stat(filepath.Join(repoPath, "Cargo.toml")); err == nil {
		return "native-gpu-systems"
	}
	if _, err := os.Stat(filepath.Join(repoPath, "package.json")); err == nil {
		return "app-service"
	}
	if _, err := os.Stat(filepath.Join(repoPath, "pyproject.toml")); err == nil {
		return "app-service"
	}
	return "template-seed"
}

func executeOnboarding(repoPath, repoName, arch string, facets []string) error {
	if err := ensureOnboardingManifest(repoPath, repoName, arch, facets); err != nil {
		return err
	}

	baselinePath := filepath.Join(repoPath, ".standards-baseline.json")
	if _, err := os.Stat(baselinePath); os.IsNotExist(err) {
		base := &baseline.Baseline{Version: 1, TotalInfractions: 0, Infractions: []baseline.Infraction{}}
		if err := baseline.SaveBaseline(baselinePath, base); err != nil {
			return fmt.Errorf("save baseline: %w", err)
		}
	}

	lockPath := filepath.Join(repoPath, ".standards.lock")
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		if err := os.WriteFile(lockPath, []byte("# SemVer lockfile\nversion: 1\npinned_version: \"v1.0.0\"\n"), 0644); err != nil {
			return fmt.Errorf("write lockfile: %w", err)
		}
	}

	agentsPath := filepath.Join(repoPath, "AGENTS.md")
	if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
		initialAgentsMD := fmt.Sprintf("# %s Agent Operating Harness\n\nRun verification before concluding any turn:\n```bash\nmake verify-all\n```\n", repoName)
		if err := os.WriteFile(agentsPath, []byte(initialAgentsMD), 0644); err != nil {
			return fmt.Errorf("write AGENTS.md: %w", err)
		}
	}

	tr := compiler.NewTranspiler()
	if res, err := tr.Compile(agentsPath); err == nil {
		if err := tr.WriteOutputs(res, repoPath); err != nil {
			return fmt.Errorf("write transpiled outputs: %w", err)
		}
	}

	opts := editor.DefaultOptions()
	opts.WorkspaceRoot = repoPath
	if set, err := editor.Synthesize(opts); err == nil {
		if err := editor.Write(set, repoPath); err != nil {
			return fmt.Errorf("write editor configs: %w", err)
		}
	}

	return nil
}

func ensureOnboardingManifest(repoPath, repoName, arch string, facets []string) error {
	manifestPath := filepath.Join(repoPath, ".standards.yaml")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		manifest := config.Manifest{
			Version: 1,
			Repository: config.RepositoryMetadata{
				Owner:      "cordanaLLM",
				Name:       repoName,
				Visibility: "public",
			},
			Profiles: []string{arch},
			Facets:   facets,
		}
		data, err := yaml.Marshal(&manifest)
		if err != nil {
			return fmt.Errorf("marshal manifest: %w", err)
		}
		if err := os.WriteFile(manifestPath, data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", manifestPath, err)
		}
	}
	return nil
}
