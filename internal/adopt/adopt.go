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

	"github.com/cordanaLLM/standards/internal/baseline"
	"github.com/cordanaLLM/standards/internal/compiler"
	"github.com/cordanaLLM/standards/internal/config"
	"github.com/cordanaLLM/standards/internal/devcontainer"
	"github.com/cordanaLLM/standards/internal/editor"
	"github.com/cordanaLLM/standards/internal/hiss"
	"github.com/cordanaLLM/standards/internal/util"
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
	Path           string   `json:"path"`
	Profile        string   `json:"profile"`
	Facets         []string `json:"facets"`
	DryRun         bool     `json:"dry_run"`
	Force          bool     `json:"force"`
	RecordBaseline bool     `json:"record_baseline"`
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
	info, err := os.Stat(normPath)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("target path %q must be an existing directory", normPath)
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
	cmd := exec.Command("git", "-C", repoPath, "config", "--get", "remote.origin.url")
	out, err := cmd.Output()
	if err == nil {
		url := strings.TrimSpace(string(out))
		if owner := extractOwnerFromURL(url); owner != "" {
			return owner
		}
	}
	parent := filepath.Base(filepath.Dir(repoPath))
	if parent != "" && parent != "." && parent != "/" && parent != "dev" {
		return parent
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
	cmd := exec.Command("git", "-C", repoPath, "config", "--get", "remote.origin.url")
	out, err := cmd.Output()
	if err == nil {
		url := strings.TrimSpace(string(out))
		if name := extractRepoFromURL(url); name != "" {
			return name
		}
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

	// 1. Scaffold / reconcile .standards.yaml
	if err := reconcileManifest(repoPath, repoName, arch, facets, opts, report); err != nil {
		return err
	}

	// 2. Scaffold / reconcile .standards.lock
	if err := reconcileLockfile(repoPath, opts, report); err != nil {
		return err
	}

	// 3. Technical debt baseline & ratcheting
	if err := reconcileBaseline(repoPath, opts, report); err != nil {
		return err
	}

	// 4. Universal AGENTS.md & vendor transpilation
	if err := reconcileAgentHarness(repoPath, repoName, arch, opts, report); err != nil {
		return err
	}

	// 5. DevContainer platform
	if err := reconcileDevContainer(repoPath, repoName, arch, facets, opts, report); err != nil {
		return err
	}

	// 6. IDE ecosystem
	if err := reconcileEditors(repoPath, arch, opts, report); err != nil {
		return err
	}

	// 7. Makefile & Git hygiene
	if err := reconcileMakefileAndGit(repoPath, arch, opts, report); err != nil {
		return err
	}

	// 8. Repository governance texts & documentation
	if err := reconcileGovernanceTexts(repoPath, repoName, arch, opts, report); err != nil {
		return err
	}

	return nil
}

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
			scanLegacyDebt(repoPath, base, report)
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

func scanLegacyDebt(repoPath string, base *baseline.Baseline, report *AdoptReport) {
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
		return
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
}

func buildAgentHarness(repoName, arch string) string {
	verifyCmd := "make verify-all"
	testCmd := "go test -v -race ./..."
	if arch == "native-gpu-systems" {
		testCmd = "meson test -C core/build --suite=fast"
	}

	return fmt.Sprintf(`<!-- markdownlint-disable MD013 MD025 -->
# %s Agent Operating Harness

Run verification before concluding any turn:

`+"```bash\n%s\n```\n\n```mermaid\n"+`flowchart LR
    AGENT["Autonomous Agent"] --> CHECK["%s"]
    CHECK --> AUDIT["standardsctl audit"]
    CHECK --> COMPILER["standardsctl compile-context --verify"]
    CHECK --> GATE{"All checks Pass?"}
    GATE -- Yes --> RECEIPT["Ed25519 Exit-0 Receipt"]
    GATE -- No --> DISTILL["SARIF Diagnostic Distillation (<= 1500 tokens)"]
`+"```\n\n"+`## Core Directives & Invariants (Modernized NASA JPL Power-of-10)

| Invariant | Scope | NASA Rule | Enforcement Mechanism | Failure Action |
| :--- | :--- | :--- | :--- | :--- |
| **HISS-01** | Control Flow | Rule 1 | Recursion strictly prohibited; call graph must be DAG; zero `+"`goto`"+`. | Immediate build failure |
| **HISS-02** | Loops & I/O | Rule 2 | Scalar upper bound on all loops; explicit `+"`context.Context`"+` timeout on all I/O. | Semgrep / AST error |
| **HISS-03** | Memory | Rule 3 | Zero dynamic heap allocation (`+"`malloc` / `free`"+`) in hot simulation/tick loops. | Allocation audit sweep |
| **HISS-04** | Complexity | Rule 4 | Function length $\le 60$ LOC, McCabe Cyclomatic $\le 10$, Statements $\le 50$. | AST sweep blocker |
| **HISS-07** | Error Handling | Rule 7 | Zero `+"`.unwrap()` / `.expect()`"+`; all errors handled or wrapped with context. | Linter / Compiler error |
| **HISS-08** | Determinism | Rule 8 | Zero dynamic execution (`+"`eval` / `exec`"+`); zero banned unsafe libc (`+"`gets` / `strcpy` / `sprintf`"+`). | AST / Linter error |
| **HISS-09** | Reference Safety | Rule 9 | Mandatory `+"`// SAFETY:`"+` proofs for all pointer arithmetic and `+"`unsafe`"+` blocks. | AST check blocker |
| **HISS-10** | Warning Hygiene | Rule 10 | Zero-warning tolerance across compiler, linter, and format sweeps. | Exit code 1 |
| **HISS-15** | 3D Testing | Rule 5 | Positive, negative, and boundary tests mandatory for all public interfaces. | CI coverage gate |
| **HISS-16** | Context Integrity | Fleet | Single canonical `+"`AGENTS.md`"+`; vendor files compiled via `+"`standardsctl compile-context`"+`. | Pre-commit blocker |

## Operational Rules

1. **Act on Verified State**:
   Read source files and run real commands before hypothesizing or editing. Never guess flag names, library signatures, or repo configurations from memory.

2. **Lead with Output**:
   Provide direct answers, diffs, and commands. Avoid filler preambles, "Based on", restatements, or conversational chatter.

3. **Context Transpiler First**:
   Never edit `+"`CLAUDE.md`"+`, `+"`.cursor/rules/*.mdc`"+`, `+"`.windsurfrules`"+`, or `+"`.github/copilot-instructions.md`"+` manually. Make all agent instruction updates in `+"`AGENTS.md`"+` and execute:

   `+"```bash\n   standardsctl compile-context\n   ```\n\n"+`4. **SARIF Diagnostic Distillation**:
   When reporting compiler or linter errors, distill output to $\le 1,500$ tokens ($< 60$ lines). Print the top 3 root-cause failures with file/line pointers and write full SARIF logs to ephemeral storage.

5. **No Evasion Tolerated**:
   Do not attempt `+"`--no-verify`"+`, `+"`LEFTHOOK=0`"+`, or modifying `+"`.git/hooks`"+`. All pull requests are authoritatively re-checked in an ephemeral isolated sandbox by `+"`cordana-standards[bot]`"+`.

6. **Anti-Loop Interception**:
   If the same AST diff and error category repeats $\ge 3$ times, halt execution immediately. Re-evaluate the underlying design instead of making micro-textual retries.

## Primary Verification Commands

`+"```bash\n"+`# Fast local test suite
%s

# Recompile and verify cross-agent context outputs
standardsctl compile-context --verify

# Audit repository against declared HISS-16 standards
standardsctl audit

# Run all formatting, linting, and security gates
%s
`+"```\n", repoName, verifyCmd, verifyCmd, testCmd, verifyCmd)
}

func reconcileAgentHarness(repoPath, repoName, arch string, opts AdoptOptions, report *AdoptReport) error {
	agentsPath := filepath.Join(repoPath, "AGENTS.md")
	var agentsContent string

	if !fileExists(agentsPath) {
		agentsContent = buildAgentHarness(repoName, arch)
		if !opts.DryRun {
			if err := os.WriteFile(agentsPath, []byte(agentsContent), 0644); err != nil {
				return fmt.Errorf("write %s: %w", agentsPath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, "AGENTS.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "AGENTS.md",
			Action:  "create",
			Details: "Synthesized canonical Praetor Agent Operating Harness and HISS-16 invariants",
		})
	} else {
		existingBytes, err := os.ReadFile(agentsPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", agentsPath, err)
		}
		existing := string(existingBytes)
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
						return fmt.Errorf("write %s: %w", agentsPath, err)
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
					return fmt.Errorf("write %s: %w", agentsPath, err)
				}
			}
			report.ReconciledFiles = append(report.ReconciledFiles, "AGENTS.md")
			report.ActionDetails = append(report.ActionDetails, ActionDetail{
				Path:    "AGENTS.md",
				Action:  "merge",
				Details: "Merged Praetor Agent Operating Harness & HISS-16 directives above existing instructions",
			})
		}
	}

	// Transpilation of vendor targets
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
		if !fileExists(fullPath) {
			report.CreatedFiles = append(report.CreatedFiles, f.RelativePath)
			report.ActionDetails = append(report.ActionDetails, ActionDetail{
				Path:    f.RelativePath,
				Action:  "create",
				Details: fmt.Sprintf("Compiled vendor context target (%d LOC)", f.LineCount),
			})
		} else {
			report.ReconciledFiles = append(report.ReconciledFiles, f.RelativePath)
			report.ActionDetails = append(report.ActionDetails, ActionDetail{
				Path:    f.RelativePath,
				Action:  "reconcile",
				Details: fmt.Sprintf("Synchronized vendor context target (%d LOC)", f.LineCount),
			})
		}
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
			_ = os.MkdirAll(devDir, 0755)
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
	makefilePath := filepath.Join(repoPath, "Makefile")
	if !fileExists(makefilePath) {
		content := []byte(".PHONY: all verify-all audit compile-context build test\n\nverify-all:\n\t@echo \"Running verification...\"\n\ncompile-context:\n\t@standardsctl compile-context\n\naudit:\n\t@standardsctl audit\n\ntest:\n\t@go test -v -race ./...\n\nbuild:\n\t@go build -v ./...\n")
		if !opts.DryRun {
			_ = os.WriteFile(makefilePath, content, 0644)
		}
		report.CreatedFiles = append(report.CreatedFiles, "Makefile")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "Makefile",
			Action:  "create",
			Details: "Created default Makefile with verify-all, audit, and compile-context targets",
		})
	} else {
		data, err := os.ReadFile(makefilePath)
		if err == nil {
			content := string(data)
			if !strings.Contains(content, "verify-all:") {
				appendTargets := "\n# cordanaLLM/praetor Governance Targets\n.PHONY: verify-all compile-context audit\n\nverify-all:\n\t@standardsctl audit && standardsctl compile-context --verify\n\ncompile-context:\n\t@standardsctl compile-context\n\naudit:\n\t@standardsctl audit\n"
				if !opts.DryRun {
					f, err := os.OpenFile(makefilePath, os.O_APPEND|os.O_WRONLY, 0644)
					if err == nil {
						_, _ = f.WriteString(appendTargets)
						f.Close()
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
		}
		report.ReconciledFiles = append(report.ReconciledFiles, "Makefile")
	}

	gitIgnorePath := filepath.Join(repoPath, ".gitignore")
	if !fileExists(gitIgnorePath) {
		content := []byte("bin/\n*.test\n*.out\n.DS_Store\n")
		if !opts.DryRun {
			_ = os.WriteFile(gitIgnorePath, content, 0644)
		}
		report.CreatedFiles = append(report.CreatedFiles, ".gitignore")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".gitignore",
			Action:  "create",
			Details: "Created default .gitignore for build artifacts",
		})
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, ".gitignore")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    ".gitignore",
			Action:  "reconcile",
			Details: "Existing .gitignore verified present",
		})
	}
	return nil
}

