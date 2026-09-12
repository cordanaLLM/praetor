package adopt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// Invariant bounds (HISS-02: every loop in this package carries one of these caps).
const (
	maxInfractionsCap   = 10000
	defaultTimeout      = 30 * time.Second
	defaultMaxFuncLOC   = 60
	maxAdoptSteps       = 32
	maxTranspileTargets = 64
	maxEditorFiles      = 256
	maxArchetypeRules   = 16
	maxMarkersPerRule   = 16
)

// Canonical repository-relative paths written by adoption.
const (
	manifestFile     = ".standards.yaml"
	lockFile         = ".standards.lock"
	baselineFile     = ".standards-baseline.json"
	agentsFile       = "AGENTS.md"
	devcontainerFile = ".devcontainer/devcontainer.json"
	workingDirPath   = ".workingdir"
	defaultOwner     = "cordanaLLM"
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
	// SkipHookActivation leaves generated hooks inactive in disposable analysis clones.
	SkipHookActivation bool `json:"skip_hook_activation,omitempty"`
	// LockSourceRoot selects the verified Praetor bundle used for new lock pins.
	LockSourceRoot string `json:"lock_source_root,omitempty"`
}

// ActionDetail describes a specific planned or executed action on a target file.
type ActionDetail struct {
	Path    string `json:"path"`
	Action  string `json:"action"` // "create", "reconcile", "merge", "append", "skip"
	Details string `json:"details"`
}

// AdoptReport details the actions executed or simulated during adoption.
//
// Errors lists failures that left the repository short of the advertised state and
// must make the caller exit non-zero; Warnings lists deliberate safety skips (for
// example hooks that were not activated because their configuration was not written
// by praetor) that the caller should print but that do not fail the run.
type AdoptReport struct {
	// EffectivePolicy is the resolved planned/applied snapshot. Its source bytes
	// remain excluded by config's JSON contract; absence never implies defaults.
	EffectivePolicy *config.EffectivePolicy `json:"effective_policy,omitempty"`
	State           RepositoryState         `json:"state"`
	Archetype       string                  `json:"archetype"`
	Facets          []string                `json:"facets"`
	CreatedFiles    []string                `json:"created_files"`
	ReconciledFiles []string                `json:"reconciled_files"`
	ActionDetails   []ActionDetail          `json:"action_details,omitempty"`
	DebtBreakdown   map[string]int          `json:"debt_breakdown,omitempty"`
	LegacyDebtCount int                     `json:"legacy_debt_count"`
	DryRun          bool                    `json:"dry_run"`
	Errors          []string                `json:"errors,omitempty"`
	Warnings        []string                `json:"warnings,omitempty"`
}

// adoptSession carries the resolved inputs of one adoption run through the step chain.
type adoptSession struct {
	repoPath string
	repoName string
	arch     string
	facets   []string
	opts     AdoptOptions
	report   *AdoptReport
	policy   *config.EffectivePolicy
}

// adoptStep is one reconciliation step of the adoption chain.
type adoptStep func(ctx context.Context, s *adoptSession) error

// Adopt brings any repository to 100% template and governance compliance.
//
// When a step fails, the returned report still lists every file written before the
// failure so that callers can show what a partially adopted repository now contains.
func Adopt(ctx context.Context, opts AdoptOptions) (*AdoptReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("adopt: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("adopt cancelled: %w", err)
	}

	normPath, err := resolveTargetPath(opts)
	if err != nil {
		return nil, err
	}

	arch := resolveArchetype(normPath, opts.Profile)
	report := &AdoptReport{
		State:           DetectState(normPath),
		Archetype:       arch,
		Facets:          resolveFacets(opts.Facets),
		CreatedFiles:    make([]string, 0),
		ReconciledFiles: make([]string, 0),
		ActionDetails:   make([]ActionDetail, 0),
		DebtBreakdown:   make(map[string]int),
		DryRun:          opts.DryRun,
		Errors:          make([]string, 0),
		Warnings:        make([]string, 0),
	}
	s := &adoptSession{
		repoPath: normPath,
		repoName: resolveRepoName(ctx, normPath),
		arch:     arch,
		facets:   report.Facets,
		opts:     opts,
		report:   report,
	}

	if err := executeAdoptSteps(ctx, s); err != nil {
		return report, err
	}
	report.EffectivePolicy = s.policy
	return report, nil
}

