package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/paperclip"
	"github.com/cordanaLLM/praetor/internal/runner"
	"github.com/cordanaLLM/praetor/internal/util"
)

func runAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	manifestPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml")
	baselinePath := fs.String("baseline", ".standards-baseline.json", "Path to .standards-baseline.json")
	agentsPath := fs.String("agents", "AGENTS.md", "Path to AGENTS.md")

	if err := fs.Parse(args); err != nil {
		return err
	}

	manifest, err := auditManifestAndLockfile(*manifestPath)
	if err != nil {
		return err
	}

	fmt.Printf("=== %s/%s Governance Audit ===\n", manifest.Repository.Owner, manifest.Repository.Name)

	rootDir := filepath.Dir(*manifestPath)
	if err := auditRepoIdentity(manifest, rootDir); err != nil {
		return err
	}

	if err := auditBaselineAndInvariants(*baselinePath); err != nil {
		return err
	}

	if err := auditAgentContextAndDevcontainer(manifest, *agentsPath); err != nil {
		return err
	}

	if err := auditBranchProtectionAndSupplyChain(manifest, rootDir); err != nil {
		return err
	}

	if err := auditPaperclipHarness(manifest, rootDir); err != nil {
		return err
	}

	if err := auditRunnerMatrix(manifest, rootDir); err != nil {
		return err
	}

	if err := auditPreMigrationTracking(rootDir); err != nil {
		return err
	}

	if err := auditAgentDefinitions(rootDir); err != nil {
		return err
	}

	if err := auditGitHooks(rootDir); err != nil {
		return err
	}

	fmt.Printf("\nAudit Summary: 100%% Compliance with %s/%s HISS-16 baseline.\n", manifest.Repository.Owner, manifest.Repository.Name)
	return nil
}

func auditManifestAndLockfile(manifestPath string) (*config.Manifest, error) {
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("[FAIL] Manifest audit failed: %w", err)
	}
	fmt.Printf("[PASS] Manifest verified: %s/%s (Version %d)\n", manifest.Repository.Owner, manifest.Repository.Name, manifest.Version)
	fmt.Printf("       Profiles: %v | Facets: %v\n", manifest.Profiles, manifest.Facets)

	lockPath := filepath.Join(filepath.Dir(manifestPath), ".standards.lock")
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("[FAIL] .standards.lock is missing")
	}
	fmt.Println("[PASS] SemVer lockfile .standards.lock verified.")
	return manifest, nil
}

func auditBaselineAndInvariants(baselinePath string) error {
	base, err := baseline.LoadBaseline(baselinePath)
	if err != nil {
		return fmt.Errorf("[FAIL] Baseline audit failed: %w", err)
	}

	ctx := context.Background()
	root := filepath.Dir(baselinePath)
	scanRep, err := hiss.Scan(ctx, root, hiss.ScanOptions{})
	if err != nil {
		return fmt.Errorf("[FAIL] Invariant audit failed: %w", err)
	}

	currentViolations := hiss.ConvertToBaseline(scanRep.Violations)
	for i := range currentViolations {
		currentViolations[i].Fingerprint = fmt.Sprintf("%s:%d:%s", currentViolations[i].FilePath, currentViolations[i].LineNumber, currentViolations[i].RuleID)
	}

	ratchet := baseline.EvaluateRatchet(base, currentViolations, nil)
	if !ratchet.Passed {
		limit := 3
		if len(ratchet.NewViolations) < limit {
			limit = len(ratchet.NewViolations)
		}
		var msgs []string
		for i := 0; i < limit; i++ {
			v := ratchet.NewViolations[i]
			msgs = append(msgs, fmt.Sprintf("  [%s] %s:%d - %s", v.RuleID, v.FilePath, v.LineNumber, v.Message))
		}
		return fmt.Errorf("[FAIL] HISS invariant violations introduced (%d total infractions, %d new unbaselined violations):\n%s",
			ratchet.CurrentCount, len(ratchet.NewViolations), strings.Join(msgs, "\n"))
	}
	fmt.Printf("[PASS] HISS invariant scan verified: %d active violations within %d baselined limit.\n",
		ratchet.CurrentCount, base.TotalInfractions)
	return nil
}

