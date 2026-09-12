package adopt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/paperclip"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// Invariant bounds.
const (
	maxFilesScan      = 10000
	maxInfractionsCap = 10000
	defaultTimeout    = 30 * time.Second
	defaultMaxFuncLOC = 60
)

// RepositoryState describes the adoption state of a target codebase.
type RepositoryState string

const (
	StateGreenfield RepositoryState = "greenfield"
	StatePartial    RepositoryState = "partial"
	StateBrownfield RepositoryState = "brownfield"
)

// AdoptOptions controls repository adoption and template compliance.
type AdoptOptions struct {
	Path              string   `json:"path"`
	Profile           string   `json:"profile"`
	Facets            []string `json:"facets"`
	DryRun            bool     `json:"dry_run"`
	Force             bool     `json:"force"`
	RecordBaseline    bool     `json:"record_baseline"`
	SkipGitValidation bool     `json:"skip_git_validation"`
}

// ActionDetail describes a specific planned or executed action on a target file.
type ActionDetail struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // "create", "reconcile", "merge", "append"
	Details string `json:"details"`
}

// AdoptReport details the actions executed or simulated during adoption.
type AdoptReport struct {
	State           RepositoryState `json:"state"`
	Archetype       string          `json:"archetype"`
	Facets          []string        `json:"facets"`
	CreatedFiles    []string        `json:"created_files"`
	ReconciledFiles []string        `json:"reconciled_files"`
	ActionDetails   []ActionDetail  `json:"action_details,omitempty"`
	DebtBreakdown   map[string]int  `json:"debt_breakdown,omitempty"`
	LegacyDebtCount int             `json:"legacy_debt_count"`
	DryRun          bool            `json:"dry_run"`
	Errors          []string        `json:"errors,omitempty"`
}

// Adopt brings any repository to 100% template and governance compliance.
func Adopt(ctx context.Context, opts AdoptOptions) (*AdoptReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("adopt: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("adopt cancelled: %w", err)
	}

	normPath, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve repo path %q: %w", opts.Path, err)
	}
	if !opts.SkipGitValidation {
		if err := ValidateAdoptionTarget(normPath); err != nil {
			return nil, fmt.Errorf("adoption validation failed: %w", err)
		}
	} else {
		info, err := os.Stat(normPath)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("target path %q must be an existing directory", normPath)
		}
	}

	state := DetectState(normPath)
	arch := resolveArchetype(normPath, opts.Profile)
	facets := resolveFacets(opts.Facets)

	report := &AdoptReport{
		State:           state,
		Archetype:       arch,
		Facets:          facets,
		CreatedFiles:    make([]string, 0),
		ReconciledFiles: make([]string, 0),
		ActionDetails:   make([]ActionDetail, 0),
		DebtBreakdown:   make(map[string]int),
		DryRun:          opts.DryRun,
		Errors:          make([]string, 0),
	}

	if err := executeAdoptSteps(ctx, normPath, arch, facets, opts, report); err != nil {
		return nil, err
	}

	return report, nil
}

// DetectState determines if a repository is greenfield, partial, or brownfield.
func DetectState(repoPath string) RepositoryState {
	manifestExists := fileExists(filepath.Join(repoPath, ".standards.yaml"))
	lockExists := fileExists(filepath.Join(repoPath, ".standards.lock"))
	agentsExists := fileExists(filepath.Join(repoPath, "AGENTS.md"))
	baselineExists := fileExists(filepath.Join(repoPath, ".standards-baseline.json"))

	if !manifestExists && !lockExists && !agentsExists {
		return StateGreenfield
	}
	if manifestExists && lockExists && agentsExists && baselineExists {
		return StateBrownfield
	}
	return StatePartial
}

func resolveArchetype(repoPath, explicitProfile string) string {
	if explicitProfile != "" {
		return explicitProfile
	}
	if fileExists(filepath.Join(repoPath, "meson.build")) ||
		fileExists(filepath.Join(repoPath, "core", "meson.build")) ||
		fileExists(filepath.Join(repoPath, "libvmaf", "meson.build")) ||
		fileExists(filepath.Join(repoPath, "CMakeLists.txt")) {
		return "native-gpu-systems"
	}
	if fileExists(filepath.Join(repoPath, "go.mod")) {
		return "framework"
	}
	if fileExists(filepath.Join(repoPath, "Cargo.toml")) {
		return "native-gpu-systems"
	}
	if fileExists(filepath.Join(repoPath, "pubspec.yaml")) {
		return "app-service"
	}
	if fileExists(filepath.Join(repoPath, "pom.xml")) ||
		fileExists(filepath.Join(repoPath, "build.gradle")) ||
		fileExists(filepath.Join(repoPath, "build.gradle.kts")) {
		return "app-service"
	}
	if fileExists(filepath.Join(repoPath, "package.json")) {
		return "app-service"
	}
	if fileExists(filepath.Join(repoPath, "pyproject.toml")) {
		return "app-service"
	}
	if fileExists(filepath.Join(repoPath, "Dockerfile")) {
		return "container-image"
	}
	return "template-seed"
}

func resolveOwner(repoPath string) string {
	owner, _, _ := util.ResolveRepoIdentity(context.Background(), repoPath)
	if owner != "" {
		return owner
	}
	return "cordanaLLM"
}

func cleanGitURL(url string) string {
	return util.CleanGitURL(url)
}

func extractOwnerFromURL(url string) string {
	owner, _ := util.ExtractOwnerAndRepo(url)
	return owner
}

func resolveRepoName(repoPath string) string {
	_, repo, _ := util.ResolveRepoIdentity(context.Background(), repoPath)
	if repo != "" {
		return repo
	}
	return filepath.Base(repoPath)
}

func extractRepoFromURL(url string) string {
	_, repo := util.ExtractOwnerAndRepo(url)
	return repo
}

func resolveFacets(input []string) []string {
	if len(input) > 0 {
		return input
	}
	return []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed"}
}

func executeAdoptSteps(ctx context.Context, repoPath, arch string, facets []string, opts AdoptOptions, report *AdoptReport) error {
	repoName := resolveRepoName(repoPath)
	if err := executeCoreAdoptSteps(ctx, repoPath, repoName, arch, facets, opts, report); err != nil {
		return err
	}
	return executeGovernanceAndHookSteps(ctx, repoPath, repoName, arch, opts, report)
}