// resolveTargetPath normalises opts.Path and validates that it is an adoptable target.
func resolveTargetPath(opts AdoptOptions) (string, error) {
	normPath, err := filepath.Abs(opts.Path)
	if err != nil {
		return "", fmt.Errorf("resolve repo path %q: %w", opts.Path, err)
	}
	if !opts.SkipGitValidation {
		if err := ValidateAdoptionTarget(normPath); err != nil {
			return "", fmt.Errorf("adoption validation failed: %w", err)
		}
		return normPath, nil
	}
	info, err := os.Stat(normPath)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("target path %q must be an existing directory", normPath)
	}
	return normPath, nil
}

// DetectState determines if a repository is greenfield, partial, or brownfield.
func DetectState(repoPath string) RepositoryState {
	manifestExists := fileExists(filepath.Join(repoPath, manifestFile))
	lockExists := fileExists(filepath.Join(repoPath, lockFile))
	agentsExists := fileExists(filepath.Join(repoPath, agentsFile))
	baselineExists := fileExists(filepath.Join(repoPath, baselineFile))

	if !manifestExists && !lockExists && !agentsExists {
		return StateGreenfield
	}
	if manifestExists && lockExists && agentsExists && baselineExists {
		return StateBrownfield
	}
	return StatePartial
}

// archetypeRule maps build-system marker files to the archetype they imply.
type archetypeRule struct {
	markers   []string
	archetype string
}

// archetypeRules returns the marker table in priority order.
func archetypeRules() []archetypeRule {
	return []archetypeRule{
		{[]string{"meson.build", "core/meson.build", "libvmaf/meson.build", "CMakeLists.txt"}, "native-gpu-systems"},
		{[]string{"go.mod"}, "framework"},
		{[]string{"Cargo.toml"}, "native-gpu-systems"},
		{[]string{"pubspec.yaml", "pom.xml", "build.gradle", "build.gradle.kts", "package.json", "pyproject.toml"}, "app-service"},
		{[]string{"Dockerfile"}, "container-image"},
	}
}

func resolveArchetype(repoPath, explicitProfile string) string {
	if explicitProfile != "" {
		return explicitProfile
	}
	rules := archetypeRules()
	for i := 0; i < len(rules) && i < maxArchetypeRules; i++ {
		for j := 0; j < len(rules[i].markers) && j < maxMarkersPerRule; j++ {
			if fileExists(filepath.Join(repoPath, filepath.FromSlash(rules[i].markers[j]))) {
				return rules[i].archetype
			}
		}
	}
	return "template-seed"
}

// resolveOwner returns the owner from the origin remote or directory layout, falling
// back to the fleet default. The lookup runs under the caller's context.
func resolveOwner(ctx context.Context, repoPath string) string {
	owner, _, err := util.ResolveRepoIdentity(ctx, repoPath)
	if err != nil || owner == "" {
		return defaultOwner
	}
	return owner
}

// resolveRepoName returns the repository name from the origin remote or directory
// layout, falling back to the directory basename.
func resolveRepoName(ctx context.Context, repoPath string) string {
	_, repo, err := util.ResolveRepoIdentity(ctx, repoPath)
	if err != nil || repo == "" {
		return filepath.Base(repoPath)
	}
	return repo
}

func resolveFacets(input []string) []string {
	if len(input) > 0 {
		return input
	}
	return []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed"}
}

