// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package release

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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

	maxRepoScanFiles        = 10000
	maxLevelFileBytes       = 1 << 20
	maxWorkflowJobs         = 64
	maxReleaseWorkflowFiles = ghworkflow.MaxFiles
)

// TestLevelsOptions configures AuditTestLevels (#1733).
type TestLevelsOptions struct {
	FlagRelease  bool
	BaselinePath string
	Actions      forge.ActionsReader
	Branch       string
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
	return evaluateLevelBindings(ctx, repoPath, bindings, opts, report)
}

func ensureManifest(repoPath string, manifest *config.Manifest) (*config.Manifest, error) {
	if manifest != nil {
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

func appendUniqueProfile(profiles []string, p string) []string {
	p = strings.TrimSpace(p)
	if p != "" && !slices.Contains(profiles, p) {
		return append(profiles, p)
	}
	return profiles
}

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

func evaluateLevelBindings(
	ctx context.Context,
	repoPath string,
	bindings []resolvedLevelBinding,
	opts TestLevelsOptions,
	report TestLevelsReport,
) (TestLevelsReport, error) {
	files, err := listRepositoryFiles(ctx, repoPath)
	if err != nil {
		return report, err
	}
	workflows, auxScripts, err := loadRepositoryWorkflows(ctx, repoPath)
	if err != nil {
		return report, err
	}
	liveStatus, liveByWorkflow := checkLiveWorkflowHealth(ctx, repoPath, opts)
	report.LiveHealth = liveStatus

	baselinedAnchors, err := loadBaselinedLevelAnchors(repoPath, opts.BaselinePath)
	if err != nil {
		return report, err
	}
	for i := 0; i < len(bindings) && i < config.MaxReleaseTestLevels; i++ {
		b := bindings[i]
		reason := checkSingleLevel(ctx, repoPath, b, files, workflows, auxScripts, liveByWorkflow)
		if reason == "" {
			continue
		}
		anchor := fmt.Sprintf("level:%s:%s", b.Profile, b.Level)
		isBaselined := isLevelAnchorBaselined(b.Profile, b.Level, report.Profiles, baselinedAnchors)
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

func checkSingleLevel(
	ctx context.Context,
	repoPath string,
	b resolvedLevelBinding,
	files []string,
	workflows []parsedWorkflowFile,
	auxScripts string,
	liveByWorkflow map[string]forge.WorkflowRunFinding,
) string {
	matchedFiles := matchFilesForGlobList(files, b.Spec.Paths)
	if len(matchedFiles) == 0 {
		return fmt.Sprintf("no tracked file matches paths %v", b.Spec.Paths)
	}
	if b.Spec.Pattern != "" {
		ok, err := anyFileMatchesPattern(ctx, repoPath, matchedFiles, b.Spec.Pattern)
		if err != nil || !ok {
			return fmt.Sprintf("no matched file satisfies pattern %q", b.Spec.Pattern)
		}
	}
	wfName, ok := findRunnableWorkflowForLevel(b, workflows, auxScripts)
	if !ok {
		return "no runnable workflow job executes this level"
	}
	if finding, hasLive := liveByWorkflow[wfName]; hasLive && finding.Health != forge.WorkflowHealthy {
		return fmt.Sprintf("workflow %s run health is %s", wfName, finding.Health)
	}
	return ""
}

func matchFilesForGlobList(files, globs []string) []string {
	var matched []string
	for i := 0; i < len(files) && i < maxRepoScanFiles; i++ {
		f := files[i]
		if matchesAnyGlob(f, globs) {
			matched = append(matched, f)
		}
	}
	return matched
}

func anyFileMatchesPattern(ctx context.Context, repoPath string, relPaths []string, pattern string) (bool, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, err
	}
	for i := 0; i < len(relPaths) && i < maxRepoScanFiles; i++ {
		data, readErr := contextopt.ReadSnapshot(ctx, filepath.Join(repoPath, filepath.FromSlash(relPaths[i])))
		if readErr != nil || len(data) > maxLevelFileBytes {
			continue
		}
		if re.Match(data) {
			return true, nil
		}
	}
	return false, nil
}

func findRunnableWorkflowForLevel(
	b resolvedLevelBinding,
	workflows []parsedWorkflowFile,
	auxScripts string,
) (string, bool) {
	for i := 0; i < len(workflows) && i < maxReleaseWorkflowFiles; i++ {
		wf := workflows[i]
		if workflowRunsLevel(b, wf.Workflow, auxScripts) {
			return wf.Name, true
		}
	}
	return "", false
}

func workflowRunsLevel(b resolvedLevelBinding, wf ghworkflow.Spec, auxScripts string) bool {
	if len(wf.Jobs) == 0 {
		return false
	}
	jobIDs := make([]string, 0, len(wf.Jobs))
	for id := range wf.Jobs {
		jobIDs = append(jobIDs, id)
	}
	slices.Sort(jobIDs)
	for i := 0; i < len(jobIDs) && i < maxWorkflowJobs; i++ {
		id := jobIDs[i]
		job := wf.Jobs[id]
		if ghworkflow.NeverRuns(job.If) || advisoryContinueOnError(job.ContinueOnError) {
			continue
		}
		if jobMatchesLevel(b, id, job, auxScripts) {
			return true
		}
	}
	return false
}

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

func invokesAuxBuildRunner(stepsText string) bool {
	for _, runner := range []string{"make", "npm ", "pnpm ", "yarn ", "bun "} {
		if strings.Contains(stepsText, runner) {
			return true
		}
	}
	return false
}

func advisoryContinueOnError(raw string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	return trimmed == "true" || strings.Contains(trimmed, "true")
}

func loadRepositoryWorkflows(ctx context.Context, repoPath string) ([]parsedWorkflowFile, string, error) {
	wfDir := filepath.Join(repoPath, ".github", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, loadAuxBuildScripts(ctx, repoPath), nil
		}
		return nil, "", fmt.Errorf("read workflows directory: %w", err)
	}
	var parsed []parsedWorkflowFile
	for i := 0; i < len(entries) && i < maxReleaseWorkflowFiles; i++ {
		entry := entries[i]
		if entry.IsDir() || !isYAMLFilename(entry.Name()) {
			continue
		}
		fullPath := filepath.Join(wfDir, entry.Name())
		data, readErr := contextopt.ReadSnapshot(ctx, fullPath)
		if readErr != nil || len(data) > maxLevelFileBytes {
			continue
		}
		wf, parseErr := ghworkflow.Parse(data)
		if parseErr != nil {
			continue
		}
		parsed = append(parsed, parsedWorkflowFile{
			Name:     entry.Name(),
			RelPath:  filepath.ToSlash(filepath.Join(".github", "workflows", entry.Name())),
			Workflow: wf,
		})
	}
	return parsed, loadAuxBuildScripts(ctx, repoPath), nil
}

func isYAMLFilename(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}

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

func listRepositoryFiles(ctx context.Context, repoPath string) ([]string, error) {
	res, err := util.RunGitProbe(ctx, repoPath, maxGitLogBytes, "ls-files", "--cached", "--others", "--exclude-standard")
	if err == nil {
		files := filterExistingGitFiles(repoPath, string(res.Stdout))
		if len(files) > 0 {
			return files, nil
		}
	}
	return walkRepositoryFilesBounded(repoPath)
}

func filterExistingGitFiles(repoPath, gitOut string) []string {
	lines := strings.Split(strings.TrimSpace(gitOut), "\n")
	var files []string
	for i := 0; i < len(lines) && i < maxRepoScanFiles; i++ {
		rel := strings.TrimSpace(lines[i])
		if rel == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(repoPath, filepath.FromSlash(rel)))
		if err == nil && info.Mode().IsRegular() {
			files = append(files, filepath.ToSlash(rel))
		}
	}
	return files
}

func walkRepositoryFilesBounded(repoPath string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".workingdir" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= maxRepoScanFiles || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(repoPath, path)
		if err == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	return files, err
}

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
		return LiveRunHealthUnmeasured, nil
	}
	byFile := make(map[string]forge.WorkflowRunFinding, len(health.Findings))
	for i := 0; i < len(health.Findings) && i < maxReleaseWorkflowFiles; i++ {
		f := health.Findings[i]
		byFile[f.Workflow] = f
	}
	return LiveRunHealthVerified, byFile
}

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