func executeCoreAdoptSteps(ctx context.Context, repoPath, repoName, arch string, facets []string, opts AdoptOptions, report *AdoptReport) error {
	if err := reconcileManifest(repoPath, repoName, arch, facets, opts, report); err != nil {
		return err
	}
	if err := reconcileLockfile(repoPath, opts, report); err != nil {
		return err
	}
	if err := reconcileBaseline(repoPath, opts, report); err != nil {
		return err
	}
	if err := reconcileAgentHarness(repoPath, repoName, arch, opts, report); err != nil {
		return err
	}
	if err := reconcileDevContainer(repoPath, repoName, arch, facets, opts, report); err != nil {
		return err
	}
	return reconcileEditors(repoPath, arch, opts, report)
}

func executeGovernanceAndHookSteps(ctx context.Context, repoPath, repoName, arch string, opts AdoptOptions, report *AdoptReport) error {
	if err := reconcileMakefileAndGit(repoPath, arch, opts, report); err != nil {
		return err
	}
	if err := reconcileGovernanceTexts(repoPath, repoName, arch, opts, report); err != nil {
		return err
	}
	if err := reconcileBranchRulesetsAndLabels(repoPath, opts, report); err != nil {
		return err
	}
	if err := reconcilePaperclip(repoPath, opts, report); err != nil {
		return err
	}
	if err := reconcileAgentDefinitions(repoPath, opts, report); err != nil {
		return err
	}
	if err := reconcileWorkingDirAndFlavor(ctx, repoPath, opts, report); err != nil {
		return err
	}
	return reconcileGitHooks(repoPath, opts, report)
}

func reconcileWorkingDirAndFlavor(ctx context.Context, repoPath string, opts AdoptOptions, report *AdoptReport) error {
	if !opts.DryRun {
		if err := state.InitWorkingDir(repoPath); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("workingdir init: %v", err))
		}
		detectedFlv := flavor.DetectFlavor(repoPath)
		if _, err := flavor.ApplyFlavor(ctx, repoPath, detectedFlv, false); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("apply flavor %s: %v", detectedFlv, err))
		}
	}
	report.ActionDetails = append(report.ActionDetails, ActionDetail{
		Path:    ".workingdir",
		Action:  "create",
		Details: "Initialized canonical session state ledger and bug/question journals",
	})
	return nil
}

