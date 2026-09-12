package cifilter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Invariant bounds.
const (
	maxFilesToInspect = 5000
	defaultGitTimeout = 15 * time.Second
)

// FilterOptions configures diff analysis and change detection.
type FilterOptions struct {
	RepoDir  string `json:"repo_dir"`
	BaseRef  string `json:"base_ref"`
	HeadRef  string `json:"head_ref"`
	ForceAll bool   `json:"force_all"`
}

// ChangeSet summarizes categorized file modifications.
type ChangeSet struct {
	TotalFiles    int      `json:"total_files"`
	Files         []string `json:"files"`
	CodeChanged   bool     `json:"code_changed"`
	TestsChanged  bool     `json:"tests_changed"`
	DocsChanged   bool     `json:"docs_changed"`
	ConfigChanged bool     `json:"config_changed"`
	AgentChanged  bool     `json:"agent_changed"`
	StateOnly     bool     `json:"state_only"`
	DocsOnly      bool     `json:"docs_only"`
}

// FilterDecision details the execution plan for CI checks and test suites.
type FilterDecision struct {
	RunTests       bool       `json:"run_tests"`
	RunLinters     bool       `json:"run_linters"`
	RunSecurity    bool       `json:"run_security"`
	RunAudit       bool       `json:"run_audit"`
	RunContextSync bool       `json:"run_context_sync"`
	RunDocsOnly    bool       `json:"run_docs_only"`
	SkipHeavyGates bool       `json:"skip_heavy_gates"`
	Reason         string     `json:"reason"`
	ChangeSet      *ChangeSet `json:"change_set"`
}

