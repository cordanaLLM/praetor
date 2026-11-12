// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package release

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestLevelStatus represents the outcome of AuditTestLevels (#1733).
type TestLevelStatus string

const (
	// TestLevelStatusPass indicates all required test levels passed or are baselined.
	TestLevelStatusPass TestLevelStatus = "PASS"
	// TestLevelStatusWarn indicates unbaselined gaps exist outside release context.
	TestLevelStatusWarn TestLevelStatus = "WARN"
	// TestLevelStatusFail indicates unbaselined gaps or invalid configuration in release context.
	TestLevelStatusFail TestLevelStatus = "FAIL"
	// TestLevelStatusSkip indicates the repository's profiles require no release test levels.
	TestLevelStatusSkip TestLevelStatus = "SKIP"

	// LiveRunHealthUnmeasured is emitted when live forge run health cannot be checked
	// because the audit runs offline or without a forge reader (#1733).
	LiveRunHealthUnmeasured = "live run health unmeasured"
	// LiveRunHealthVerified is emitted when live forge run health was checked and passed.
	LiveRunHealthVerified = "live run health verified"

	maxLevelFileBytes       = 1 << 20
	maxRepoWalkEntries      = 1 << 20
	maxWorkflowJobs         = 64
	maxReleaseWorkflowFiles = ghworkflow.MaxFiles

	defaultMaxRepoScanFiles = 10000
	repoListingEntryFactor  = 64
)

// maxRepoScanFiles bounds how many candidate files matching test-level paths one scan reads (HISS-02).
var maxRepoScanFiles = defaultMaxRepoScanFiles

// maxRepoListingEntries bounds streamed git ls-files entries per scan (defaultMaxRepoScanFiles * repoListingEntryFactor; HISS-02).
var maxRepoListingEntries = defaultMaxRepoScanFiles * repoListingEntryFactor

// maxRepoListingBufferBytes bounds retained path and stderr bytes while streaming git ls-files (HISS-02).
var maxRepoListingBufferBytes = maxGitLogBytes

// TestLevelsOptions configures AuditTestLevels (#1733).
type TestLevelsOptions struct {
	FlagRelease  bool
	BaselinePath string
	Actions      forge.ActionsReader
	Branch       string
	Head         string
}

// levelLiveHealth separates advisory branch health from required release CI evidence.
type levelLiveHealth struct {
	Status     string
	Refusal    string
	ByWorkflow map[string]forge.WorkflowRunFinding
	ByLevel    map[string]string
}

// TestLevelFinding describes a single test level gap and whether it is covered by the baseline.
type TestLevelFinding struct {
	Profile   string
	Level     string
	Anchor    string
	Reason    string
	Baselined bool
}

// TestLevelsReport holds the outcome of the release test levels gate (#1733).
type TestLevelsReport struct {
	Status      TestLevelStatus
	Detail      string
	ReleaseMode bool
	Profiles    []string
	Levels      []string
	LiveHealth  string
	Findings    []TestLevelFinding
}

type resolvedLevelBinding struct {
	Profile     string
	Level       string
	Spec        config.ReleaseTestLevelSpec
	DefaultRuns []string
	DefaultJobs []string
}

type parsedWorkflowFile struct {
	Name     string
	RelPath  string
	Workflow ghworkflow.Spec
}

// AuditTestLevels evaluates required and declared release test levels for repoPath (#1733).
func AuditTestLevels(
	ctx context.Context,
	repoPath string,
	manifest *config.Manifest,
	opts TestLevelsOptions,
) (TestLevelsReport, error) {
	report := TestLevelsReport{
		ReleaseMode: IsReleaseContext(opts.FlagRelease),
	}
	loadedManifest, err := ensureManifest(repoPath, manifest)
	if err != nil {
		report.Status = TestLevelStatusFail
		report.Detail = err.Error()
		return report, err
	}
	profiles, bindings, err := resolveLevelBindings(ctx, repoPath, loadedManifest)
	if err != nil {
		report.Status = TestLevelStatusFail
		report.Detail = err.Error()
		return report, err
	}
	report.Profiles = profiles
	if len(bindings) == 0 {
		report.Status = TestLevelStatusSkip
		report.Detail = "no release test levels required by profiles"
		return report, nil
	}
	for i := 0; i < len(bindings) && i < config.MaxReleaseTestLevels; i++ {
		report.Levels = append(report.Levels, bindings[i].Level)
	}
	report, err = evaluateLevelBindings(ctx, repoPath, bindings, opts, report)
	if err != nil && report.Detail == "" {
		report.Status = TestLevelStatusFail
		report.Detail = err.Error()
	}
	return report, err
}

// ensureManifest validates an existing manifest or loads and validates .standards.yaml from
// repoPath, returning an empty manifest if the file does not exist.
func ensureManifest(repoPath string, manifest *config.Manifest) (*config.Manifest, error) {
	if manifest != nil {
		if err := config.ValidateManifest(manifest); err != nil {
			return nil, fmt.Errorf("validate supplied manifest: %w", err)
		}
		return manifest, nil
	}
	loaded, err := config.LoadManifest(filepath.Join(repoPath, config.ManifestFileName))
	if errors.Is(err, os.ErrNotExist) {
		return &config.Manifest{}, nil
	}
	return loaded, err
}

// ResolveRepositoryProfiles returns the union of profiles from the repository's resolved flavor
// (HISSProfile plus AlsoImplements) or its declared profiles when no flavor applies (#1733).
func ResolveRepositoryProfiles(repoPath string, manifest *config.Manifest) ([]string, error) {
	var profiles []string
	targets, err := flavor.ResolveTargets(repoPath)
	if err != nil && !flavor.IsNotApplicable(err) && !errors.Is(err, flavor.ErrNoFlavorMatched) {
		return nil, err
	}
	for i := 0; i < len(targets) && i < 16; i++ {
		profiles = appendFlavorProfiles(profiles, targets[i].Flavor)
	}
	profiles = appendManifestProfiles(repoPath, profiles, manifest, len(targets) == 0)
	if len(profiles) == 0 {
		profiles = appendUniqueProfile(profiles, flavor.ResolutionProfile(repoPath))
	}
	return profiles, nil
}

// appendUniqueProfile trims and appends profile p to profiles if p is non-empty and not already present.
func appendUniqueProfile(profiles []string, p string) []string {
	p = strings.TrimSpace(p)
	if p != "" && !slices.Contains(profiles, p) {
		return append(profiles, p)
	}
	return profiles
}

// appendFlavorProfiles looks up flvName in the flavor registry and appends its HISS profile
// and any secondary implemented profiles to profiles.
func appendFlavorProfiles(profiles []string, flvName string) []string {
	flv, err := flavor.Get(flvName)
	if err != nil || flv == nil {
		return profiles
	}
	profiles = appendUniqueProfile(profiles, flv.HISSProfile())
	if multi, ok := flv.(flavor.MultiProfile); ok {
		for _, extra := range multi.AlsoImplements() {
			profiles = appendUniqueProfile(profiles, extra)
		}
	}
	return profiles
}

// appendManifestProfiles resolves flavor targets and appends profiles declared in manifest to profiles.
func appendManifestProfiles(repoPath string, profiles []string, manifest *config.Manifest, resolveFlavor bool) []string {
	if manifest == nil {
		return profiles
	}
	for i := 0; i < len(manifest.Profiles) && i < 16; i++ {
		if resolveFlavor {
			if flvName, err := flavor.ResolveForProfile(repoPath, manifest.Profiles[i]); err == nil {
				profiles = appendFlavorProfiles(profiles, flvName)
			}
		}
		profiles = appendUniqueProfile(profiles, manifest.Profiles[i])
	}
	return profiles
}

// resolveLevelBindings resolves repository archetype profiles and binds required and declared
// release test levels into evaluated bindings.
func resolveLevelBindings(
	ctx context.Context,
	repoPath string,
	manifest *config.Manifest,
) ([]string, []resolvedLevelBinding, error) {
	profiles, err := ResolveRepositoryProfiles(repoPath, manifest)
	if err != nil {
		return nil, nil, err
	}
	if manifest != nil && manifest.Release != nil {
		if err := manifest.Release.Validate(); err != nil {
			return nil, nil, err
		}
		if err := manifest.Release.ValidateForProfiles(ctx, repoPath, profiles); err != nil {
			return nil, nil, err
		}
	}
	byProfile, err := config.ProfileReleaseTestLevels(ctx, repoPath, profiles)
	if err != nil {
		return nil, nil, err
	}
	return profiles, mergeProfileAndDeclaredLevels(profiles, byProfile, manifest), nil
}

// mergeProfileAndDeclaredLevels combines profile-default test levels with manifest-declared
// test level overrides into a deduplicated list of bindings.
func mergeProfileAndDeclaredLevels(
	profiles []string,
	byProfile map[string][]string,
	manifest *config.Manifest,
) []resolvedLevelBinding {
	var bindings []resolvedLevelBinding
	seen := make(map[string]bool)
	for i := 0; i < len(profiles) && i < 16; i++ {
		prof := profiles[i]
		levels := byProfile[prof]
		for j := 0; j < len(levels) && j < config.MaxReleaseTestLevels; j++ {
			lvl := levels[j]
			if seen[lvl] {
				continue
			}
			seen[lvl] = true
			bindings = append(bindings, buildLevelBinding(prof, lvl, manifest))
		}
	}
	return appendDeclaredExtraLevels(bindings, seen, profiles, manifest)
}

// appendDeclaredExtraLevels appends any test levels declared in manifest.Release.TestLevels
// that were not already bound by archetype profiles.
func appendDeclaredExtraLevels(
	bindings []resolvedLevelBinding,
	seen map[string]bool,
	profiles []string,
	manifest *config.Manifest,
) []resolvedLevelBinding {
	if manifest == nil || manifest.Release == nil || len(manifest.Release.TestLevels) == 0 {
		return bindings
	}
	primaryProfile := "repository"
	if len(profiles) > 0 {
		primaryProfile = profiles[0]
	}
	var extras []string
	for lvl := range manifest.Release.TestLevels {
		if !seen[lvl] {
			extras = append(extras, lvl)
		}
	}
	slices.Sort(extras)
	for i := 0; i < len(extras) && i < config.MaxReleaseTestLevels; i++ {
		lvl := extras[i]
		bindings = append(bindings, buildLevelBinding(primaryProfile, lvl, manifest))
	}
	return bindings
}

// buildLevelBinding constructs a resolvedLevelBinding for profile and level using default
// specifications overlaid with any custom manifest configuration.
func buildLevelBinding(profile, level string, manifest *config.Manifest) resolvedLevelBinding {
	defSpec, defRuns, defJobs := defaultLevelSpec(level)
	if manifest != nil && manifest.Release != nil {
		if custom, ok := manifest.Release.TestLevels[level]; ok {
			if len(custom.Paths) > 0 {
				defSpec.Paths = custom.Paths
			}
			defSpec.Pattern = custom.Pattern
			defSpec.Job = custom.Job
			defSpec.Run = custom.Run
		}
	}
	return resolvedLevelBinding{
		Profile:     profile,
		Level:       level,
		Spec:        defSpec,
		DefaultRuns: defRuns,
		DefaultJobs: defJobs,
	}
}

// defaultLevelSpec returns the default ReleaseTestLevelSpec, runner patterns, and job names
// for standard test levels such as unit, integration, and e2e.
func defaultLevelSpec(level string) (config.ReleaseTestLevelSpec, []string, []string) {
	switch level {
	case "unit":
		return config.ReleaseTestLevelSpec{
				Paths: []string{
					"**/*_test.go", "**/tests/**", "**/test/**", "**/*.test.ts", "**/*.test.js",
					"**/*.spec.ts", "**/*.spec.js", "**/test_*.py", "**/*_test.py", "**/*_test.rs",
				},
			},
			[]string{"go test", "cargo test", "pytest", "npm test", "npm run test", "pnpm test", "pnpm run test", "yarn test", "bun test", "ctest", "flutter test", "dart test", "mvn test", "gradle test"},
			[]string{"unit", "test"}
	case "integration":
		return config.ReleaseTestLevelSpec{
				Paths: []string{
					"**/*_integration_test.go", "**/integration/**", "**/tests/integration/**",
					"**/test/integration/**", "**/*.integration.ts", "**/*.integration.js", "**/test_*_integration.py",
				},
			},
			[]string{"integration", "go test", "cargo test", "pytest", "ctest", "npm test", "pnpm test", "yarn test", "bun test"},
			[]string{"integration", "test"}
	case "e2e":
		return config.ReleaseTestLevelSpec{Paths: config.DefaultReleaseE2EPaths()},
			[]string{"e2e", "playwright", "cypress", "go test", "cargo test", "pytest"},
			[]string{"e2e"}
	default:
		return defaultSpecializedLevelSpec(level)
	}
}

// defaultSpecializedLevelSpec returns default specifications, runner commands, and job names
// for specialized test levels like load_soak, recovery_restore, a11y, and smoke.
func defaultSpecializedLevelSpec(level string) (config.ReleaseTestLevelSpec, []string, []string) {
	switch level {
	case "load_soak":
		return config.ReleaseTestLevelSpec{
				Paths: []string{
					"load/**", "soak/**", "perf/**", "bench/**", "k6/**", "tests/load/**",
					"tests/soak/**", "test/load/**", "test/soak/**", "**/*_load_test.go", "**/*_soak_test.go", "**/*_bench_test.go",
				},
			},
			[]string{"load", "soak", "k6", "bench", "locust", "vegeta"},
			[]string{"load", "soak", "perf", "bench"}
	case "recovery_restore":
		return config.ReleaseTestLevelSpec{
				Paths: []string{
					"recovery/**", "restore/**", "dr/**", "tests/recovery/**", "tests/restore/**",
					"test/recovery/**", "test/restore/**", "**/*_recovery_test.go", "**/*_restore_test.go",
				},
			},
			[]string{"recovery", "restore", "backup", "go test", "pytest"},
			[]string{"recovery", "restore"}
	case "a11y":
		return config.ReleaseTestLevelSpec{
				Paths: []string{"tests/a11y.spec.ts", "**/a11y/**", "**/*a11y*.spec.ts", "**/*a11y*.test.ts", "**/*a11y*_test.go"},
			},
			[]string{"a11y", "ui-gates", "playwright", "axe"},
			[]string{"a11y", "ui-gates", "accessibility"}
	case "smoke":
		return config.ReleaseTestLevelSpec{
				Paths: []string{"smoke/**", "tests/smoke/**", "test/smoke/**", "**/*_smoke_test.go", "**/*smoke*"},
			},
			[]string{"smoke", "kubeconform", "packer", "mkosi", "docker", "helm", "terraform", "go test", "pytest"},
			[]string{"smoke", "verify", "test", "build"}
	default:
		return config.ReleaseTestLevelSpec{Paths: []string{"**/*" + level + "*"}}, []string{level}, []string{level}
	}
}

// evaluateLevelBindings checks each resolved test level binding against tracked repository files,
// workflow job steps, and live run health, returning a populated TestLevelsReport.
func evaluateLevelBindings(
	ctx context.Context,
	repoPath string,
	bindings []resolvedLevelBinding,
	opts TestLevelsOptions,
	report TestLevelsReport,
) (TestLevelsReport, error) {
	files, err := listLevelCandidateFiles(ctx, repoPath, bindings)
	if err != nil {
		return report, err
	}
	workflows, auxScripts, err := loadRepositoryWorkflows(ctx, repoPath)
	if err != nil {
		return report, err
	}
	live := checkTestLevelLiveHealth(ctx, repoPath, opts, bindings, workflows, auxScripts)
	report.LiveHealth = live.Status

	baselinedAnchors, err := loadBaselinedLevelAnchors(repoPath, opts.BaselinePath)
	if err != nil {
		return report, err
	}
	for i := 0; i < len(bindings) && i < config.MaxReleaseTestLevels; i++ {
		b := bindings[i]
		reason, lvlErr := checkSingleLevel(ctx, repoPath, b, files, workflows, auxScripts, live)
		if lvlErr != nil {
			return report, lvlErr
		}
		if reason == "" {
			continue
		}
		anchor := fmt.Sprintf("level:%s:%s", b.Profile, b.Level)
		isBaselined := isLevelAnchorBaselined(b.Profile, b.Level, report.Profiles, baselinedAnchors)
		isBaselined = isBaselined && live.Refusal == "" && live.ByLevel[b.Level] == ""
		report.Findings = append(report.Findings, TestLevelFinding{
			Profile:   b.Profile,
			Level:     b.Level,
			Anchor:    anchor,
			Reason:    reason,
			Baselined: isBaselined,
		})
	}
	return finalizeTestLevelsReport(report)
}

// checkSingleLevel evaluates one resolved level binding against repository files, workflows, and live forge health.
func checkSingleLevel(
	ctx context.Context,
	repoPath string,
	b resolvedLevelBinding,
	files []string,
	workflows []parsedWorkflowFile,
	auxScripts string,
	live levelLiveHealth,
) (string, error) {
	if live.Refusal != "" {
		return live.Refusal, nil
	}
	matchedFiles, err := matchFilesForLevel(ctx, repoPath, files, b.Spec)
	if err != nil {
		return "", err
	}
	if len(matchedFiles) == 0 {
		return fmt.Sprintf("no tracked file matches paths %v", b.Spec.Paths), nil
	}
	if b.Spec.Pattern != "" && !anyFileMatchesPattern(ctx, repoPath, matchedFiles, b.Spec.Pattern) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return fmt.Sprintf("no matched file satisfies pattern %q", b.Spec.Pattern), nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	wfName, _, ok := findRunnableWorkflowForLevel(b, workflows, auxScripts)
	if !ok {
		return "no runnable workflow job executes this level", nil
	}
	return live.levelRefusal(b.Level, wfName), nil
}

// levelRefusal reports missing release evidence or unhealthy advisory workflow runs for one level.
func (live levelLiveHealth) levelRefusal(level, workflow string) string {
	if reason := live.ByLevel[level]; reason != "" {
		return reason
	}
	if finding, hasLive := live.ByWorkflow[workflow]; hasLive && finding.Health != forge.WorkflowHealthy && finding.Health != forge.WorkflowSelf {
		return fmt.Sprintf("workflow %s run health is %s", workflow, finding.Health)
	}
	return ""
}

// listLevelCandidateFiles returns existing regular repository files matching at least one binding.
func listLevelCandidateFiles(ctx context.Context, repoPath string, bindings []resolvedLevelBinding) ([]string, error) {
	return listRepositoryFiles(ctx, repoPath, func(rel string) (bool, error) {
		return fileMatchesAnyBinding(ctx, repoPath, rel, bindings)
	})
}

// fileMatchesAnyBinding reports whether rel matches the path rules of any resolved level binding.
func fileMatchesAnyBinding(ctx context.Context, root, rel string, bindings []resolvedLevelBinding) (bool, error) {
	if len(bindings) > config.MaxReleaseTestLevels {
		return false, fmt.Errorf("test level bindings passed %d levels", config.MaxReleaseTestLevels)
	}
	for i := 0; i < len(bindings) && i < config.MaxReleaseTestLevels; i++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		hasPattern := strings.TrimSpace(bindings[i].Spec.Pattern) != ""
		matched, err := fileMatchesLevelPaths(ctx, root, rel, bindings[i].Spec.Paths, hasPattern)
		if err != nil || matched {
			return matched, err
		}
	}
	return false, nil
}