func reconcileBranchRulesetsAndLabels(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	rulesetPath := filepath.Join(repoPath, ".github", "rulesets", "main.json")
	if opts.Force || !fileExists(rulesetPath) {
		report.CreatedFiles = append(report.CreatedFiles, ".github/rulesets/main.json")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".github/rulesets/main.json",
			Action:  "create",
			Details: "Scaffolded declarative branch protection ruleset",
		})
		if !opts.DryRun {
			if err := os.MkdirAll(filepath.Dir(rulesetPath), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(rulesetPath, []byte(buildRulesetJSON()), 0644); err != nil {
				return err
			}
		}
	}

	labelsPath := filepath.Join(repoPath, ".config", "labels.yaml")
	if opts.Force || !fileExists(labelsPath) {
		report.CreatedFiles = append(report.CreatedFiles, ".config/labels.yaml")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".config/labels.yaml",
			Action:  "create",
			Details: "Scaffolded repository label taxonomy",
		})
		if !opts.DryRun {
			if err := os.MkdirAll(filepath.Dir(labelsPath), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(labelsPath, []byte(buildDefaultLabelsYAML()), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildRulesetJSON() string {
	return `{
  "name": "praetor-main-protection",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": {
      "include": ["refs/heads/main", "refs/heads/lts-*"],
      "exclude": []
    }
  },
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"},
    {"type": "required_linear_history"},
    {"type": "required_signatures"},
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 1,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": true,
        "require_last_push_approval": false,
        "required_review_thread_resolution": true
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [
          {"context": "verify"},
          {"context": "Standards & Invariant Verification Gate"},
          {"context": "DCO 1.1 & REUSE Compliance Gate"}
        ]
      }
    }
  ]
}`
}

func buildDefaultLabelsYAML() string {
	return `version: 1
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
`
}

func reconcilePaperclip(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	paperclipDir := filepath.Join(repoPath, ".paperclip")
	harnessPath := filepath.Join(paperclipDir, "harness.json")
	if opts.Force || !fileExists(harnessPath) {
		report.CreatedFiles = append(report.CreatedFiles, ".paperclip/harness.json")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".paperclip/harness.json",
			Action:  "create",
			Details: "Scaffolded Paperclip agent runtime harness and AGit rules",
		})
		if !opts.DryRun {
			harness, err := paperclip.SynthesizeHarness(repoPath)
			if err != nil {
				return err
			}
			if err := paperclip.WriteHarness(harness, repoPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func reconcileAgentDefinitions(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	agentsDir := filepath.Join(repoPath, ".agents", "agents")
	auditorPath := filepath.Join(agentsDir, "repo-auditor.md")
	if opts.Force || !fileExists(auditorPath) {
		report.CreatedFiles = append(report.CreatedFiles, ".agents/agents/repo-auditor.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".agents/agents/repo-auditor.md",
			Action:  "create",
			Details: "Scaffolded repository auditor agent definition",
		})
		if !opts.DryRun {
			if err := os.MkdirAll(agentsDir, 0755); err != nil {
				return err
			}
			if err := os.WriteFile(auditorPath, []byte(defaultAuditorAgentMD), 0644); err != nil {
				return err
			}
		}
	}

	gatekeeperPath := filepath.Join(agentsDir, "repo-gatekeeper.md")
	if opts.Force || !fileExists(gatekeeperPath) {
		report.CreatedFiles = append(report.CreatedFiles, ".agents/agents/repo-gatekeeper.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".agents/agents/repo-gatekeeper.md",
			Action:  "create",
			Details: "Scaffolded repository gatekeeper agent definition",
		})
		if !opts.DryRun {
			if err := os.MkdirAll(agentsDir, 0755); err != nil {
				return err
			}
			if err := os.WriteFile(gatekeeperPath, []byte(defaultGatekeeperAgentMD), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

const defaultAuditorAgentMD = `---
name: repo-auditor
description: "Autonomous agent for repository HISS invariant sweeps and standardsctl compliance."
mainAgent: true
subagent: true
commandExecutionPolicy: auto
---

# Repository Governance Auditor Persona

You are the authoritative repository governance auditor. Your purpose is to run autonomous sweeps across codebases and git commits to guarantee 100% adherence to declared standards.

## Execution Command
` + "```bash\nstandardsctl audit\n```\n"

const defaultGatekeeperAgentMD = `---
name: repo-gatekeeper
description: "Autonomous subagent for dependency verification, SCA security scans, and worktree gating."
mainAgent: true
subagent: true
commandExecutionPolicy: auto
---

# Repository Gatekeeper Persona

You are the repository gatekeeper. Your mission is to strictly enforce the anti-direct-merge policy and verify all verification gates before shipping.

## Execution Command
` + "```bash\nstandardsctl gate run --target=. --dry-run\n```\n"

func reconcileGitHooks(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	lhPath := filepath.Join(repoPath, "lefthook.yml")
	if opts.Force || !fileExists(lhPath) {
		report.CreatedFiles = append(report.CreatedFiles, "lefthook.yml")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "lefthook.yml",
			Action:  "create",
			Details: "Scaffolded Lefthook configuration for local pre-commit and pre-push enforcement",
		})
		if !opts.DryRun {
			if err := os.WriteFile(lhPath, []byte(defaultLefthookYAML), 0644); err != nil {
				return err
			}
		}
	}

	evasionHookPath := filepath.Join(repoPath, ".config", "agent", "hooks", "block_evasion.py")
	if opts.Force || !fileExists(evasionHookPath) {
		report.CreatedFiles = append(report.CreatedFiles, ".config/agent/hooks/block_evasion.py")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".config/agent/hooks/block_evasion.py",
			Action:  "create",
			Details: "Scaffolded anti-evasion hook interceptor",
		})
		if !opts.DryRun {
			if err := os.MkdirAll(filepath.Dir(evasionHookPath), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(evasionHookPath, []byte(defaultBlockEvasionPY), 0755); err != nil {
				return err
			}
		}
	}

	gitDir := filepath.Join(repoPath, ".git")
	if util.DirExists(gitDir) && !opts.DryRun {
		cmd := exec.Command("lefthook", "install")
		cmd.Dir = repoPath
		if err := cmd.Run(); err != nil {
			if err := installFallbackHooks(repoPath); err != nil {
				return err
			}
		}
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".git/hooks/pre-commit",
			Action:  "create",
			Details: "Installed and activated local Git hooks",
		})
	}
	return nil
}

func installFallbackHooks(repoPath string) error {
	hooksDir := filepath.Join(repoPath, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return err
	}
	preCommitScript := `#!/usr/bin/env bash
set -e
if command -v standardsctl >/dev/null 2>&1; then
    standardsctl compile-context --verify
    standardsctl audit
elif [ -f "./bin/standardsctl" ]; then
    ./bin/standardsctl compile-context --verify
    ./bin/standardsctl audit
fi
`
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	return os.WriteFile(preCommitPath, []byte(preCommitScript), 0755)
}

const defaultLefthookYAML = `# Lefthook Configuration (Go 1.27+ & HISS-16 Governance)
pre-commit:
  parallel: true
  commands:
    gofmt:
      glob: "*.go"
      run: gofmt -w {staged_files}
      stage_fixed: true
    govet:
      glob: "*.go"
      run: go vet ./...
    context-check:
      run: which praetorctl >/dev/null 2>&1 && praetorctl compile-context --verify || ([ -d "./cmd/standardsctl" ] && go run ./cmd/standardsctl compile-context --verify || true)
    hiss-audit:
      run: which praetorctl >/dev/null 2>&1 && praetorctl audit || ([ -d "./cmd/standardsctl" ] && go run ./cmd/standardsctl audit || true)
    block-evasion:
      run: python3 .config/agent/hooks/block_evasion.py

post-commit:
  commands:
    state-sync:
      run: which praetorctl >/dev/null 2>&1 && praetorctl state sync . || ([ -d "./cmd/standardsctl" ] && go run ./cmd/standardsctl state sync . || true)
    dedupe-cadence:
      run: which praetorctl >/dev/null 2>&1 && praetorctl dedupe cadence --threshold=20 --record . || ([ -d "./cmd/standardsctl" ] && go run ./cmd/standardsctl dedupe cadence --threshold=20 --record . || true)

pre-push:
  parallel: false
  commands:
    security:
      run: which govulncheck >/dev/null 2>&1 && govulncheck ./... || true
    flavor-audit:
      run: which praetorctl >/dev/null 2>&1 && praetorctl flavor audit . || ([ -d "./cmd/standardsctl" ] && go run ./cmd/standardsctl flavor audit . || true)
    audit:
      run: which praetorctl >/dev/null 2>&1 && praetorctl audit || ([ -d "./cmd/standardsctl" ] && go run ./cmd/standardsctl audit || true)
    gate:
      run: which praetorctl >/dev/null 2>&1 && praetorctl gate run --path=. || ([ -d "./cmd/standardsctl" ] && go run ./cmd/standardsctl gate run --path=. || true)
`

const defaultBlockEvasionPY = `#!/usr/bin/env python3
import sys, os, re

BLOCKED_PATTERNS = [
    r"--no-verify\b",
    r"-n\b(?=.*git\s+commit)",
    r"LEFTHOOK=0\b",
    r"SKIP=.*git",
    r"core\.hooksPath\s*=\s*/dev/null",
    r"rm\s+(-rf?\s+)?\.git/hooks",
]

def main():
    if os.environ.get("LEFTHOOK") == "0":
        sys.stderr.write("[BLOCKED BY HISS-16] LEFTHOOK=0 detected in environment.\n")
        sys.exit(1)
    if len(sys.argv) > 1:
        cmd = " ".join(sys.argv[1:])
        for p in BLOCKED_PATTERNS:
            if re.search(p, cmd):
                sys.stderr.write(f"[BLOCKED BY HISS-16] Verification evasion prohibited: {p}\n")
                sys.exit(1)
    sys.exit(0)

if __name__ == "__main__":
    main()
`

func reconcileManifest(repoPath, repoName, arch string, facets []string, opts AdoptOptions, report *AdoptReport) error {
	manifestPath := filepath.Join(repoPath, ".standards.yaml")
	if !fileExists(manifestPath) || opts.Force {
		owner := resolveOwner(repoPath)
		manifest := config.Manifest{
			Version: 1,
			Repository: config.RepositoryMetadata{
				Owner:      owner,
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
		if !opts.DryRun {
			if err := os.WriteFile(manifestPath, data, 0644); err != nil {
				return fmt.Errorf("write %s: %w", manifestPath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, ".standards.yaml")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".standards.yaml",
			Action:  "create",
			Details: fmt.Sprintf("Scaffolded standards manifest (Owner: %s, Profile: %s)", owner, arch),
		})
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, ".standards.yaml")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".standards.yaml",
			Action:  "reconcile",
			Details: "Existing standards manifest verified present",
		})
	}
	return nil
}

func reconcileLockfile(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	lockPath := filepath.Join(repoPath, ".standards.lock")
	if !fileExists(lockPath) || opts.Force {
		content := []byte("# SemVer lockfile\nversion: 1\npinned_version: \"v1.0.0\"\n")
		if !opts.DryRun {
			if err := os.WriteFile(lockPath, content, 0644); err != nil {
				return fmt.Errorf("write %s: %w", lockPath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, ".standards.lock")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".standards.lock",
			Action:  "create",
			Details: "Pinned SemVer lockfile to v1.0.0",
		})
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, ".standards.lock")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".standards.lock",
			Action:  "reconcile",
			Details: "SemVer lockfile verified present",
		})
	}
	return nil
}