// AnalyzeChanges inspects git diff and computes the optimal CI execution decision.
func AnalyzeChanges(ctx context.Context, opts FilterOptions) (*FilterDecision, error) {
	if ctx == nil {
		return nil, errors.New("analyze changes requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, defaultGitTimeout)
	defer cancel()

	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = "."
	}

	files, err := GetChangedFiles(ctx, repoDir, opts.BaseRef, opts.HeadRef)
	if err != nil {
		// Fallback safely to executing full test battery on diff error
		cs := &ChangeSet{CodeChanged: true, ConfigChanged: true}
		return &FilterDecision{
			RunTests:       true,
			RunLinters:     true,
			RunSecurity:    true,
			RunAudit:       true,
			RunContextSync: true,
			SkipHeavyGates: false,
			Reason:         fmt.Sprintf("git diff unavailable (%v); running full verification", err),
			ChangeSet:      cs,
		}, nil
	}

	cs := ClassifyChanges(files)
	decision := MakeDecision(cs, opts.ForceAll)
	return decision, nil
}

// GetChangedFiles extracts modified files between two git refs.
func GetChangedFiles(ctx context.Context, dir, baseRef, headRef string) ([]string, error) {
	if baseRef == "" {
		baseRef = "origin/main"
	}
	if headRef == "" {
		headRef = "HEAD"
	}

	// Try three-dot merge-base diff first
	diffArg := fmt.Sprintf("%s...%s", baseRef, headRef)
	out, err := util.RunGit(ctx, dir, "diff", "--name-only", diffArg)
	if err != nil || out == "" {
		// Fallback to two-dot direct diff
		out, err = util.RunGit(ctx, dir, "diff", "--name-only", baseRef, headRef)
		if err != nil {
			// Fallback to diff against HEAD~1 or uncommitted changes
			out, err = util.RunGit(ctx, dir, "diff", "--name-only", "HEAD")
			if err != nil {
				return nil, fmt.Errorf("git diff failed: %w", err)
			}
		}
	}

	var files []string
	rawLines := strings.Split(out, "\n")
	limit := len(rawLines)
	if limit > maxFilesToInspect {
		return nil, fmt.Errorf("git diff exceeds %d file entries", maxFilesToInspect)
	}

	for i := 0; i < limit; i++ {
		trimmed := strings.TrimSpace(rawLines[i])
		if trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, nil
}

// ClassifyChanges categorizes a list of file paths into distinct domains.
func ClassifyChanges(files []string) *ChangeSet {
	cs := &ChangeSet{
		TotalFiles: len(files),
		Files:      files,
	}

	if len(files) == 0 {
		return cs
	}
	if len(files) > maxFilesToInspect {
		cs.CodeChanged, cs.ConfigChanged = true, true
		return cs
	}

	nonStateCount := 0
	nonDocsCount := 0

	for i := 0; i < len(files) && i < maxFilesToInspect; i++ {
		p := filepath.ToSlash(strings.TrimSpace(files[i]))
		if p == "" {
			continue
		}

		if isState(p) {
			continue
		}
		nonStateCount++

		if isDocumentation(p) {
			cs.DocsChanged = true
			continue
		}
		nonDocsCount++

		cs.classifySource(p)
	}

	cs.StateOnly = nonStateCount == 0 && len(files) > 0
	cs.DocsOnly = nonDocsCount == 0 && cs.DocsChanged
	return cs
}

func (cs *ChangeSet) classifySource(path string) {
	cs.TestsChanged = cs.TestsChanged || isTest(path)
	cs.CodeChanged = cs.CodeChanged || isCode(path)
	cs.ConfigChanged = cs.ConfigChanged || isConfig(path)
	cs.AgentChanged = cs.AgentChanged || isAgent(path)
}

// MakeDecision maps categorized changes to CI execution decisions.
func MakeDecision(cs *ChangeSet, forceAll bool) *FilterDecision {
	if cs == nil || cs.TotalFiles == 0 {
		return makeFullMatrixDecision(cs, "no file diff detected; running full verification matrix")
	}
	if forceAll {
		return makeFullMatrixDecision(cs, "force execution flag set; running full verification matrix")
	}
	if cs.StateOnly {
		return &FilterDecision{
			Reason:         "only .workingdir session state modified; skipping CI gates",
			SkipHeavyGates: true,
			ChangeSet:      cs,
		}
	}
	if cs.DocsOnly {
		return &FilterDecision{
			RunAudit:       true,
			RunDocsOnly:    true,
			SkipHeavyGates: true,
			Reason:         "pure documentation change; running audit only and skipping heavy race tests",
			ChangeSet:      cs,
		}
	}
	return makeTargetedDecision(cs)
}

func makeFullMatrixDecision(cs *ChangeSet, reason string) *FilterDecision {
	return &FilterDecision{
		RunTests:       true,
		RunLinters:     true,
		RunSecurity:    true,
		RunAudit:       true,
		RunContextSync: true,
		SkipHeavyGates: false,
		Reason:         reason,
		ChangeSet:      cs,
	}
}

func makeTargetedDecision(cs *ChangeSet) *FilterDecision {
	needsTests := cs.CodeChanged || cs.ConfigChanged || cs.TestsChanged
	needsLinters := cs.CodeChanged || cs.ConfigChanged
	needsSecurity := cs.CodeChanged || cs.ConfigChanged
	needsContextSync := cs.AgentChanged || cs.ConfigChanged

	return &FilterDecision{
		RunTests:       needsTests,
		RunLinters:     needsLinters,
		RunSecurity:    needsSecurity,
		RunAudit:       true,
		RunContextSync: needsContextSync,
		RunDocsOnly:    false,
		SkipHeavyGates: !needsTests,
		Reason:         "source code or configuration modified; running targeted CI matrix",
		ChangeSet:      cs,
	}
}

// ToEnvMap converts the decision to standard key-value environment variables.
func (d *FilterDecision) ToEnvMap() map[string]string {
	return map[string]string{
		"RUN_TESTS":        fmt.Sprintf("%t", d.RunTests),
		"RUN_LINTERS":      fmt.Sprintf("%t", d.RunLinters),
		"RUN_SECURITY":     fmt.Sprintf("%t", d.RunSecurity),
		"RUN_AUDIT":        fmt.Sprintf("%t", d.RunAudit),
		"RUN_CONTEXT_SYNC": fmt.Sprintf("%t", d.RunContextSync),
		"DOCS_ONLY":        fmt.Sprintf("%t", d.RunDocsOnly),
		"SKIP_HEAVY_GATES": fmt.Sprintf("%t", d.SkipHeavyGates),
	}
}

// FormatGitHubOutput outputs the decision in GitHub Actions $GITHUB_OUTPUT format.
func (d *FilterDecision) FormatGitHubOutput() string {
	var sb strings.Builder
	for k, v := range d.ToEnvMap() {
		sb.WriteString(strings.ToLower(k) + "=" + v + "\n")
	}
	return sb.String()
}

// ToJSON serializes the decision structure into indented JSON.
func (d *FilterDecision) ToJSON() ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}

func isDocumentation(p string) bool {
	base := filepath.Base(p)
	return strings.HasSuffix(p, ".md") ||
		strings.HasPrefix(p, "docs/") ||
		strings.HasSuffix(p, ".png") ||
		strings.HasSuffix(p, ".svg") ||
		strings.HasSuffix(p, ".jpg") ||
		strings.EqualFold(base, "LICENSE") ||
		strings.EqualFold(base, "NOTICE") ||
		strings.HasSuffix(p, ".txt")
}

func isCode(p string) bool {
	return slices.Contains([]string{
		".go", ".c", ".cpp", ".h", ".cu", ".rs", ".ts", ".js", ".py", ".java", ".dart", ".proto",
	}, filepath.Ext(p))
}

func isTest(p string) bool {
	return strings.HasSuffix(p, "_test.go") ||
		strings.Contains(p, "_test.") ||
		strings.Contains(p, ".test.") ||
		strings.Contains(p, ".spec.") ||
		strings.HasPrefix(p, "test/") ||
		strings.HasPrefix(p, "tests/")
}

func isConfig(p string) bool {
	base := filepath.Base(p)
	return strings.HasPrefix(p, ".github/") ||
		strings.HasSuffix(p, ".yaml") ||
		strings.HasSuffix(p, ".yml") ||
		slices.Contains([]string{
			"makefile", "lefthook.yml", "go.mod", "go.sum", "cargo.toml", "cargo.lock", "package.json", "pnpm-lock.yaml", "pom.xml",
		}, strings.ToLower(base))
}

func isAgent(p string) bool {
	base := filepath.Base(p)
	return strings.EqualFold(base, "AGENTS.md") ||
		strings.EqualFold(base, "CLAUDE.md") ||
		strings.HasPrefix(p, ".paperclip/") ||
		strings.HasPrefix(p, ".agents/")
}

func isState(p string) bool {
	return strings.HasPrefix(p, ".workingdir/")
}
