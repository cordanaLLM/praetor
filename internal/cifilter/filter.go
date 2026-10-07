package cifilter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
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
	// ManifestPath names the .standards.yaml whose overrides.ci block governs the decision.
	// Empty, or a path with no file, keeps config.DefaultCIPolicy; a manifest that cannot be
	// read or parsed selects the full matrix.
	ManifestPath string `json:"manifest_path,omitempty"`
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
	// UnclassifiedChanged records a path whose file kind no classifier recognised, even under a
	// test directory. Such a path also sets ConfigChanged, so an unknown file kind runs tests,
	// linters and security (fail closed).
	UnclassifiedChanged bool `json:"unclassified_changed"`
	StateOnly           bool `json:"state_only"`
	DocsOnly            bool `json:"docs_only"`
}

// FilterDecision details the execution plan for CI checks and test suites.
type FilterDecision struct {
	RunTests       bool       `json:"run_tests"`
	RunLinters     bool       `json:"run_linters"`
	RunSecurity    bool       `json:"run_security"`
	RunAudit       bool       `json:"run_audit"`
	RunContextSync bool       `json:"run_context_sync"`
	RunDocs        bool       `json:"run_docs"`
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

	// Every failure below fails closed: the full matrix runs rather than a guessed subset.
	unknown := &ChangeSet{CodeChanged: true, ConfigChanged: true}
	policy, err := loadCIPolicy(opts.ManifestPath)
	if err != nil {
		return makeFullMatrixDecision(unknown, fmt.Sprintf("CI policy unavailable (%v); running full verification", err)), nil
	}
	files, err := GetChangedFiles(ctx, repoDir, opts.BaseRef, opts.HeadRef)
	if err != nil {
		return makeFullMatrixDecision(unknown, fmt.Sprintf("git diff unavailable (%v); running full verification", err)), nil
	}

	cs := ClassifyChanges(files)
	return MakePolicyDecision(cs, opts.ForceAll, policy), nil
}

// loadCIPolicy reads overrides.ci from the manifest at path through config.LoadManifest. No
// path and no file both mean the repository declares no CI policy, so the defaults apply.
func loadCIPolicy(path string) (config.CIPolicy, error) {
	if path == "" {
		return config.DefaultCIPolicy(), nil
	}
	manifest, err := config.LoadManifest(path)
	if errors.Is(err, fs.ErrNotExist) {
		return config.DefaultCIPolicy(), nil
	}
	if err != nil {
		return config.CIPolicy{}, err
	}
	return manifest.Overrides.EffectiveCI(), nil
}

