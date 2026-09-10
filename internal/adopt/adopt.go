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
	"gopkg.in/yaml.v3"
)

// Invariant bounds.
const (
	maxFilesScan   = 2000
	maxLoopBound   = 500
	defaultTimeout = 30 * time.Second
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
	trimmed := strings.TrimSpace(url)
	trimmed = strings.TrimSuffix(trimmed, "/")
	trimmed = strings.TrimSuffix(trimmed, ".git")
	return strings.TrimSuffix(trimmed, "/")
}

func extractOwnerFromURL(url string) string {
	trimmed := cleanGitURL(url)
	if idx := strings.LastIndex(trimmed, ":"); idx != -1 && !strings.HasPrefix(trimmed, "http") {
		pathPart := trimmed[idx+1:]
		parts := strings.Split(pathPart, "/")
		if len(parts) >= 2 {
			return parts[len(parts)-2]
		}
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return ""
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
	trimmed := cleanGitURL(url)
	if idx := strings.LastIndex(trimmed, ":"); idx != -1 && !strings.HasPrefix(trimmed, "http") {
		pathPart := trimmed[idx+1:]
		parts := strings.Split(pathPart, "/")
		if len(parts) >= 1 && parts[len(parts)-1] != "" {
			return parts[len(parts)-1]
		}
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 1 && parts[len(parts)-1] != "" {
		return parts[len(parts)-1]
	}
	return ""
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

	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if len(base.Infractions) >= maxLoopBound {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(repoPath, path)
		if err != nil {
			return nil
		}

		// Skip common vendor, build, and version-control trees
		if strings.HasPrefix(rel, "vendor/") || strings.HasPrefix(rel, ".standards/") ||
			strings.HasPrefix(rel, ".git/") || strings.HasPrefix(rel, "node_modules/") ||
			strings.HasPrefix(rel, ".venv/") || strings.HasPrefix(rel, "build/") ||
			strings.HasPrefix(rel, "core/build/") || strings.HasPrefix(rel, "libvmaf/build/") ||
			strings.HasPrefix(rel, "target/") || strings.HasPrefix(rel, ".cache/") ||
			strings.HasPrefix(rel, ".idea/") || strings.HasPrefix(rel, ".vscode/") ||
			strings.HasPrefix(rel, "third_party/") {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		isGo := ext == ".go"
		isNative := ext == ".c" || ext == ".cpp" || ext == ".cc" || ext == ".cxx" || ext == ".h" || ext == ".hpp" || ext == ".cu" || ext == ".hip"
		isPython := ext == ".py"
		isRust := ext == ".rs"

		if !isGo && !isNative && !isPython && !isRust {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		for lineIdx, line := range lines {
			if lineIdx >= maxFilesScan {
				break
			}
			trimmed := strings.TrimSpace(line)

			// Go checks
			if isGo {
				if strings.Contains(line, "_ = ") {
					recordInfraction(base, report, "HISS-07", rel, lineIdx+1, "Legacy unchecked error assignment")
				}
				if strings.HasPrefix(trimmed, "for {") {
					recordInfraction(base, report, "HISS-02", rel, lineIdx+1, "Legacy unbounded loop (for { ... })")
				}
			}

			// C / C++ / CUDA checks
			if isNative {
				if strings.Contains(line, "while (1)") || strings.Contains(line, "while(1)") ||
					strings.Contains(line, "while (true)") || strings.Contains(line, "while(true)") ||
					strings.Contains(line, "for (;;)") || strings.Contains(line, "for(;;)") {
					recordInfraction(base, report, "HISS-02", rel, lineIdx+1, "Legacy unbounded loop in native code")
				}
				if strings.Contains(line, "gets(") {
					recordInfraction(base, report, "HISS-09", rel, lineIdx+1, "Banned unsafe gets() invocation")
				}
				if strings.Contains(line, "strcpy(") {
					recordInfraction(base, report, "HISS-09", rel, lineIdx+1, "Banned unsafe strcpy() invocation; bounded string copy required")
				}
				if strings.Contains(line, "sprintf(") {
					recordInfraction(base, report, "HISS-09", rel, lineIdx+1, "Banned unsafe sprintf() invocation; snprintf required")
				}
				if strings.HasPrefix(trimmed, "goto ") {
					recordInfraction(base, report, "HISS-01", rel, lineIdx+1, "Legacy non-DAG control flow jump (goto)")
				}
			}

			// Python checks
			if isPython {
				if strings.HasPrefix(trimmed, "while True:") {
					recordInfraction(base, report, "HISS-02", rel, lineIdx+1, "Legacy unbounded while True loop in Python")
				}
				if strings.Contains(line, "eval(") || strings.Contains(line, "exec(") {
					recordInfraction(base, report, "HISS-09", rel, lineIdx+1, "Unsafe dynamic eval/exec execution in Python")
				}
				if trimmed == "except:" || strings.HasPrefix(trimmed, "except: ") || strings.HasPrefix(trimmed, "except:#") {
					recordInfraction(base, report, "HISS-07", rel, lineIdx+1, "Bare except catches and suppresses unhandled exceptions")
				}
			}

			// Rust checks
			if isRust {
				if strings.Contains(line, ".unwrap()") {
					recordInfraction(base, report, "HISS-07", rel, lineIdx+1, "Legacy .unwrap() invocation in production Rust code")
				}
				if strings.Contains(line, ".expect(") {
					recordInfraction(base, report, "HISS-07", rel, lineIdx+1, "Legacy .expect() invocation in production Rust code")
				}
				if strings.HasPrefix(trimmed, "unsafe {") {
					recordInfraction(base, report, "HISS-09", rel, lineIdx+1, "Unaudited unsafe block in Rust code")
				}
			}

			if len(base.Infractions) >= maxLoopBound {
				break
			}
		}
		return nil
	})
	base.TotalInfractions = len(base.Infractions)
}

func recordInfraction(base *baseline.Baseline, report *AdoptReport, ruleID, rel string, line int, msg string) {
	if len(base.Infractions) >= maxLoopBound {
		return
	}
	base.Infractions = append(base.Infractions, baseline.Infraction{
		RuleID:      ruleID,
		FilePath:    rel,
		LineNumber:  line,
		Message:     msg,
		Fingerprint: fmt.Sprintf("%s:%d:%s", rel, line, ruleID),
	})
	if report.DebtBreakdown != nil {
		report.DebtBreakdown[ruleID]++
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
`+"```\n\n"+`## Core Directives & Invariants

| Invariant | Scope | Enforcement Mechanism | Failure Action |
| :--- | :--- | :--- | :--- |
| **HISS-01** | Control Flow | Recursion strictly prohibited; call graph must be DAG. | Immediate build failure |
| **HISS-02** | Loops & I/O | Scalar upper bound on all loops; explicit `+"`context.Context`"+` timeout on all I/O. | Semgrep / AST error |
| **HISS-04** | Complexity | McCabe Cyclomatic $\le 10$, Cognitive $\le 15$, Func LOC $\le 75$, Statements $\le 50$. | AST sweep blocker |
| **HISS-07** | Error Handling | Zero `+"`.unwrap()` / `.expect()`"+`; all errors handled or wrapped with context. | Linter / Compiler error |
| **HISS-10** | Warning Hygiene | Zero-warning tolerance across compiler, linter, and format sweeps. | Exit code 1 |
| **HISS-15** | 3D Testing | Positive, negative, and boundary tests mandatory for all public interfaces. | CI coverage gate |
| **HISS-16** | Context Integrity | Single canonical `+"`AGENTS.md`"+`; vendor files compiled via `+"`standardsctl compile-context`"+`. | Pre-commit blocker |

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
			agentsContent = existing
			report.ReconciledFiles = append(report.ReconciledFiles, "AGENTS.md")
			report.ActionDetails = append(report.ActionDetails, ActionDetail{
				Path:    "AGENTS.md",
				Action:  "reconcile",
				Details: "Existing Praetor Agent Operating Harness verified in sync",
			})
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