// executeAdoptSteps runs the reconciliation chain in order, stopping at the first
// failure and observing context cancellation between steps.
func executeAdoptSteps(ctx context.Context, s *adoptSession) error {
	steps := []adoptStep{
		reconcileManifest,
		reconcileLockfile,
		reconcilePolicyCatalog,
		reconcileBaseline,
		reconcileAgentHarness,
		reconcileDevContainer,
		reconcileEditors,
		reconcileMakefile,
		reconcileGitIgnore,
		reconcileContributing,
		reconcilePullRequestTemplate,
		reconcileSecurityPolicy,
		reconcileADR,
		reconcileReadme,
		reconcileBranchRuleset,
		reconcileLabels,
		reconcilePaperclip,
		reconcileAgentDefinitions,
		reconcileWorkingDirAndFlavor,
		reconcileGitHooks,
	}
	for i := 0; i < len(steps) && i < maxAdoptSteps; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("adopt cancelled: %w", err)
		}
		if err := steps[i](ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func reconcileManifest(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, manifestFile)
	if err != nil {
		return err
	}
	if fileExists(full) && !s.opts.Force {
		s.report.recordReconciled(manifestFile, "Existing standards manifest verified present")
		return nil
	}
	manifest := newAdoptionManifest(ctx, s)
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := s.write(full, data, filePerm); err != nil {
		return err
	}
	s.report.recordCreated(manifestFile, fmt.Sprintf("Scaffolded standards manifest (Owner: %s, Profile: %s)", manifest.Repository.Owner, s.arch))
	return nil
}

// reconcileBaseline records the legacy-debt baseline. A scan that does not complete is
// an error: persisting an empty baseline would silently mis-anchor the HISS-13 ratchet.
func reconcileBaseline(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, baselineFile)
	if err != nil {
		return err
	}
	existed := fileExists(full)
	if existed && !s.opts.RecordBaseline {
		return s.verifyExistingBaseline(full)
	}

	base := &baseline.Baseline{Version: 1, Infractions: make([]baseline.Infraction, 0)}
	if s.opts.RecordBaseline {
		if err := scanLegacyDebt(ctx, s.repoPath, base, s.report, adoptionScanLimit(s)); err != nil {
			return err
		}
	}
	s.report.LegacyDebtCount = base.TotalInfractions
	if !s.opts.DryRun {
		if err := baseline.SaveBaseline(full, base); err != nil {
			return fmt.Errorf("save baseline: %w", err)
		}
	}
	detail := fmt.Sprintf("Recorded %d legacy debt infractions into baseline", base.TotalInfractions)
	if existed {
		s.report.recordReconciled(baselineFile, "Rescanned and "+lowerFirst(detail))
		return nil
	}
	s.report.recordCreated(baselineFile, detail)
	return nil
}

// verifyExistingBaseline keeps an existing baseline and exposes its debt count so that
// later steps (the README badge) reflect the recorded state.
func (s *adoptSession) verifyExistingBaseline(full string) error {
	base, err := baseline.LoadBaseline(full)
	if err != nil {
		s.report.addError("baseline: existing %s is unreadable: %v", baselineFile, err)
	} else {
		s.report.LegacyDebtCount = base.TotalInfractions
	}
	s.report.recordReconciled(baselineFile, "Technical debt baseline verified present")
	return nil
}