// GetChangedFiles extracts modified files between two git refs. Both refs must resolve: a
// diff of the working tree against HEAD describes uncommitted edits, not the commits under
// review, so an unresolvable ref is an error rather than a fallback to it (BUG-891).
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
			return nil, fmt.Errorf("git diff %s %s failed: %w", baseRef, headRef, err)
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

		// Agent instruction files (AGENTS.md, compiled vendor files, .agents/**,
		// .paperclip/**) are classified before documentation: a docs-only decision skips
		// the HISS-16 compile-context --verify gate, and an agent-only change set must
		// never take that path even though most of those files end in .md.
		if !isAgent(p) && isDocumentation(p) {
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

// classifySource records the domains a non-documentation path belongs to. A path whose file
// kind none of them recognises (an extensionless script, a Dockerfile, a template, an unknown manifest)
// fails closed as configuration, so it selects tests, linters and security (BUG-236). A test
// location names no file kind: a script or fixture under a tests/ directory still fails closed.
func (cs *ChangeSet) classifySource(path string) {
	test, code, conf, agent := isTest(path), isCode(path), isConfig(path), isAgent(path)
	cs.TestsChanged = cs.TestsChanged || test
	cs.CodeChanged = cs.CodeChanged || code
	cs.ConfigChanged = cs.ConfigChanged || conf
	cs.AgentChanged = cs.AgentChanged || agent
	if !code && !conf && !agent {
		cs.UnclassifiedChanged = true
		cs.ConfigChanged = true
	}
}

// MakeDecision maps categorized changes to CI execution decisions under
// config.DefaultCIPolicy.
func MakeDecision(cs *ChangeSet, forceAll bool) *FilterDecision {
	return MakePolicyDecision(cs, forceAll, config.DefaultCIPolicy())
}

// MakePolicyDecision maps categorized changes to CI execution decisions under policy, the
// repository's overrides.ci block. A switch set to false only ever adds gates.
func MakePolicyDecision(cs *ChangeSet, forceAll bool, policy config.CIPolicy) *FilterDecision {
	if reason := fullMatrixReason(cs, forceAll, policy); reason != "" {
		return makeFullMatrixDecision(cs, reason)
	}
	if cs.StateOnly {
		return &FilterDecision{
			Reason:         "only private .workingdir/.workingdir2 session state modified; skipping CI gates",
			SkipHeavyGates: true,
			ChangeSet:      cs,
		}
	}
	if cs.DocsOnly {
		return &FilterDecision{
			RunAudit:       true,
			RunDocs:        true,
			RunDocsOnly:    true,
			SkipHeavyGates: true,
			Reason:         "pure documentation change; running audit and documentation governance; skipping heavy race and security gates",
			ChangeSet:      cs,
		}
	}
	return makeTargetedDecision(cs)
}

// fullMatrixReason names why no selective decision applies, or returns "" when one may.
func fullMatrixReason(cs *ChangeSet, forceAll bool, policy config.CIPolicy) string {
	switch {
	case cs == nil || cs.TotalFiles == 0:
		return "no file diff detected; running full verification matrix"
	case forceAll:
		return "force execution flag set; running full verification matrix"
	case !policy.DiffAwareFiltering:
		return "overrides.ci.diff_aware_filtering is false; running full verification matrix"
	case (cs.StateOnly || cs.DocsOnly) && !policy.SkipHeavyGatesOnDocsOrState:
		return "overrides.ci.skip_heavy_gates_on_docs_or_state is false; running full verification matrix for a docs-only or state-only change"
	}
	return ""
}

func makeFullMatrixDecision(cs *ChangeSet, reason string) *FilterDecision {
	return &FilterDecision{
		RunTests:       true,
		RunLinters:     true,
		RunSecurity:    true,
		RunAudit:       true,
		RunContextSync: true,
		RunDocs:        true,
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
	reason := "source code or configuration modified; running targeted CI matrix"
	if cs.UnclassifiedChanged {
		reason = "unclassified file kind modified; failing closed to tests, linters and security in the targeted CI matrix"
	}

	return &FilterDecision{
		RunTests:       needsTests,
		RunLinters:     needsLinters,
		RunSecurity:    needsSecurity,
		RunAudit:       true,
		RunContextSync: needsContextSync,
		RunDocs:        cs.DocsChanged || needsTests,
		RunDocsOnly:    false,
		SkipHeavyGates: !needsTests,
		Reason:         reason,
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
		"RUN_DOCS":         fmt.Sprintf("%t", d.RunDocs),
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
	if isBuildManifest(p) {
		return false
	}
	base := filepath.Base(p)
	lower := strings.ToLower(p)
	for _, suffix := range []string{
		".md", ".markdown", ".mdx", ".md.tmpl", ".markdown.tmpl", ".mdx.tmpl",
		".png", ".svg", ".jpg", ".txt",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return strings.HasPrefix(p, "docs/") || strings.EqualFold(base, "LICENSE") ||
		strings.EqualFold(base, "NOTICE")
}

// unscannedCodeExtensions are the source kinds the HISS scanner has no dispatch for; JavaScript,
// TypeScript, Svelte and shell come from the scanner's own table (hiss.SupportsExtension). Shading
// language sources are code: GLSL (.glsl and the stage suffixes glslang infers a stage from),
// HLSL, WGSL and Metal are compiled into the program, so a change to one selects tests,
// linters and security, and never context sync.
var unscannedCodeExtensions = []string{
	".java", ".dart", ".proto", ".zig",
	".glsl", ".vert", ".frag", ".comp", ".geom", ".tesc", ".tese",
	".rgen", ".rint", ".rahit", ".rchit", ".rmiss", ".rcall", ".hlsl", ".wgsl", ".metal",
}

// isCode reports a source file. Every extension the HISS scanner reads (C, C++ with its .h, .hpp
// and .hh headers, CUDA, HIP, Go, Python, Rust, JavaScript, TypeScript, Svelte, and shell's .sh
// and .bash) comes from the scanner's own table (HISS-19), so a file hiss checks for invariants
// always selects the code gates too. A file the scanner claims only from its bytes (an
// extensionless script, a systemd unit, an Ansible playbook) is not named by its path, so it
// keeps failing closed or classifying as configuration here.
func isCode(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return hiss.SupportsExtension(ext) || slices.Contains(unscannedCodeExtensions, ext)
}

// testDirs are directory names whose files are tests at any depth: test/ and tests/ at the root
// or inside a package (src/test/, a Cargo crate's tests/), and a Cargo crate's benches/.
var testDirs = []string{"test", "tests", "benches"}

func isTest(p string) bool {
	segments := strings.Split(p, "/")
	return strings.HasSuffix(p, "_test.go") ||
		strings.Contains(p, "_test.") ||
		strings.Contains(p, ".test.") ||
		strings.Contains(p, ".spec.") ||
		slices.ContainsFunc(segments[:len(segments)-1], func(dir string) bool {
			return slices.Contains(testDirs, dir)
		})
}

// buildManifestNames are the dependency and build manifests by lower-cased base name.
var buildManifestNames = []string{
	"makefile", "go.mod", "go.sum", "cargo.toml", "cargo.lock", "package.json", "package-lock.json", "pnpm-lock.yaml", "pom.xml",
	"cmakelists.txt",
}

// isBuildManifest reports whether p is a dependency or build manifest: one of
// buildManifestNames, a tsconfig*.json, or a pip requirements or constraints file, either the
// locked .txt or the .in that pip-compile reads. It changes what CI installs or builds, and the
// credits gate reads its dependencies (internal/supplychain), so it is configuration even under
// docs/, never documentation (BUG-562).
func isBuildManifest(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	if slices.Contains(buildManifestNames, base) {
		return true
	}
	if strings.HasPrefix(base, "tsconfig") && strings.HasSuffix(base, ".json") {
		return true
	}
	pip := strings.HasPrefix(base, "requirements") || strings.HasPrefix(base, "constraints")
	return pip && (strings.HasSuffix(base, ".txt") || strings.HasSuffix(base, ".in"))
}

func isConfig(p string) bool {
	return isBuildManifest(p) || strings.HasPrefix(p, ".github/") ||
		strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")
}

// compiledAgentPaths is every file compile-context writes, read from its own target list
// (HISS-19) so a new vendor target is classified as an agent file without a second edit.
var compiledAgentPaths = agentcontext.VendorTargetPaths()

// isAgent reports whether p is agent instruction text: the canonical AGENTS.md, any
// CLAUDE.md, a compiled vendor file (.cursor/rules/*.mdc, .windsurfrules,
// .github/copilot-instructions.md, ... - BUG-242), or a file under .agents/ or .paperclip/.
func isAgent(p string) bool {
	base := filepath.Base(p)
	return strings.EqualFold(base, "AGENTS.md") ||
		strings.EqualFold(base, "CLAUDE.md") ||
		slices.Contains(compiledAgentPaths, p) ||
		strings.HasPrefix(p, ".paperclip/") ||
		strings.HasPrefix(p, ".agents/")
}

func isState(p string) bool {
	return strings.HasPrefix(p, ".workingdir/") || strings.HasPrefix(p, ".workingdir2/")
}