// matchFilesForLevel returns repository files matching the level's path globs, requiring
// a recognizable test file for directory globs when pattern is unset.
func matchFilesForLevel(ctx context.Context, root string, files []string, spec config.ReleaseTestLevelSpec) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(files) > maxRepoScanFiles {
		return nil, fmt.Errorf("test level scan passed %d scanned candidate files; narrow test_levels paths", maxRepoScanFiles)
	}
	hasPattern := strings.TrimSpace(spec.Pattern) != ""
	var matched []string
	for i := 0; i < len(files) && i < maxRepoScanFiles; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ok, err := fileMatchesLevelPaths(ctx, root, files[i], spec.Paths, hasPattern)
		if err != nil {
			return nil, err
		}
		if ok {
			matched = append(matched, files[i])
		}
	}
	return matched, nil
}

// fileMatchesLevelPaths reports whether rel matches any glob in paths; a directory glob without a
// pattern accepts only a test file by its language convention (forge.IsTestFileContext).
func fileMatchesLevelPaths(ctx context.Context, root, rel string, paths []string, hasPattern bool) (bool, error) {
	if len(paths) > config.MaxReleaseE2EPaths {
		return false, fmt.Errorf("test level path globs passed %d entries", config.MaxReleaseE2EPaths)
	}
	for i := 0; i < len(paths) && i < config.MaxReleaseE2EPaths; i++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		g := paths[i]
		if !matchesAnyGlob(rel, []string{g}) {
			continue
		}
		if !isDirectoryGlob(g) || hasPattern {
			return true, nil
		}
		isTest, err := forge.IsTestFileContext(ctx, root, rel)
		if err != nil || isTest {
			return isTest, err
		}
	}
	return false, nil
}