func reconcileBaseline(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	baselinePath := filepath.Join(repoPath, ".standards-baseline.json")
	if !fileExists(baselinePath) || opts.RecordBaseline {
		base := &baseline.Baseline{
			Version:          1,
			TotalInfractions: 0,
			Infractions:      make([]baseline.Infraction, 0),
		}
		if opts.RecordBaseline {
			if err := scanLegacyDebt(repoPath, base, report); err != nil {
				return err
			}
		}
		report.LegacyDebtCount = base.TotalInfractions
		if !opts.DryRun {
			if err := baseline.SaveBaseline(baselinePath, base); err != nil {
				return fmt.Errorf("save baseline: %w", err)
			}
		}
		if !fileExists(baselinePath) {
			report.CreatedFiles = append(report.CreatedFiles, ".standards-baseline.json")
			report.ActionDetails = append(report.ActionDetails, ActionDetail{
				Path:    ".standards-baseline.json",
				Action:  "create",
				Details: fmt.Sprintf("Recorded %d legacy debt infractions into baseline", base.TotalInfractions),
			})
		} else {
			report.ReconciledFiles = append(report.ReconciledFiles, ".standards-baseline.json")
			report.ActionDetails = append(report.ActionDetails, ActionDetail{
				Path:    ".standards-baseline.json",
				Action:  "reconcile",
				Details: fmt.Sprintf("Rescanned and recorded %d legacy debt infractions into baseline", base.TotalInfractions),
			})
		}
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, ".standards-baseline.json")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".standards-baseline.json",
			Action:  "reconcile",
			Details: "Technical debt baseline verified present",
		})
	}
	return nil
}