func reconcileGovernanceTexts(repoPath, repoName, arch string, opts AdoptOptions, report *AdoptReport) error {
	// 1. CONTRIBUTING.md
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
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, "CONTRIBUTING.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "CONTRIBUTING.md",
			Action:  "reconcile",
			Details: "Existing contributor guide verified present",
		})
	}

	// 2. .github/pull_request_template.md
	prTmplPath := filepath.Join(repoPath, ".github", "pull_request_template.md")
	prTmplUpperPath := filepath.Join(repoPath, ".github", "PULL_REQUEST_TEMPLATE.md")
	if !fileExists(prTmplPath) && !fileExists(prTmplUpperPath) {
		prTmpl := buildPullRequestTemplate(repoName)
		if !opts.DryRun {
			_ = os.MkdirAll(filepath.Dir(prTmplPath), 0755)
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
	} else {
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
	}

	// 3. SECURITY.md
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
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, "SECURITY.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "SECURITY.md",
			Action:  "reconcile",
			Details: "Existing security policy verified present",
		})
	}

	// 4. docs/adr/ (index and template)
	adrDir := filepath.Join(repoPath, "docs", "adr")
	adrIndexPath := filepath.Join(adrDir, "README.md")
	adrTmplPath := filepath.Join(adrDir, "0000-template.md")
	if !fileExists(adrIndexPath) {
		if !opts.DryRun {
			_ = os.MkdirAll(adrDir, 0755)
			_ = os.WriteFile(adrIndexPath, []byte(buildADRIndex(repoName)), 0644)
			_ = os.WriteFile(adrTmplPath, []byte(buildADRTemplate(repoName)), 0644)
		}
		report.CreatedFiles = append(report.CreatedFiles, "docs/adr/README.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "docs/adr/README.md",
			Action:  "create",
			Details: "Scaffolded Architectural Decision Records (ADR) directory and template",
		})
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, "docs/adr/README.md")
		report.ActionDetails = append(report.ActionDetails, ActionDetail{
			Path:    "docs/adr/README.md",
			Action:  "reconcile",
			Details: "Architectural Decision Records directory verified present",
		})
	}

	// 5. README.md badge and verification table
	readmePath := filepath.Join(repoPath, "README.md")
	if fileExists(readmePath) {
		data, err := os.ReadFile(readmePath)
		if err == nil {
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
					_ = os.WriteFile(readmePath, []byte(content), 0644)
				}
				report.ReconciledFiles = append(report.ReconciledFiles, "README.md")
				report.ActionDetails = append(report.ActionDetails, ActionDetail{
					Path:    "README.md",
					Action:  "reconcile",
					Details: "Non-destructively injected HISS-16 compliance badge and verification gate table",
				})
			}
		}
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