// isDirectoryGlob reports whether glob matches a directory tree rather than a specific file pattern.
func isDirectoryGlob(glob string) bool {
	return strings.HasSuffix(glob, "/**") || strings.HasSuffix(glob, "/*") || strings.HasSuffix(glob, "/")
}

// anyFileMatchesPattern reports whether any file in relPaths contains content matching the
// given regular expression pattern.
func anyFileMatchesPattern(ctx context.Context, repoPath string, relPaths []string, pattern string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	for i := 0; i < len(relPaths) && i < maxRepoScanFiles; i++ {
		data, readErr := contextopt.ReadSnapshot(ctx, filepath.Join(repoPath, filepath.FromSlash(relPaths[i])))
		if readErr != nil || len(data) > maxLevelFileBytes {
			continue
		}
		if re.Match(data) {
			return true
		}
	}
	return false
}

// findRunnableWorkflowForLevel inspects workflows to locate a runnable workflow file executing
// the specified test level binding, returning its filename, matched job name, and whether found.
func findRunnableWorkflowForLevel(
	b resolvedLevelBinding,
	workflows []parsedWorkflowFile,
	auxScripts string,
) (string, string, bool) {
	for i := 0; i < len(workflows) && i < maxReleaseWorkflowFiles; i++ {
		wf := workflows[i]
		if job, ok := workflowJobForLevel(b, wf.Workflow, auxScripts); ok {
			return wf.Name, job, true
		}
	}
	return "", "", false
}