// scanLegacyDebt records the repository's current infractions into base. A failed or
// capped scan is an error: a baseline recorded from a partial scan would silently
// exempt every unscanned violation from the debt ratchet (HISS-07, HISS-13).
func scanLegacyDebt(repoPath string, base *baseline.Baseline, report *AdoptReport) error {
	if report.DebtBreakdown == nil {
		report.DebtBreakdown = make(map[string]int)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	scanRep, err := hiss.Scan(ctx, repoPath, hiss.ScanOptions{
		MaxFuncLOC: defaultMaxFuncLOC,
		Cap:        maxInfractionsCap,
	})
	if err != nil {
		return fmt.Errorf("scan legacy debt: %w", err)
	}
	if scanRep.Truncated {
		return fmt.Errorf("scan legacy debt: %w", hiss.ErrScanTruncated)
	}

	for _, v := range scanRep.Violations {
		base.Infractions = append(base.Infractions, baseline.Infraction{
			RuleID:      v.RuleID,
			FilePath:    v.FilePath,
			LineNumber:  v.LineNumber,
			Symbol:      v.Symbol,
			Message:     v.Message,
			Fingerprint: fmt.Sprintf("%s:%d:%s", v.FilePath, v.LineNumber, v.RuleID),
		})
	}
	base.TotalInfractions = len(base.Infractions)
	for k, count := range scanRep.Breakdown {
		report.DebtBreakdown[k] = count
	}
	return nil
}

const agentHarnessTemplate = `<!-- markdownlint-disable MD013 MD025 -->
# {{ .RepoName }} Agent Operating Harness

Run verification before concluding any turn:

` + "```bash\n{{ .VerifyCmd }}\n```\n\n```mermaid\n" + `flowchart LR
    AGENT["Autonomous Agent"] --> CHECK["{{ .VerifyCmd }}"]
    CHECK --> AUDIT["standardsctl audit"]
    CHECK --> COMPILER["standardsctl compile-context --verify"]
    CHECK --> GATE{"All checks Pass?"}
    GATE -- Yes --> RECEIPT["Ed25519 Exit-0 Receipt"]
    GATE -- No --> DISTILL["SARIF Diagnostic Distillation (<= 1500 tokens)"]
` + "```\n\n"

const agentHarnessFooterTemplate = `## Primary Verification Commands

` + "```bash\n" + `# Fast local test suite
{{ .TestCmd }}

# Recompile and verify cross-agent context outputs
standardsctl compile-context --verify

# Audit repository against declared HISS-16 standards
standardsctl audit

# Run all formatting, linting, and security gates
{{ .VerifyCmd }}
` + "```\n"

func buildAgentHarness(repoName, arch string) string {
	verifyCmd := "make verify-all"
	testCmd := "go test -v -race ./..."
	if arch == "native-gpu-systems" {
		testCmd = "meson test -C core/build --suite=fast"
	}

	tCtx := TemplateContext{
		RepoName:  repoName,
		Archetype: arch,
		VerifyCmd: verifyCmd,
		TestCmd:   testCmd,
	}

	header, err := RenderTemplate("harness_header", agentHarnessTemplate, tCtx)
	if err != nil {
		header = fmt.Sprintf("# %s Agent Operating Harness\n", repoName)
	}

	footer, err := RenderTemplate("harness_footer", agentHarnessFooterTemplate, tCtx)
	if err != nil {
		footer = "## Primary Verification Commands\n"
	}

	return header + buildAgentHarnessDirectives() + footer
}

func buildAgentHarnessDirectives() string {
	return `## Core Directives & Invariants (Modernized NASA JPL Power-of-10)

| Invariant | Scope | NASA Rule | Enforcement Mechanism | Failure Action |
| :--- | :--- | :--- | :--- | :--- |
| **HISS-01** | Control Flow | Rule 1 | Recursion strictly prohibited; call graph must be DAG; zero ` + "`goto`" + `. | Immediate build failure |
| **HISS-02** | Loops & I/O | Rule 2 | Scalar upper bound on all loops; explicit ` + "`context.Context`" + ` timeout on all I/O. | Semgrep / AST error |
| **HISS-03** | Memory | Rule 3 | Zero dynamic heap allocation (` + "`malloc` / `free`" + `) in hot simulation/tick loops. | Allocation audit sweep |
| **HISS-04** | Complexity | Rule 4 | Function length $\le 60$ LOC, McCabe Cyclomatic $\le 10$, Statements $\le 50$. | AST sweep blocker |
| **HISS-07** | Error Handling | Rule 7 | Zero ` + "`.unwrap()` / `.expect()`" + `; all errors handled or wrapped with context. | Linter / Compiler error |
| **HISS-08** | Determinism | Rule 8 | Zero dynamic execution (` + "`eval` / `exec`" + `); zero banned unsafe libc (` + "`gets` / `strcpy` / `sprintf`" + `). | AST / Linter error |
| **HISS-09** | Reference Safety | Rule 9 | Mandatory ` + "`// SAFETY:`" + ` proofs for all pointer arithmetic and ` + "`unsafe`" + ` blocks. | AST check blocker |
| **HISS-10** | Warning Hygiene | Rule 10 | Zero-warning tolerance across compiler, linter, and format sweeps. | Exit code 1 |
| **HISS-15** | 3D Testing | Rule 5 | Positive, negative, and boundary tests mandatory for all public interfaces. | CI coverage gate |
| **HISS-16** | Context Integrity | Fleet | Single canonical ` + "`AGENTS.md`" + `; vendor files compiled via ` + "`standardsctl compile-context`" + `. | Pre-commit blocker |

## Operational Rules

1. **Act on Verified State**:
   Read source files and run real commands before hypothesizing or editing. Never guess flag names, library signatures, or repo configurations from memory.

2. **Lead with Output**:
   Provide direct answers, diffs, and commands. Avoid filler preambles, "Based on", restatements, or conversational chatter.

3. **Context Transpiler First**:
   Never edit ` + "`CLAUDE.md`" + `, ` + "`.cursor/rules/*.mdc`" + `, ` + "`.windsurfrules`" + `, or ` + "`.github/copilot-instructions.md`" + ` manually. Make all agent instruction updates in ` + "`AGENTS.md`" + ` and execute:

   ` + "```bash\n   standardsctl compile-context\n   ```\n\n" + `4. **SARIF Diagnostic Distillation**:
   When reporting compiler or linter errors, distill output to $\le 1,500$ tokens ($< 60$ lines). Print the top 3 root-cause failures with file/line pointers and write full SARIF logs to ephemeral storage.

5. **No Evasion Tolerated**:
   Do not attempt ` + "`--no-verify`" + `, ` + "`LEFTHOOK=0`" + `, or modifying ` + "`.git/hooks`" + `. All pull requests are authoritatively re-checked in an ephemeral isolated sandbox by ` + "`cordana-standards[bot]`" + `.

6. **Anti-Loop Interception**:
   If the same AST diff and error category repeats $\ge 3$ times, halt execution immediately. Re-evaluate the underlying design instead of making micro-textual retries.

`
}

func reconcileAgentHarness(repoPath, repoName, arch string, opts AdoptOptions, report *AdoptReport) error {
	agentsContent, err := resolveAgentsContent(repoPath, repoName, arch, opts, report)
	if err != nil {
		return err
	}
	return transpileAgentTargets(repoPath, agentsContent, opts, report)
}

func resolveAgentsContent(repoPath, repoName, arch string, opts AdoptOptions, report *AdoptReport) (string, error) {
	agentsPath := filepath.Join(repoPath, "AGENTS.md")
	if !fileExists(agentsPath) {
		agentsContent := buildAgentHarness(repoName, arch)
		if !opts.DryRun {
			if err := os.WriteFile(agentsPath, []byte(agentsContent), 0644); err != nil {
				return "", fmt.Errorf("write %s: %w", agentsPath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, "AGENTS.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "AGENTS.md",
			Action:  "create",
			Details: "Synthesized canonical Praetor Agent Operating Harness and HISS-16 invariants",
		})
		return agentsContent, nil
	}

	existingBytes, err := os.ReadFile(agentsPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", agentsPath, err)
	}
	return mergeExistingAgentsContent(agentsPath, string(existingBytes), repoName, arch, opts, report)
}

func mergeExistingAgentsContent(agentsPath, existing, repoName, arch string, opts AdoptOptions, report *AdoptReport) (string, error) {
	var agentsContent string
	if strings.Contains(existing, "Agent Operating Harness") || strings.Contains(existing, "## Core Directives & Invariants") {
		if opts.Force {
			harness := strings.TrimSpace(buildAgentHarness(repoName, arch))
			parts := strings.SplitN(existing, "\n---\n", 2)
			if len(parts) > 1 {
				agentsContent = harness + "\n\n---\n\n" + strings.TrimSpace(parts[1]) + "\n"
			} else {
				agentsContent = harness + "\n"
			}
			if !opts.DryRun {
				if err := os.WriteFile(agentsPath, []byte(agentsContent), 0644); err != nil {
					return "", fmt.Errorf("write %s: %w", agentsPath, err)
				}
			}
			report.ReconciledFiles = append(report.ReconciledFiles, "AGENTS.md")
			report.ActionDetails = append(report.ActionDetails, ActionDetail{
				Path:    "AGENTS.md",
				Action:  "reconcile",
				Details: "Updated Praetor Agent Operating Harness while preserving repository-specific instructions",
			})
		} else {
			agentsContent = existing
			report.ReconciledFiles = append(report.ReconciledFiles, "AGENTS.md")
			report.ActionDetails = append(report.ActionDetails, ActionDetail{
				Path:    "AGENTS.md",
				Action:  "reconcile",
				Details: "Existing Praetor Agent Operating Harness verified in sync",
			})
		}
	} else {
		harness := buildAgentHarness(repoName, arch)
		agentsContent = harness + "\n---\n\n" + existing
		if !opts.DryRun {
			if err := os.WriteFile(agentsPath, []byte(agentsContent), 0644); err != nil {
				return "", fmt.Errorf("write %s: %w", agentsPath, err)
			}
		}
		report.ReconciledFiles = append(report.ReconciledFiles, "AGENTS.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "AGENTS.md",
			Action:  "merge",
			Details: "Merged Praetor Agent Operating Harness & HISS-16 directives above existing instructions",
		})
	}
	return agentsContent, nil
}

func transpileAgentTargets(repoPath, agentsContent string, opts AdoptOptions, report *AdoptReport) error {
	tr := compiler.NewTranspiler()
	res, err := tr.CompileContent(agentsContent)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("context compilation: %v", err))
		return nil
	}

	if !opts.DryRun {
		if err := tr.WriteOutputs(res, repoPath); err != nil {
			return fmt.Errorf("write transpiler outputs: %w", err)
		}
	}

	for _, f := range res.Files {
		fullPath := filepath.Join(repoPath, f.RelativePath)
		action := "reconcile"
		detail := fmt.Sprintf("Synchronized vendor context target (%d LOC)", f.LineCount)
		if !fileExists(fullPath) {
			action = "create"
			detail = fmt.Sprintf("Compiled vendor context target (%d LOC)", f.LineCount)
			report.CreatedFiles = append(report.CreatedFiles, f.RelativePath)
		} else {
			report.ReconciledFiles = append(report.ReconciledFiles, f.RelativePath)
		}
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    f.RelativePath,
			Action:  action,
			Details: detail,
		})
	}
	return nil
}