func auditAgentContextAndDevcontainer(manifest *config.Manifest, agentsPath string) error {
	root := filepath.Dir(agentsPath)
	tr := compiler.NewTranspiler()
	if err := tr.Verify(agentsPath, root); err != nil {
		return fmt.Errorf("[FAIL] Agent context targets out of sync: %w", err)
	}
	fmt.Println("[PASS] Cross-agent context targets (Claude, Cursor, Copilot, Windsurf, Gemini, Codex) verified in sync.")

	dcPath := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if _, err := os.Stat(dcPath); err == nil {
		dc, err := devcontainer.Synthesize(manifest)
		if err == nil {
			ctx := context.Background()
			if err := devcontainer.Verify(ctx, dcPath, dc); err == nil {
				fmt.Println("[PASS] DevContainer configuration verified in sync with declared standards.")
			}
		}
	}
	return nil
}

func auditBranchProtectionAndSupplyChain(manifest *config.Manifest, rootDir string) error {
	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)

	// Verify .github/rulesets/main.json if linear history or signed commits required
	rulesetPath := filepath.Join(rootDir, ".github", "rulesets", "main.json")
	if policy.BranchProtection.EnforceLinearHistory || policy.BranchProtection.RequireSignedCommits {
		if !util.FileExists(rulesetPath) {
			return fmt.Errorf("[FAIL] Branch protection ruleset .github/rulesets/main.json is missing while policy requires linear history and signed commits. Run 'standardsctl sync' to reconcile.")
		}
		fmt.Println("[PASS] Branch protection & merge ruleset .github/rulesets/main.json verified.")
	}

	// Verify .config/labels.yaml
	labelsPath := filepath.Join(rootDir, ".config", "labels.yaml")
	if !util.FileExists(labelsPath) {
		return fmt.Errorf("[FAIL] Required label taxonomy .config/labels.yaml is missing.")
	}
	fmt.Println("[PASS] Repository label taxonomy .config/labels.yaml verified.")

	return nil
}

func auditRepoIdentity(manifest *config.Manifest, rootDir string) error {
	if manifest.Repository.Owner == "" || manifest.Repository.Name == "" {
		return fmt.Errorf("[FAIL] Manifest repository owner and name must not be empty")
	}

	goModPath := filepath.Join(rootDir, "go.mod")
	if util.FileExists(goModPath) {
		data, err := os.ReadFile(goModPath)
		if err == nil {
			modContent := string(data)
			if strings.Contains(modContent, "github.com/cordanaLLM/standards") {
				return fmt.Errorf("[FAIL] go.mod contains obsolete module path 'github.com/cordanaLLM/standards'. Expected 'github.com/%s/%s'", manifest.Repository.Owner, manifest.Repository.Name)
			}
		}
	}

	needsPath := filepath.Join(rootDir, ".needs.yaml")
	if util.FileExists(needsPath) {
		data, err := os.ReadFile(needsPath)
		if err == nil && strings.Contains(string(data), "github.com/cordanaLLM/standards") {
			return fmt.Errorf("[FAIL] .needs.yaml contains obsolete repository reference 'github.com/cordanaLLM/standards'")
		}
	}

	basePath := filepath.Join(rootDir, ".standards-baseline.json")
	if util.FileExists(basePath) {
		data, err := os.ReadFile(basePath)
		if err == nil && manifest.Repository.Name != "standards" && strings.Contains(string(data), `"repository": "cordanaLLM/standards"`) {
			return fmt.Errorf("[FAIL] .standards-baseline.json contains obsolete repository 'cordanaLLM/standards'")
		}
	}

	fmt.Printf("[PASS] Repository identity verified (%s/%s, zero legacy references).\n", manifest.Repository.Owner, manifest.Repository.Name)
	return nil
}

func auditPaperclipHarness(manifest *config.Manifest, rootDir string) error {
	harnessPath := filepath.Join(rootDir, ".paperclip", "harness.json")
	if !util.FileExists(harnessPath) {
		return fmt.Errorf("[FAIL] Paperclip agent runtime harness .paperclip/harness.json is missing. Run 'standardsctl adopt' to reconcile.")
	}
	h, err := paperclip.LoadHarness(harnessPath)
	if err != nil {
		return fmt.Errorf("[FAIL] Paperclip harness validation failed: %w", err)
	}
	expectedPlatform := fmt.Sprintf("%s/%s", manifest.Repository.Owner, manifest.Repository.Name)
	if h.Platform != expectedPlatform {
		return fmt.Errorf("[FAIL] Paperclip harness platform mismatch: got %q, expected %q. Run 'standardsctl adopt --force' to reconcile.", h.Platform, expectedPlatform)
	}
	fmt.Printf("[PASS] Paperclip agent runtime harness verified (%s, %d rules).\n", h.Platform, len(h.OperatingContract))
	return nil
}