// workflowJobForLevel returns the display name (or ID) of the first runnable job matching b.
func workflowJobForLevel(b resolvedLevelBinding, wf ghworkflow.Spec, auxScripts string) (string, bool) {
	jobIDs := slices.Sorted(maps.Keys(wf.Jobs))
	for i := 0; i < len(jobIDs) && i < maxWorkflowJobs; i++ {
		id := jobIDs[i]
		job := wf.Jobs[id]
		if ghworkflow.NeverRuns(job.If) || advisoryContinueOnError(job.ContinueOnError) {
			continue
		}
		if jobMatchesLevel(b, id, job, auxScripts) {
			if job.Name != "" {
				return job.Name, true
			}
			return id, true
		}
	}
	return "", false
}

// jobMatchesLevel reports whether workflow job satisfies the job name or run step requirements
// for test level binding b.
func jobMatchesLevel(b resolvedLevelBinding, jobID string, job ghworkflow.Job, auxScripts string) bool {
	jobMatch := matchesJobName(jobID, job.Name, b.Spec.Job, b.DefaultJobs)
	if b.Spec.Job != "" && !jobMatch {
		return false
	}
	runSteps := runnableStepsText(job, auxScripts)
	if runSteps == "" {
		return false
	}
	if b.Spec.Run != "" {
		return strings.Contains(runSteps, b.Spec.Run)
	}
	if b.Spec.Job != "" {
		return true
	}
	for i := 0; i < len(b.DefaultRuns) && i < 32; i++ {
		if strings.Contains(runSteps, b.DefaultRuns[i]) {
			return true
		}
	}
	return jobMatch
}