func reconcileDevContainer(repoPath, repoName, arch string, facets []string, opts AdoptOptions, report *AdoptReport) error {
	devDir := filepath.Join(repoPath, ".devcontainer")
	jsonPath := filepath.Join(devDir, "devcontainer.json")

	if !fileExists(jsonPath) || opts.Force {
		dc, err := devcontainer.SynthesizeFromProfiles(repoName, []string{arch}, facets)
		if err != nil {
			return fmt.Errorf("synthesize devcontainer: %w", err)
		}
		data, err := json.MarshalIndent(dc, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal devcontainer: %w", err)
		}
		if !opts.DryRun {
			if err := os.MkdirAll(devDir, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", devDir, err)
			}
			if err := os.WriteFile(jsonPath, append(data, '\n'), 0644); err != nil {
				return fmt.Errorf("write %s: %w", jsonPath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, ".devcontainer/devcontainer.json")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".devcontainer/devcontainer.json",
			Action:  "create",
			Details: fmt.Sprintf("Synthesized DevContainer for archetype '%s'", arch),
		})
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, ".devcontainer/devcontainer.json")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".devcontainer/devcontainer.json",
			Action:  "reconcile",
			Details: "DevContainer configuration verified present",
		})
	}
	return nil
}

func reconcileEditors(repoPath, arch string, opts AdoptOptions, report *AdoptReport) error {
	edOpts := editor.DefaultOptions()
	edOpts.WorkspaceRoot = repoPath
	edOpts.Archetype = arch
	set, err := editor.Synthesize(edOpts)
	if err != nil {
		return fmt.Errorf("synthesize editors: %w", err)
	}

	if !opts.DryRun {
		if err := editor.Write(set, repoPath); err != nil {
			return fmt.Errorf("write editors: %w", err)
		}
	}

	for _, f := range set.Files {
		fullPath := filepath.Join(repoPath, f.Path)
		if !fileExists(fullPath) {
			report.CreatedFiles = append(report.CreatedFiles, f.Path)
			report.ActionDetails = append(report.ActionDetails, ActionDetail{
				Path:    f.Path,
				Action:  "create",
				Details: fmt.Sprintf("Synthesized %s IDE configuration for archetype '%s'", f.Editor, arch),
			})
		} else {
			report.ReconciledFiles = append(report.ReconciledFiles, f.Path)
			report.ActionDetails = append(report.ActionDetails, ActionDetail{
				Path:    f.Path,
				Action:  "reconcile",
				Details: fmt.Sprintf("Reconciled %s IDE configuration for archetype '%s'", f.Editor, arch),
			})
		}
	}
	return nil
}

func reconcileMakefileAndGit(repoPath, arch string, opts AdoptOptions, report *AdoptReport) error {
	if err := reconcileMakefile(repoPath, opts, report); err != nil {
		return err
	}
	return reconcileGitIgnore(repoPath, opts, report)
}

func reconcileMakefile(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	makefilePath := filepath.Join(repoPath, "Makefile")
	if !fileExists(makefilePath) {
		content := []byte(".PHONY: all verify-all audit compile-context build test\n\nverify-all:\n\t@echo \"Running verification...\"\n\ncompile-context:\n\t@standardsctl compile-context\n\naudit:\n\t@standardsctl audit\n\ntest:\n\t@go test -v -race ./...\n\nbuild:\n\t@go build -v ./...\n")
		if !opts.DryRun {
			if err := os.WriteFile(makefilePath, content, 0644); err != nil {
				return fmt.Errorf("write %s: %w", makefilePath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, "Makefile")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "Makefile",
			Action:  "create",
			Details: "Created default Makefile with verify-all, audit, and compile-context targets",
		})
		return nil
	}

	data, err := os.ReadFile(makefilePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", makefilePath, err)
	}
	content := string(data)
	if !strings.Contains(content, "verify-all:") {
		appendTargets := "\n# cordanaLLM/praetor Governance Targets\n.PHONY: verify-all compile-context audit\n\nverify-all:\n\t@standardsctl audit && standardsctl compile-context --verify\n\ncompile-context:\n\t@standardsctl compile-context\n\naudit:\n\t@standardsctl audit\n"
		if !opts.DryRun {
			f, err := os.OpenFile(makefilePath, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("open %s: %w", makefilePath, err)
			}
			defer f.Close()
			if _, err := f.WriteString(appendTargets); err != nil {
				return fmt.Errorf("append %s: %w", makefilePath, err)
			}
		}
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "Makefile",
			Action:  "append",
			Details: "Appended governance targets: verify-all, compile-context, and audit",
		})
	} else {
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "Makefile",
			Action:  "reconcile",
			Details: "Existing Makefile already contains verify-all target",
		})
	}
	report.ReconciledFiles = append(report.ReconciledFiles, "Makefile")
	return nil
}