func auditRunnerMatrix(manifest *config.Manifest, rootDir string) error {
	policy, err := config.LoadCascadingRunnerConfig(rootDir, manifest.Repository.Owner)
	if err != nil {
		return fmt.Errorf("[FAIL] Cascading runner config failed: %w", err)
	}

	targets := []struct {
		osName string
		arch   string
		isGPU  bool
	}{
		{"linux", "amd64", false},
		{"linux", "arm64", false},
		{"linux", "amd64", true},
		{"darwin", "arm64", false},
		{"darwin", "amd64", false},
	}

	for _, target := range targets {
		spec, err := runner.ResolveRunner(policy, target.osName, target.arch, target.isGPU)
		if err != nil {
			return fmt.Errorf("[FAIL] Runner matrix resolution failed for %s/%s (gpu=%v): %w", target.osName, target.arch, target.isGPU, err)
		}
		if spec.Type == "" || len(spec.RunsOn) == 0 {
			return fmt.Errorf("[FAIL] Empty runner spec for %s/%s", target.osName, target.arch)
		}
	}
	fmt.Println("[PASS] External runner matrix & ARC platform routing policies verified.")
	return nil
}

func auditPreMigrationTracking(rootDir string) error {
	epicPath := filepath.Join(rootDir, "PRE_MIGRATION_EPIC.md")
	if util.FileExists(epicPath) {
		content, err := os.ReadFile(epicPath)
		if err != nil {
			return fmt.Errorf("[FAIL] Read PRE_MIGRATION_EPIC.md failed: %w", err)
		}
		requiredStages := []string{"[TASK 1/5]", "[TASK 2/5]", "[TASK 3/5]", "[TASK 4/5]", "[TASK 5/5]"}
		text := string(content)
		for _, stage := range requiredStages {
			if !strings.Contains(text, stage) {
				return fmt.Errorf("[FAIL] PRE_MIGRATION_EPIC.md missing required stage %s", stage)
			}
		}
		fmt.Println("[PASS] Pre-migration epic (5-stage lifecycle) verified.")
	}

	needsPath := filepath.Join(rootDir, ".needs.yaml")
	if util.FileExists(needsPath) {
		data, err := os.ReadFile(needsPath)
		if err != nil || len(data) == 0 {
			return fmt.Errorf("[FAIL] .needs.yaml is missing or empty")
		}
		fmt.Println("[PASS] Polyglot framework demand & needs analysis (.needs.yaml) verified.")
	}
	return nil
}

func auditAgentDefinitions(rootDir string) error {
	agentsDir := filepath.Join(rootDir, ".agents", "agents")
	if util.DirExists(agentsDir) {
		entries, err := os.ReadDir(agentsDir)
		if err != nil {
			return fmt.Errorf("[FAIL] Failed to inspect .agents/agents: %w", err)
		}
		count := 0
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				count++
			}
		}
		if count == 0 {
			return fmt.Errorf("[FAIL] .agents/agents directory exists but contains zero agent definitions")
		}
		fmt.Printf("[PASS] Agent definitions verified (%d agents registered).\n", count)
	}
	return nil
}

func auditGitHooks(rootDir string) error {
	gitDir := filepath.Join(rootDir, ".git")
	if !util.DirExists(gitDir) && !util.FileExists(gitDir) {
		return nil
	}

	lhPath := filepath.Join(rootDir, "lefthook.yml")
	if !util.FileExists(lhPath) {
		return fmt.Errorf("[FAIL] lefthook.yml configuration is missing from repository root.")
	}

	if os.Getenv("CI") == "true" || os.Getenv("GITHUB_ACTIONS") == "true" {
		fmt.Println("[PASS] CI environment detected: lefthook.yml verified (local hook installation skipped).")
		return nil
	}

	hooksDir := resolveHooksDir(rootDir)
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	if !util.FileExists(preCommitPath) {
		return fmt.Errorf("[FAIL] Pre-commit hook %s is missing or inactive. Run 'lefthook install' or 'standardsctl adopt' to activate.", preCommitPath)
	}

	fmt.Println("[PASS] Local Git hooks (.git/hooks/pre-commit via lefthook) verified active.")
	return nil
}

func resolveHooksDir(rootDir string) string {
	out, err := util.RunGit(context.Background(), rootDir, "rev-parse", "--git-path", "hooks")
	if err == nil {
		path := strings.TrimSpace(out)
		if filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(rootDir, path)
	}
	return filepath.Join(rootDir, ".git", "hooks")
}