// matchesJobName reports whether jobID or jobName matches the explicit job target or any of
// the default job name keywords.
func matchesJobName(jobID, jobName, explicit string, defaults []string) bool {
	idLower := strings.ToLower(strings.TrimSpace(jobID))
	nameLower := strings.ToLower(strings.TrimSpace(jobName))
	if explicit != "" {
		target := strings.ToLower(strings.TrimSpace(explicit))
		return idLower == target || nameLower == target ||
			strings.Contains(idLower, target) || strings.Contains(nameLower, target)
	}
	for i := 0; i < len(defaults) && i < 16; i++ {
		tok := strings.ToLower(defaults[i])
		if idLower == tok || nameLower == tok || strings.Contains(idLower, tok) || strings.Contains(nameLower, tok) {
			return true
		}
	}
	return false
}

// runnableStepsText concatenates runnable step commands and uses clauses from job, appending
// auxiliary scripts when build tool runners are invoked.
func runnableStepsText(job ghworkflow.Job, auxScripts string) string {
	var b strings.Builder
	for i := 0; i < len(job.Steps) && i < 64; i++ {
		step := job.Steps[i]
		if ghworkflow.NeverRuns(step.If) || advisoryContinueOnError(step.ContinueOnError) {
			continue
		}
		run := strings.TrimSpace(step.Run)
		uses := strings.TrimSpace(step.Uses)
		if run == "" && uses == "" {
			continue
		}
		b.WriteString(run)
		b.WriteByte('\n')
		b.WriteString(uses)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return ""
	}
	if auxScripts != "" && invokesAuxBuildRunner(b.String()) {
		b.WriteString(auxScripts)
	}
	return b.String()
}

// invokesAuxBuildRunner reports whether the concatenated steps text invokes common build tools
// like make or npm package managers.
func invokesAuxBuildRunner(stepsText string) bool {
	for _, runner := range []string{"make", "npm ", "pnpm ", "yarn ", "bun "} {
		if strings.Contains(stepsText, runner) {
			return true
		}
	}
	return false
}

// advisoryContinueOnError reports whether raw continue-on-error YAML expressions evaluate to true.
func advisoryContinueOnError(raw string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	return trimmed == "true" || strings.Contains(trimmed, "true")
}

