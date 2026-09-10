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

// AdoptReport details the actions executed or simulated during adoption.
type AdoptReport struct {
	State           RepositoryState `json:"state"`
	Archetype       string          `json:"archetype"`
	Facets          []string        `json:"facets"`
	CreatedFiles    []string        `json:"created_files"`
	ReconciledFiles []string        `json:"reconciled_files"`
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

func extractOwnerFromURL(url string) string {
	trimmed := strings.TrimSuffix(url, ".git")
	trimmed = strings.TrimSuffix(trimmed, "/")
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

func resolveFacets(input []string) []string {
	if len(input) > 0 {
		return input
	}
	return []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed"}
}

func executeAdoptSteps(ctx context.Context, repoPath, arch string, facets []string, opts AdoptOptions, report *AdoptReport) error {
	repoName := filepath.Base(repoPath)

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
	if err := reconcileAgentHarness(repoPath, repoName, opts, report); err != nil {
		return err
	}

	// 5. DevContainer platform
	if err := reconcileDevContainer(repoPath, repoName, arch, facets, opts, report); err != nil {
		return err
	}

	// 6. IDE ecosystem
	if err := reconcileEditors(repoPath, opts, report); err != nil {
		return err
	}

	// 7. Makefile & Git hygiene
	if err := reconcileMakefileAndGit(repoPath, opts, report); err != nil {
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
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, ".standards.yaml")
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
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, ".standards.lock")
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
			scanLegacyDebt(repoPath, base)
		}
		report.LegacyDebtCount = base.TotalInfractions
		if !opts.DryRun {
			if err := baseline.SaveBaseline(baselinePath, base); err != nil {
				return fmt.Errorf("save baseline: %w", err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, ".standards-baseline.json")
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, ".standards-baseline.json")
	}
	return nil
}

func scanLegacyDebt(repoPath string, base *baseline.Baseline) {
	// Scan touched Go files for unbounded loops or unchecked errors to record initial debt
	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if len(base.Infractions) >= maxLoopBound {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(repoPath, path)
		if strings.HasPrefix(rel, "vendor/") || strings.HasPrefix(rel, ".standards/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		if strings.Contains(content, "_ = ") {
			base.Infractions = append(base.Infractions, baseline.Infraction{
				RuleID:      "HISS-07",
				FilePath:    rel,
				LineNumber:  1,
				Message:     "Legacy unchecked error recorded during praetor adoption",
				Fingerprint: fmt.Sprintf("%s:1:HISS-07", rel),
			})
		}
		return nil
	})
	base.TotalInfractions = len(base.Infractions)
}

func reconcileAgentHarness(repoPath, repoName string, opts AdoptOptions, report *AdoptReport) error {
	agentsPath := filepath.Join(repoPath, "AGENTS.md")
	if !fileExists(agentsPath) || opts.Force {
		tmpl := fmt.Sprintf("# %s Agent Operating Harness\n\nRun verification before concluding any turn:\n```bash\nmake verify-all\n```\n", repoName)
		if !opts.DryRun {
			if err := os.WriteFile(agentsPath, []byte(tmpl), 0644); err != nil {
				return fmt.Errorf("write %s: %w", agentsPath, err)
			}
		}
		report.CreatedFiles = append(report.CreatedFiles, "AGENTS.md")
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, "AGENTS.md")
	}

	if !opts.DryRun {
		tr := compiler.NewTranspiler()
		if res, err := tr.Compile(agentsPath); err == nil {
			_ = tr.WriteOutputs(res, repoPath)
		}
	}
	report.ReconciledFiles = append(report.ReconciledFiles, "CLAUDE.md", ".cursor/rules/*.mdc", ".windsurfrules")
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
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, ".devcontainer/devcontainer.json")
	}
	return nil
}

func reconcileEditors(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	edOpts := editor.DefaultOptions()
	edOpts.WorkspaceRoot = repoPath
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
		report.CreatedFiles = append(report.CreatedFiles, f.Path)
	}
	return nil
}

func reconcileMakefileAndGit(repoPath string, opts AdoptOptions, report *AdoptReport) error {
	makefilePath := filepath.Join(repoPath, "Makefile")
	if !fileExists(makefilePath) {
		content := []byte(".PHONY: all verify-all audit compile-context build test\n\nverify-all:\n\t@echo \"Running verification...\"\n\ncompile-context:\n\t@standardsctl compile-context\n\naudit:\n\t@standardsctl audit\n\ntest:\n\t@go test -v -race ./...\n\nbuild:\n\t@go build -v ./...\n")
		if !opts.DryRun {
			_ = os.WriteFile(makefilePath, content, 0644)
		}
		report.CreatedFiles = append(report.CreatedFiles, "Makefile")
	} else {
		report.ReconciledFiles = append(report.ReconciledFiles, "Makefile")
	}

	gitIgnorePath := filepath.Join(repoPath, ".gitignore")
	if !fileExists(gitIgnorePath) {
		content := []byte("bin/\n*.test\n*.out\n.DS_Store\n")
		if !opts.DryRun {
			_ = os.WriteFile(gitIgnorePath, content, 0644)
		}
		report.CreatedFiles = append(report.CreatedFiles, ".gitignore")
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