func reconcileGitIgnore(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	gitIgnorePath := filepath.Join(repoPath, ".gitignore")
	if !fileExists(gitIgnorePath) {
		content := []byte("bin/\n*.test\n*.out\n.DS_Store\n")
		if !opts.DryRun {
			if err := os.WriteFile(gitIgnorePath, content, 0644); err != nil {
				return fmt.Errorf("write %s: %w", gitIgnorePath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, ".gitignore")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".gitignore",
			Action:  "create",
			Details: "Created default .gitignore for build artifacts",
		})
		return nil
	}

	report.ReconciledFiles = append(report.ReconciledFiles, ".gitignore")
	report.ActionDetails = append(report.ActionDetails, ActionDetail{
		Path:    ".gitignore",
		Action:  "reconcile",
		Details: "Existing .gitignore verified present",
	})
	return nil
}

func reconcileGovernanceTexts(repoPath, repoName, arch string, opts AdoptOptions, report *AdoptReport) error {
	if err := reconcileContributing(repoPath, repoName, opts, report); err != nil {
		return err
	}
	if err := reconcilePullRequestTemplate(repoPath, repoName, opts, report); err != nil {
		return err
	}
	if err := reconcileSecurityPolicy(repoPath, repoName, opts, report); err != nil {
		return err
	}
	if err := reconcileADR(repoPath, repoName, opts, report); err != nil {
		return err
	}
	return reconcileReadme(repoPath, opts, report)
}