// loadRepositoryWorkflows loads and parses GitHub Actions workflows and auxiliary build files.
func loadRepositoryWorkflows(ctx context.Context, repoPath string) (_ []parsedWorkflowFile, _ string, err error) {
	if ctx == nil {
		return nil, "", errors.New("context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, "", fmt.Errorf("read workflows: %w", err)
	}
	root, err := contextopt.OpenDirectoryIn(ctx, repoPath, filepath.Join(".github", "workflows"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, loadAuxBuildScripts(ctx, repoPath), nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read workflows directory: %w", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	entries, err := readWorkflowDirEntries(root)
	if err != nil {
		return nil, "", fmt.Errorf("read workflows directory: %w", err)
	}
	workflows, err := parseWorkflowEntries(ctx, root, entries)
	if err != nil {
		return nil, "", err
	}
	aux := loadAuxBuildScripts(ctx, repoPath)
	if err := ctx.Err(); err != nil {
		return nil, "", fmt.Errorf("read auxiliary build scripts: %w", err)
	}
	return workflows, aux, nil
}

// readWorkflowDirEntries reads up to maxReleaseWorkflowFiles entries from a workflows directory root,
// sorted by entry name so workflow evaluation does not depend on filesystem directory order.
func readWorkflowDirEntries(root *os.Root) ([]os.DirEntry, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open directory: %w", err)
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	entries, err := directory.ReadDir(maxReleaseWorkflowFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read directory: %w", err)
	}
	if len(entries) > maxReleaseWorkflowFiles {
		return nil, fmt.Errorf("workflow inventory exceeds %d files", maxReleaseWorkflowFiles)
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return entries, nil
}

// parseWorkflowEntries parses workflow definitions from directory entries, failing closed on context cancellation.
func parseWorkflowEntries(ctx context.Context, root *os.Root, entries []os.DirEntry) ([]parsedWorkflowFile, error) {
	var parsed []parsedWorkflowFile
	for i := 0; i < len(entries) && i < maxReleaseWorkflowFiles; i++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("scan workflow entries: %w", err)
		}
		item, err := parseSingleWorkflowEntry(ctx, root, entries[i])
		if err != nil {
			return nil, err
		}
		if item != nil {
			parsed = append(parsed, *item)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("scan workflow entries: %w", err)
	}
	return parsed, nil
}

// parseSingleWorkflowEntry reads and parses a single workflow file, failing closed on context cancellation.
func parseSingleWorkflowEntry(ctx context.Context, root *os.Root, entry os.DirEntry) (*parsedWorkflowFile, error) {
	if entry.IsDir() || !isYAMLFilename(entry.Name()) {
		return nil, nil
	}
	data, readErr := contextopt.ReadRootSnapshot(ctx, root, entry.Name())
	if readErr != nil {
		if isContextDone(ctx, readErr) {
			return nil, fmt.Errorf("read workflow snapshot %s: %w", entry.Name(), readErr)
		}
		return nil, nil
	}
	if len(data) > maxLevelFileBytes {
		return nil, nil
	}
	wf, parseErr := ghworkflow.Parse(data)
	if parseErr != nil {
		return nil, nil //nolint:nilerr // an unparseable workflow supplies no job, so the levels it would carry show as gaps
	}
	return &parsedWorkflowFile{
		Name:     entry.Name(),
		RelPath:  filepath.ToSlash(filepath.Join(".github", "workflows", entry.Name())),
		Workflow: wf,
	}, nil
}

// isContextDone reports whether err or ctx indicates context cancellation or deadline expiration.
func isContextDone(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (ctx != nil && ctx.Err() != nil)
}

// isYAMLFilename reports whether name has a .yml or .yaml file extension.
func isYAMLFilename(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}

// loadAuxBuildScripts reads auxiliary build configuration files such as Makefile and package.json
// from repoPath into a concatenated string.
func loadAuxBuildScripts(ctx context.Context, repoPath string) string {
	var b strings.Builder
	for _, name := range []string{"Makefile", "package.json"} {
		data, err := contextopt.ReadSnapshot(ctx, filepath.Join(repoPath, name))
		if err == nil && len(data) <= maxLevelFileBytes {
			b.Write(data)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// listRepositoryFiles returns tracked git files matching keep in repoPath, or falls back to a
// bounded filesystem directory walk when no git repository is present.
func listRepositoryFiles(ctx context.Context, repoPath string, keep func(string) (bool, error)) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("list repository files requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !util.PathExists(filepath.Join(repoPath, ".git")) {
		return walkRepositoryFilesBounded(ctx, repoPath, keep)
	}
	return streamGitTrackedFiles(ctx, repoPath, keep)
}

func streamGitTrackedFiles(ctx context.Context, repoPath string, keep func(string) (bool, error)) ([]string, error) {
	envCtx, err := util.WithGitProbeEnvironment(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	probeCtx, cancelProbe := context.WithTimeout(envCtx, contextopt.MaxDuration)
	defer cancelProbe()
	streamCtx, stopStream := context.WithCancel(probeCtx)
	defer stopStream()

	stream := &gitTrackedCandidateStream{
		repoPath:  repoPath,
		callerCtx: ctx,
		probeCtx:  probeCtx,
		stop:      stopStream,
		keep:      keep,
	}
	stderrCap := min(max(maxRepoListingBufferBytes, 1), util.MaxCommandOutputBytes)
	stderr, runErr := util.RunCommandStream(
		streamCtx, repoPath, "git", nil, stream, stderrCap,
		util.GitProbeArgs("ls-files", "--cached", "-z")...,
	)
	if stream.err != nil {
		return nil, stream.err
	}
	if runErr != nil {
		return nil, formatReleaseGitListError(ctx, probeCtx, runErr, stderr)
	}
	if err := stream.finish(); err != nil {
		return nil, err
	}
	return stream.candidates, nil
}

func formatReleaseGitListError(callerCtx, probeCtx context.Context, runErr error, stderr []byte) error {
	if ctxErr := cmp.Or(callerCtx.Err(), probeCtx.Err()); ctxErr != nil {
		return fmt.Errorf("list tracked files: %w", ctxErr)
	}
	if preview := util.BodyPreview(stderr); preview != "" {
		return fmt.Errorf("list tracked files: %w: %s", runErr, preview)
	}
	return fmt.Errorf("list tracked files: %w", runErr)
}

type gitTrackedCandidateStream struct {
	repoPath        string
	callerCtx       context.Context
	probeCtx        context.Context
	stop            context.CancelFunc
	keep            func(string) (bool, error)
	partial         bytes.Buffer
	candidates      []string
	streamedEntries int
	retainedBytes   int
	err             error
}

func (g *gitTrackedCandidateStream) Write(chunk []byte) (int, error) {
	if g.err != nil {
		return 0, g.err
	}
	if ctxErr := cmp.Or(g.callerCtx.Err(), g.probeCtx.Err()); ctxErr != nil {
		return 0, g.fail(fmt.Errorf("list tracked files: %w", ctxErr))
	}
	written := len(chunk)
	maxSteps := len(chunk) + 1
	for step := 0; len(chunk) > 0 && step < maxSteps; step++ {
		nul := bytes.IndexByte(chunk, 0)
		if nul < 0 {
			if err := g.bufferPartial(chunk); err != nil {
				return 0, err
			}
			return written, nil
		}
		if err := g.consumeEntry(chunk[:nul]); err != nil {
			return 0, err
		}
		chunk = chunk[nul+1:]
	}
	if len(chunk) > 0 {
		return 0, g.fail(fmt.Errorf("test level git listing chunk passed %d parse steps", maxSteps))
	}
	return written, nil
}

func (g *gitTrackedCandidateStream) fail(cause error) error {
	if g.err == nil {
		g.err = cause
		g.stop()
	}
	return g.err
}

func (g *gitTrackedCandidateStream) bufferPartial(part []byte) error {
	if g.retainedBytes+g.partial.Len()+len(part) > maxRepoListingBufferBytes {
		return g.fail(fmt.Errorf("test level git listing passed maxRepoListingBufferBytes (%d bytes)", maxRepoListingBufferBytes))
	}
	if _, err := g.partial.Write(part); err != nil {
		return g.fail(err)
	}
	return nil
}

func (g *gitTrackedCandidateStream) consumeEntry(part []byte) error {
	if ctxErr := cmp.Or(g.callerCtx.Err(), g.probeCtx.Err()); ctxErr != nil {
		return g.fail(fmt.Errorf("list tracked files: %w", ctxErr))
	}
	if g.retainedBytes+g.partial.Len()+len(part) > maxRepoListingBufferBytes {
		return g.fail(fmt.Errorf("test level git listing passed maxRepoListingBufferBytes (%d bytes)", maxRepoListingBufferBytes))
	}
	raw := string(part)
	if g.partial.Len() > 0 {
		if _, err := g.partial.Write(part); err != nil {
			return g.fail(err)
		}
		raw = g.partial.String()
		g.partial.Reset()
	}
	if raw == "" {
		return nil
	}
	return g.acceptTrackedPath(filepath.ToSlash(raw))
}

func (g *gitTrackedCandidateStream) acceptTrackedPath(rel string) error {
	g.streamedEntries++
	if g.streamedEntries >= maxRepoListingEntries {
		return g.fail(fmt.Errorf("test level git listing reached maxRepoListingEntries bound (%d entries)", maxRepoListingEntries))
	}
	if g.keep != nil {
		matched, keepErr := g.keep(rel)
		if keepErr != nil {
			return g.fail(keepErr)
		}
		if ctxErr := cmp.Or(g.callerCtx.Err(), g.probeCtx.Err()); ctxErr != nil {
			return g.fail(fmt.Errorf("list tracked files: %w", ctxErr))
		}
		if !matched {
			return nil
		}
	}
	if !isExistingRegularFile(g.repoPath, rel) {
		return nil
	}
	if len(g.candidates) >= maxRepoScanFiles {
		return g.fail(fmt.Errorf("test level scan passed %d scanned candidate files; narrow test_levels paths", maxRepoScanFiles))
	}
	g.candidates = append(g.candidates, rel)
	g.retainedBytes += len(rel)
	return nil
}

func (g *gitTrackedCandidateStream) finish() error {
	if ctxErr := cmp.Or(g.callerCtx.Err(), g.probeCtx.Err()); ctxErr != nil {
		return g.fail(fmt.Errorf("list tracked files: %w", ctxErr))
	}
	if g.partial.Len() == 0 {
		return nil
	}
	raw := g.partial.String()
	g.partial.Reset()
	return g.acceptTrackedPath(filepath.ToSlash(raw))
}

// isExistingRegularFile reports whether rel under repoPath exists as a regular file.
func isExistingRegularFile(repoPath, rel string) bool {
	info, err := os.Stat(filepath.Join(repoPath, filepath.FromSlash(rel)))
	return err == nil && info.Mode().IsRegular()
}

// walkRepositoryFilesBounded performs a bounded filesystem walk of repoPath, skipping ignored
// directories and returning relative paths to regular files matching keep.
func walkRepositoryFilesBounded(ctx context.Context, repoPath string, keep func(string) (bool, error)) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("walk repository files requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var files []string
	visited := 0
	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxRepoWalkEntries {
			return fmt.Errorf("repository walk exceeded %d entries", maxRepoWalkEntries)
		}
		if d.IsDir() {
			return skipIgnoredRepoDir(d.Name())
		}
		if err := collectWalkedRepoFile(repoPath, path, d, keep, &files); err != nil {
			return err
		}
		return ctx.Err()
	})
	return files, err
}

// skipIgnoredRepoDir returns filepath.SkipDir for ignored repository directories.
func skipIgnoredRepoDir(name string) error {
	if name == ".git" || name == ".workingdir" || name == "node_modules" {
		return filepath.SkipDir
	}
	return nil
}

// collectWalkedRepoFile appends a walked regular candidate file to files within maxRepoScanFiles.
func collectWalkedRepoFile(repoPath, path string, d fs.DirEntry, keep func(string) (bool, error), files *[]string) error {
	if !d.Type().IsRegular() {
		return nil
	}
	rel, err := filepath.Rel(repoPath, path)
	if err != nil {
		return fmt.Errorf("relativize walked path %s: %w", path, err)
	}
	slashed := filepath.ToSlash(rel)
	if keep != nil {
		ok, keepErr := keep(slashed)
		if keepErr != nil {
			return keepErr
		}
		if !ok {
			return nil
		}
	}
	if len(*files) >= maxRepoScanFiles {
		return fmt.Errorf("test level scan passed %d scanned candidate files; narrow test_levels paths", maxRepoScanFiles)
	}
	*files = append(*files, slashed)
	return nil
}

// checkTestLevelLiveHealth selects release-commit evidence or the existing branch-health audit.
func checkTestLevelLiveHealth(
	ctx context.Context,
	repoPath string,
	opts TestLevelsOptions,
	bindings []resolvedLevelBinding,
	workflows []parsedWorkflowFile,
	auxScripts string,
) levelLiveHealth {
	if opts.FlagRelease {
		return checkReleaseLevelHealth(ctx, opts, bindings, workflows, auxScripts)
	}
	status, byWorkflow := checkLiveWorkflowHealth(ctx, repoPath, opts)
	return levelLiveHealth{Status: status + " (default-branch workflow health)", ByWorkflow: byWorkflow}
}

// checkReleaseLevelHealth requires live evidence for each matched level and refuses all on read errors.
func checkReleaseLevelHealth(
	ctx context.Context,
	opts TestLevelsOptions,
	bindings []resolvedLevelBinding,
	workflows []parsedWorkflowFile,
	auxScripts string,
) levelLiveHealth {
	opts.Head = strings.TrimSpace(opts.Head)
	opts.Branch = strings.TrimSpace(opts.Branch)
	if opts.Branch == "" {
		opts.Branch = forge.FallbackDefaultBranch
	}
	if opts.Actions == nil {
		return unmeasuredReleaseCI("no Actions reader")
	}
	if opts.Head == "" {
		return unmeasuredReleaseCI("release Head is empty")
	}
	live := levelLiveHealth{
		Status:  fmt.Sprintf("release commit CI checked for %.12s on %s", opts.Head, opts.Branch),
		ByLevel: make(map[string]string, len(bindings)),
	}
	for i := 0; i < len(bindings) && i < config.MaxReleaseTestLevels; i++ {
		wf, job, ok := findRunnableWorkflowForLevel(bindings[i], workflows, auxScripts)
		if !ok {
			continue
		}
		reason, err := checkReleaseLevelRun(ctx, opts, wf, job)
		if err != nil {
			return unmeasuredReleaseCI(err.Error())
		}
		live.ByLevel[bindings[i].Level] = reason
	}
	return live
}

// unmeasuredReleaseCI makes unavailable live evidence a refusal rather than a static-only pass.
func unmeasuredReleaseCI(cause string) levelLiveHealth {
	reason := "did not run in the release CI: " + cause
	return levelLiveHealth{Status: LiveRunHealthUnmeasured + ": release commit CI: " + cause, Refusal: reason}
}

// checkReleaseLevelRun checks the newest completed branch run for Head and its matched job.
func checkReleaseLevelRun(ctx context.Context, opts TestLevelsOptions, workflow, job string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	history, err := opts.Actions.WorkflowRunHistory(ctx, workflow, opts.Branch)
	if err != nil {
		return "", fmt.Errorf("read release CI runs of %s: %w", workflow, err)
	}
	for i := 0; i < len(history.Completed); i++ {
		run := history.Completed[i]
		if run.HeadSHA != opts.Head || run.HeadBranch != opts.Branch || run.Status != "completed" {
			continue
		}
		if run.Conclusion != "success" {
			return fmt.Sprintf("did not run in the release CI: workflow %s run %d concluded %q", workflow, run.ID, run.Conclusion), nil
		}
		jobs, jobsErr := opts.Actions.WorkflowRunJobs(ctx, run.ID)
		if jobsErr != nil {
			return "", fmt.Errorf("read release CI jobs of %s run %d: %w", workflow, run.ID, jobsErr)
		}
		return releaseJobRefusal(jobs, job, run.ID), nil
	}
	return fmt.Sprintf("did not run in the release CI: no run of %s for %.12s", workflow, opts.Head), nil
}

// releaseJobRefusal requires the matched job to exist and every record of that name to succeed.
func releaseJobRefusal(jobs []forge.WorkflowJobRecord, name string, runID int64) string {
	found := false
	for i := 0; i < len(jobs); i++ {
		if jobs[i].Name != name {
			continue
		}
		found = true
		if jobs[i].Conclusion != "success" {
			return fmt.Sprintf("did not run in the release CI: job %s in run %d concluded %q", name, runID, jobs[i].Conclusion)
		}
	}
	if !found {
		return fmt.Sprintf("did not run in the release CI: job %s missing from run %d", name, runID)
	}
	return ""
}

// checkLiveWorkflowHealth queries live workflow run health on branch when opts.Actions
// is available. It returns LiveRunHealthVerified on success, or LiveRunHealthUnmeasured
// (with the query error appended if live audit fails) and any workflow findings.
func checkLiveWorkflowHealth(
	ctx context.Context,
	repoPath string,
	opts TestLevelsOptions,
) (string, map[string]forge.WorkflowRunFinding) {
	if opts.Actions == nil {
		return LiveRunHealthUnmeasured, nil
	}
	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		branch = "main"
	}
	health, err := forge.AuditWorkflowRunHealth(ctx, repoPath, branch, opts.Actions, nil)
	if err != nil {
		return LiveRunHealthUnmeasured + ": " + err.Error(), nil
	}
	byFile := make(map[string]forge.WorkflowRunFinding, len(health.Findings))
	for i := 0; i < len(health.Findings) && i < maxReleaseWorkflowFiles; i++ {
		f := health.Findings[i]
		byFile[f.Workflow] = f
	}
	return LiveRunHealthVerified, byFile
}

// loadBaselinedLevelAnchors loads infractions from baselinePath and returns a set of all
// baselined release test level anchors.
func loadBaselinedLevelAnchors(repoPath, baselinePath string) (map[string]bool, error) {
	if baselinePath == "" {
		baselinePath = filepath.Join(repoPath, ".standards-baseline.json")
	}
	b, err := baseline.LoadBaseline(baselinePath)
	if err != nil {
		return nil, err
	}
	anchors := make(map[string]bool)
	for i := 0; i < len(b.Infractions); i++ {
		inf := b.Infractions[i]
		if inf.RuleID == baseline.ReleaseTestLevelRuleID && inf.Anchor != "" {
			anchors[inf.Anchor] = true
		}
	}
	return anchors, nil
}

// isLevelAnchorBaselined reports whether the test level anchor for profile and level is present
// in the baselined anchors map under any active profile.
func isLevelAnchorBaselined(profile, level string, profiles []string, anchors map[string]bool) bool {
	if anchors[fmt.Sprintf("level:%s:%s", profile, level)] {
		return true
	}
	for i := 0; i < len(profiles) && i < 16; i++ {
		if anchors[fmt.Sprintf("level:%s:%s", profiles[i], level)] {
			return true
		}
	}
	return false
}

// finalizeTestLevelsReport sets final status and detail strings on report based on unbaselined
// findings, live health checks, and baselined infractions.
func finalizeTestLevelsReport(report TestLevelsReport) (TestLevelsReport, error) {
	var unbaselined []string
	baselinedCount := 0
	for i := 0; i < len(report.Findings) && i < config.MaxReleaseTestLevels; i++ {
		f := report.Findings[i]
		if f.Baselined {
			baselinedCount++
			continue
		}
		unbaselined = append(unbaselined, fmt.Sprintf("%s (%s): %s", f.Level, f.Profile, f.Reason))
	}
	suffix := ""
	if report.LiveHealth != "" {
		suffix = fmt.Sprintf(" [%s]", report.LiveHealth)
	}
	if len(unbaselined) == 0 {
		report.Status = TestLevelStatusPass
		report.Detail = fmt.Sprintf("%d required test level(s) verified (%s), %d baselined%s",
			len(report.Levels)-baselinedCount, strings.Join(report.Levels, ", "), baselinedCount, suffix)
		return report, nil
	}
	summary := fmt.Sprintf("%d unbaselined test level gap(s): %s%s; %s",
		len(unbaselined), strings.Join(unbaselined, "; "), suffix, testLevelRemedy)
	report.Detail = summary
	if report.ReleaseMode {
		report.Status = TestLevelStatusFail
		return report, fmt.Errorf("release test levels gate failed: %s", summary)
	}
	report.Status = TestLevelStatusWarn
	return report, nil
}

// testLevelRemedy tells an adopter the two ways out of a gap: a test bound to the level, or
// recording the gaps that existed before the gate through adoption (the ratchet is shrink-only,
// so a plain baseline record cannot add them).
const testLevelRemedy = "bind a test to each level under release.test_levels in .standards.yaml, or run 'praetorctl adopt' to record gaps that predate this gate in .standards-baseline.json"

// TestLevelInfractions converts findings from a TestLevelsReport into
// baseline.Infraction entries for .standards-baseline.json (#1733).
func TestLevelInfractions(findings []TestLevelFinding) []baseline.Infraction {
	out := make([]baseline.Infraction, 0, len(findings))
	for i := 0; i < len(findings) && i < config.MaxReleaseTestLevels; i++ {
		f := findings[i]
		out = append(out, baseline.Infraction{
			RuleID:     baseline.ReleaseTestLevelRuleID,
			FilePath:   config.ManifestFileName,
			LineNumber: 1,
			Anchor:     f.Anchor,
			Message:    fmt.Sprintf("missing release test level %s for profile %s: %s", f.Level, f.Profile, f.Reason),
		})
	}
	baseline.AssignFingerprints(out)
	return out
}

// AuditTestLevelsLine runs AuditTestLevels and formats a single-line status message
// matching AuditE2E for CLI and MCP audit gates (#1733).
func AuditTestLevelsLine(
	ctx context.Context,
	repoPath string,
	manifest *config.Manifest,
	opts TestLevelsOptions,
) (string, TestLevelsReport, error) {
	rep, err := AuditTestLevels(ctx, repoPath, manifest, opts)
	if err != nil {
		return "", rep, fmt.Errorf("[FAIL] Release test levels: %s", rep.Detail)
	}
	switch rep.Status {
	case TestLevelStatusSkip:
		return fmt.Sprintf("[SKIP] Release test levels not checked: %s.", rep.Detail), rep, nil
	case TestLevelStatusWarn:
		return fmt.Sprintf("[WARN] Release test levels: %s", rep.Detail), rep, nil
	default:
		return fmt.Sprintf("[PASS] Release test levels: %s", rep.Detail), rep, nil
	}
}