// scanLegacyDebt fills base with the current HISS infractions of repoPath. The scan is
// bounded by defaultTimeout and derived from the caller's context.
func scanLegacyDebt(ctx context.Context, repoPath string, base *baseline.Baseline, report *AdoptReport, maxFuncLOC int) error {
	scanCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	scanRep, err := hiss.Scan(scanCtx, repoPath, hiss.ScanOptions{
		MaxFuncLOC: maxFuncLOC,
		Cap:        maxInfractionsCap,
	})
	if err != nil {
		return fmt.Errorf("scan legacy debt in %s: %w", repoPath, err)
	}

	if scanRep.Truncated {
		return fmt.Errorf("scan legacy debt in %s: %w", repoPath, hiss.ErrScanTruncated)
	}

	for i := 0; i < len(scanRep.Violations) && i < maxInfractionsCap; i++ {
		v := scanRep.Violations[i]
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

func reconcileDevContainer(_ context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, devcontainerFile)
	if err != nil {
		return err
	}
	if fileExists(full) && !s.opts.Force {
		s.report.recordReconciled(devcontainerFile, "DevContainer configuration verified present")
		return nil
	}
	dc, err := devcontainer.SynthesizeFromProfiles(s.repoName, []string{s.arch}, s.facets)
	if err != nil {
		return fmt.Errorf("synthesize devcontainer: %w", err)
	}
	data, err := json.MarshalIndent(dc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal devcontainer: %w", err)
	}
	if err := s.write(full, append(data, '\n'), filePerm); err != nil {
		return err
	}
	s.report.recordCreated(devcontainerFile, fmt.Sprintf("Synthesized DevContainer for archetype '%s'", s.arch))
	return nil
}

// reconcileEditors writes IDE configurations that do not exist yet. Existing files are
// only regenerated with Force, and .clang-tidy/.editorconfig are always preserved
// because they carry hand-tuned project settings.
func reconcileEditors(_ context.Context, s *adoptSession) error {
	edOpts := editor.DefaultOptions()
	edOpts.WorkspaceRoot = s.repoPath
	edOpts.Archetype = s.arch
	set, err := editor.Synthesize(edOpts)
	if err != nil {
		return fmt.Errorf("synthesize editors: %w", err)
	}

	for i := 0; i < len(set.Files) && i < maxEditorFiles; i++ {
		if err := s.reconcileEditorFile(set.Files[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *adoptSession) reconcileEditorFile(f editor.GeneratedFile) error {
	full, err := repoFile(s.repoPath, f.Path)
	if err != nil {
		return err
	}
	if !fileExists(full) {
		if err := s.write(full, []byte(f.Content), filePerm); err != nil {
			return err
		}
		s.report.recordCreated(f.Path, fmt.Sprintf("Synthesized %s IDE configuration for archetype '%s'", f.Editor, s.arch))
		return nil
	}
	if s.opts.Force && !isUserOwnedEditorFile(f.Path) {
		if err := s.write(full, []byte(f.Content), filePerm); err != nil {
			return err
		}
		s.report.recordReconciled(f.Path, fmt.Sprintf("Regenerated %s IDE configuration for archetype '%s'", f.Editor, s.arch))
		return nil
	}
	s.report.recordReconciled(f.Path, fmt.Sprintf("Existing %s IDE configuration preserved (use --force to regenerate)", f.Editor))
	return nil
}

// isUserOwnedEditorFile reports whether an IDE file is never overwritten, mirroring the
// preservation rule of the editor package.
func isUserOwnedEditorFile(rel string) bool {
	return rel == ".clang-tidy" || rel == ".editorconfig"
}

func reconcileWorkingDirAndFlavor(ctx context.Context, s *adoptSession) error {
	if !s.opts.DryRun {
		if err := state.InitWorkingDirContext(ctx, s.repoPath); err != nil {
			s.report.addError("workingdir init: %v", err)
		}
		detectedFlv := flavor.DetectFlavor(s.repoPath)
		if _, err := flavor.ApplyFlavor(ctx, s.repoPath, detectedFlv, false); err != nil {
			s.report.addError("apply flavor %s: %v", detectedFlv, err)
		}
	}
	s.report.ActionDetails = append(s.report.ActionDetails, ActionDetail{
		Path:    workingDirPath,
		Action:  actionCreate,
		Details: "Initialized canonical session state ledger and bug/question journals",
	})
	return nil
}

// lowerFirst lower-cases the first byte of an ASCII detail string.
func lowerFirst(s string) string {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return s
	}
	return string(s[0]+('a'-'A')) + s[1:]
}