func reconcileContributing(repoPath, repoName string, opts AdoptOptions, report *AdoptReport) error {
	contribPath := filepath.Join(repoPath, "CONTRIBUTING.md")
	if !fileExists(contribPath) {
		contrib := buildContributingGuide(repoName)
		if !opts.DryRun {
			if err := os.WriteFile(contribPath, []byte(contrib), 0644); err != nil {
				return fmt.Errorf("write %s: %w", contribPath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, "CONTRIBUTING.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "CONTRIBUTING.md",
			Action:  "create",
			Details: "Scaffolded contributor governance guide with HISS-16 & NASA rules",
		})
		return nil
	}
	report.ReconciledFiles = append(report.ReconciledFiles, "CONTRIBUTING.md")
	report.ActionDetails = append(report.ActionDetails, ActionDetail{
		Path:    "CONTRIBUTING.md",
		Action:  "reconcile",
		Details: "Existing contributor guide verified present",
	})
	return nil
}

func reconcilePullRequestTemplate(repoPath, repoName string, opts AdoptOptions, report *AdoptReport) error {
	prTmplPath := filepath.Join(repoPath, ".github", "pull_request_template.md")
	prTmplUpperPath := filepath.Join(repoPath, ".github", "PULL_REQUEST_TEMPLATE.md")
	if !fileExists(prTmplPath) && !fileExists(prTmplUpperPath) {
		prTmpl := buildPullRequestTemplate(repoName)
		if !opts.DryRun {
			if err := os.MkdirAll(filepath.Dir(prTmplPath), 0755); err != nil {
				return fmt.Errorf("mkdir .github: %w", err)
			}
			if err := os.WriteFile(prTmplPath, []byte(prTmpl), 0644); err != nil {
				return fmt.Errorf("write %s: %w", prTmplPath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, ".github/pull_request_template.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".github/pull_request_template.md",
			Action:  "create",
			Details: "Scaffolded pull request template with HISS verification checklist",
		})
		return nil
	}
	targetName := ".github/pull_request_template.md"
	if fileExists(prTmplUpperPath) {
		targetName = ".github/PULL_REQUEST_TEMPLATE.md"
	}
	report.ReconciledFiles = append(report.ReconciledFiles, targetName)
	report.ActionDetails = append(report.ActionDetails, ActionDetail{
		Path:    targetName,
		Action:  "reconcile",
		Details: "Existing pull request template verified present",
	})
	return nil
}

func reconcileSecurityPolicy(repoPath, repoName string, opts AdoptOptions, report *AdoptReport) error {
	secPath := filepath.Join(repoPath, "SECURITY.md")
	if !fileExists(secPath) {
		sec := buildSecurityPolicy(repoName)
		if !opts.DryRun {
			if err := os.WriteFile(secPath, []byte(sec), 0644); err != nil {
				return fmt.Errorf("write %s: %w", secPath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, "SECURITY.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "SECURITY.md",
			Action:  "create",
			Details: "Scaffolded security policy and vulnerability disclosure standards",
		})
		return nil
	}
	report.ReconciledFiles = append(report.ReconciledFiles, "SECURITY.md")
	report.ActionDetails = append(report.ActionDetails, ActionDetail{
		Path:    "SECURITY.md",
		Action:  "reconcile",
		Details: "Existing security policy verified present",
	})
	return nil
}

func reconcileADR(repoPath, repoName string, opts AdoptOptions, report *AdoptReport) error {
	adrDir := filepath.Join(repoPath, "docs", "adr")
	adrIndexPath := filepath.Join(adrDir, "README.md")
	adrTmplPath := filepath.Join(adrDir, "0000-template.md")
	if !fileExists(adrIndexPath) {
		if !opts.DryRun {
			if err := os.MkdirAll(adrDir, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", adrDir, err)
			}
			if err := os.WriteFile(adrIndexPath, []byte(buildADRIndex(repoName)), 0644); err != nil {
				return fmt.Errorf("write %s: %w", adrIndexPath, err)
			}
			if err := os.WriteFile(adrTmplPath, []byte(buildADRTemplate(repoName)), 0644); err != nil {
				return fmt.Errorf("write %s: %w", adrTmplPath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, "docs/adr/README.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "docs/adr/README.md",
			Action:  "create",
			Details: "Scaffolded Architectural Decision Records (ADR) directory and template",
		})
		return nil
	}
	report.ReconciledFiles = append(report.ReconciledFiles, "docs/adr/README.md")
	report.ActionDetails = append(report.ActionDetails, ActionDetail{
		Path:    "docs/adr/README.md",
		Action:  "reconcile",
		Details: "Architectural Decision Records directory verified present",
	})
	return nil
}

func reconcileReadme(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	readmePath := filepath.Join(repoPath, "README.md")
	if !fileExists(readmePath) {
		return nil
	}
	data, err := os.ReadFile(readmePath)
	if err != nil {
		return nil
	}
	content := string(data)
	modified := false
	if !strings.Contains(content, "HISS--16%20Compliant") && !strings.Contains(content, "HISS-16") {
		badge := "[![HISS-16 Compliant](https://img.shields.io/badge/Standards-HISS--16%20Compliant-brightgreen)](AGENTS.md)\n"
		trimmed := strings.TrimSpace(content)
		if strings.HasPrefix(trimmed, "# ") {
			nlIdx := strings.Index(content, "\n")
			if nlIdx != -1 {
				content = content[:nlIdx+1] + "\n" + badge + content[nlIdx+1:]
			} else {
				content = content + "\n\n" + badge
			}
		} else {
			content = badge + "\n" + content
		}
		modified = true
	}
	if !strings.Contains(content, "Standards & Governance") && !strings.Contains(content, "make verify-all") {
		table := "\n\n## Standards & Governance\n\nThis repository conforms to High-Integrity Systems Standards (HISS-16)\nand modernized NASA JPL Power-of-10 rules.\n\n| Gate | Command | Description |\n| :--- | :--- | :--- |\n| **Verification** | `make verify-all` | Runs full audit, test suite, and context integrity check |\n| **HISS Audit** | `standardsctl audit` | Enforces zero technical debt regression against baseline |\n| **Context Sync** | `standardsctl compile-context` | Transpiles canonical `AGENTS.md` to all AI targets |\n"
		content = strings.TrimRight(content, "\r\n") + table
		modified = true
	}
	if modified {
		if !opts.DryRun {
			if err := os.WriteFile(readmePath, []byte(content), 0644); err != nil {
				return fmt.Errorf("write %s: %w", readmePath, err)
			}
		}
		report.ReconciledFiles = append(report.ReconciledFiles, "README.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "README.md",
			Action:  "reconcile",
			Details: "Non-destructively injected HISS-16 compliance badge and verification gate table",
		})
	}
	return nil
}

func buildContributingGuide(repoName string) string {
	return fmt.Sprintf(`<!-- markdownlint-disable MD013 -->
# Contributing to %s

Thank you for contributing! This repository adheres strictly to the **High-Integrity Systems Standards (HISS-16)** and modernized **NASA JPL Power-of-10** rules.

## Core Directives & Verification

All changes must pass local verification before submitting:

`+"```bash\nmake verify-all\n```\n\n"+`### Modernized NASA JPL Power-of-10 Rules

1. **Simple Control Flow (HISS-01)**: Recursion is strictly banned; call graph must be an acyclic DAG; zero `+"`goto`"+`.
2. **Bounded Loops (HISS-02)**: All loops must have a statically verifiable scalar upper bound. Network and disk I/O require `+"`context.Context`"+` timeout.
3. **Deterministic Memory (HISS-03)**: Zero dynamic heap allocations (`+"`malloc` / `free`"+`) in hot simulation or rendering loops.
4. **Function Length Cap (HISS-04)**: No function may exceed **60 lines of code** ($\le 60$ LOC).
5. **Assertion Density (HISS-15)**: Functions must assert preconditions, state invariants, and postconditions.
6. **Data Scope**: Variables must be declared at the smallest possible scope.
7. **Checked Errors (HISS-07)**: Check return values of all non-void functions; zero `+"`.unwrap()`"+` or unchecked errors.
8. **Static Execution (HISS-08)**: Dynamic code evaluation (`+"`eval` / `exec`"+`) and banned unsafe libc calls (`+"`gets` / `strcpy` / `sprintf`"+`) are prohibited.
9. **Pointer Safety (HISS-09)**: Pointer arithmetic must be bounded; all `+"`unsafe`"+` blocks require `+"`// SAFETY:`"+` justifications.
10. **Zero-Warning Hygiene (HISS-10)**: Zero compiler, linter, or formatting warnings tolerated across all builds.

### 3D Testing Discipline (HISS-15)

Every public function requires:

- **Positive tests**: Expected valid operational inputs.
- **Negative tests**: Invalid inputs, expected error returns.
- **Boundary tests**: Zero, one, max limits, off-by-one bounds.

### Commit Messages

We enforce Conventional Commits:

- `+"`feat:`"+` New features
- `+"`fix:`"+` Bug fixes
- `+"`chore:`"+` Maintenance and governance
- `+"`feat!:` / `fix!:`"+` Breaking API changes (must include `+"`Migration:`"+` footer)
`, repoName)
}

func buildPullRequestTemplate(repoName string) string {
	return `<!-- markdownlint-disable MD013 -->
## Description

<!-- Provide a concise summary of the changes and the architectural rationale. -->

## Pre-Merge Verification Checklist

- [ ] Local verification passed: ` + "`make verify-all`" + `
- [ ] No new HISS-16 / NASA Power-of-10 infractions (all new/modified functions $\le 60$ LOC)
- [ ] 3D Tests included (Positive, Negative, Boundary) for public APIs
- [ ] Agent contexts in sync: ` + "`standardsctl compile-context --verify`" + `
- [ ] Commit messages adhere to Conventional Commits format
`
}

func buildSecurityPolicy(repoName string) string {
	return `<!-- markdownlint-disable MD013 -->
# Security Policy

## Supported Versions

Only the latest release and current default branch receive security updates.

## Reporting a Vulnerability

Please report security vulnerabilities privately to the maintainers rather than opening a public issue.
Reports are investigated promptly under responsible disclosure guidelines.
`
}

func buildADRIndex(repoName string) string {
	return `<!-- markdownlint-disable MD013 -->
# Architectural Decision Records (ADRs)

This directory documents key architectural decisions following the HISS-14 immutable numbering lattice.

| Number | Date | Title | Status |
| :--- | :--- | :--- | :--- |
| [0000](0000-template.md) | 2026-09-11 | ADR Architecture Decision Template | Accepted |
`
}

func buildADRTemplate(repoName string) string {
	return `<!-- markdownlint-disable MD013 -->
# ADR-0000: Title of Decision

- **Status**: Proposed | Accepted | Deprecated | Superseded
- **Date**: YYYY-MM-DD
- **Authors**: Team

## Context

Describe the context, problem statement, and forces at play.

## Decision

Describe the decision taken and the architectural rationale.

## Consequences

- **Positive**: Benefits and capabilities gained.
- **Negative**: Trade-offs, migration burden, or constraints imposed.
`
}

func fileExists(path string) bool {
	return util.PathExists(path)
}
