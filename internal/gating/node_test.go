// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type commandResponse struct {
	out string
	err error
}

func nodeTestRunner(recorded *[]recordedCommand, responses map[string]commandResponse) commandRunner {
	base := fakeRunner(recorded, "", nil)
	return func(ctx context.Context, cmdDir, name string, args ...string) (string, error) {
		key := strings.TrimSpace(name + " " + strings.Join(args, " "))
		if resp, ok := responses[key]; ok {
			*recorded = append(*recorded, recordedCommand{dir: cmdDir, name: name, args: slices.Clone(args)})
			return resp.out, resp.err
		}
		return base(ctx, cmdDir, name, args...)
	}
}

func seedNodeWorkspace(t *testing.T, dir, lockfile string, scripts map[string]string) string {
	t.Helper()
	writeLockfiles(t, dir)
	scriptsJSON := ""
	if len(scripts) > 0 {
		var parts []string
		for k, v := range scripts {
			parts = append(parts, fmt.Sprintf("%q: %q", k, v))
		}
		slices.Sort(parts)
		scriptsJSON = fmt.Sprintf(`"scripts": {%s},`, strings.Join(parts, ", "))
	}
	content := fmt.Sprintf(`{"name": "test-node", %s "dependencies": {}}`+"\n", scriptsJSON)
	writeFile(t, filepath.Join(dir, PackageJsonFile), content)
	if lockfile != "" {
		writeFile(t, filepath.Join(dir, lockfile), "\n")
	}
	return dir
}

// Positive: npm fixture runs install, audit, test, lint in the worktree and marks node verified.
func TestToolchainStages_Positive_NpmRepositoryRunsNpm(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{
		"test": "vitest run",
		"lint": "eslint .",
	})
	// Add node_modules and .gitignore to verify dependency linking
	nodeModules := filepath.Join(repo, "node_modules", "pkg")
	if err := os.MkdirAll(nodeModules, 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	writeFile(t, filepath.Join(nodeModules, "index.js"), "module.exports = {}\n")
	writeFile(t, filepath.Join(repo, ".gitignore"), "node_modules/\n")

	cfg, recorded := newTestConfig(t, repo, false)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("npm toolchain failed a stage: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed, Message: "node: npm ci --ignore-scripts passed"},
		stageSecurity: {Status: StagePassed, Message: "node: npm audit passed"},
		stageTests:    {Status: StagePassed, Message: "node: npm run test and npm run lint passed"},
	})
	want := []string{
		"npm ci --ignore-scripts",
		"npm audit --audit-level=high --json",
		"npm ci --ignore-scripts",
		"npm run test",
		"npm run lint",
	}
	if lines := commandLines(*recorded); !slices.Equal(lines, want) {
		t.Fatalf("commands = %q, want %q", lines, want)
	}
	assertNpmStageLocations(t, *recorded, repo)
	assertInstallPrecedesTest(t, *recorded, repo)
	if !slices.Equal(cfg.verified, []string{languageNode}) {
		t.Errorf("verified languages = %q, want [node]", cfg.verified)
	}
}

// Positive: pnpm fixture runs install, audit, test, lint and marks node verified.
func TestToolchainStages_Positive_PnpmRepositoryRunsPnpm(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "pnpm-lock.yaml", map[string]string{
		"test": "pnpm test",
		"lint": "pnpm lint",
	})
	cfg, recorded := newTestConfig(t, repo, false)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("pnpm toolchain failed a stage: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed, Message: "node: pnpm install --frozen-lockfile --ignore-scripts passed"},
		stageSecurity: {Status: StagePassed, Message: "node: pnpm audit passed"},
		stageTests:    {Status: StagePassed, Message: "node: pnpm run test and pnpm run lint passed"},
	})
	want := []string{
		"pnpm install --frozen-lockfile --ignore-scripts",
		"pnpm audit --audit-level=high --json",
		"pnpm install --frozen-lockfile --ignore-scripts",
		"pnpm run test",
		"pnpm run lint",
	}
	if lines := commandLines(*recorded); !slices.Equal(lines, want) {
		t.Fatalf("commands = %q, want %q", lines, want)
	}
	if !slices.Equal(cfg.verified, []string{languageNode}) {
		t.Errorf("verified languages = %q, want [node]", cfg.verified)
	}
}

// Positive: Yarn Classic fixture detects v1 and runs Classic flags.
func TestToolchainStages_Positive_YarnClassicRepositoryRunsYarn(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "yarn.lock", map[string]string{
		"test": "jest",
		"lint": "eslint .",
	})
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = nodeTestRunner(recorded, map[string]commandResponse{
		"yarn --version": {out: "1.22.22\n"},
	})
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("yarn classic failed: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed, Message: "node: yarn install --frozen-lockfile --ignore-scripts passed"},
		stageSecurity: {Status: StagePassed, Message: "node: yarn audit passed"},
		stageTests:    {Status: StagePassed, Message: "node: yarn run test and yarn run lint passed"},
	})
	want := []string{
		"yarn --version",
		"yarn install --frozen-lockfile --ignore-scripts",
		"yarn --version",
		"yarn audit --level=high --json",
		"yarn --version",
		"yarn install --frozen-lockfile --ignore-scripts",
		"yarn run test",
		"yarn run lint",
	}
	if lines := commandLines(*recorded); !slices.Equal(lines, want) {
		t.Fatalf("commands = %q, want %q", lines, want)
	}
	if !slices.Equal(cfg.verified, []string{languageNode}) {
		t.Errorf("verified languages = %q, want [node]", cfg.verified)
	}
}

// Positive: Yarn Berry fixture detects v4 and runs Berry flags.
func TestToolchainStages_Positive_YarnBerryRepositoryRunsYarn(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "yarn.lock", map[string]string{
		"test": "jest",
		"lint": "eslint .",
	})
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = nodeTestRunner(recorded, map[string]commandResponse{
		"yarn --version": {out: "4.1.0\n"},
	})
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("yarn berry failed: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed, Message: "node: yarn install --immutable --mode=skip-build passed"},
		stageSecurity: {Status: StagePassed, Message: "node: yarn audit passed"},
		stageTests:    {Status: StagePassed, Message: "node: yarn run test and yarn run lint passed"},
	})
	want := []string{
		"yarn --version",
		"yarn install --immutable --mode=skip-build",
		"yarn --version",
		"yarn npm audit --severity=high --json",
		"yarn --version",
		"yarn install --immutable --mode=skip-build",
		"yarn run test",
		"yarn run lint",
	}
	if lines := commandLines(*recorded); !slices.Equal(lines, want) {
		t.Fatalf("commands = %q, want %q", lines, want)
	}
	if !slices.Equal(cfg.verified, []string{languageNode}) {
		t.Errorf("verified languages = %q, want [node]", cfg.verified)
	}
}

// Positive: Go, Cargo and Node present runs all three and records each verified.
func TestToolchainStages_Positive_MixedGoCargoNode(t *testing.T) {
	t.Setenv(cargoTargetDirEnv, "")
	repo := newHermeticGitRepo(t)
	seedGoModule(t, repo)
	writeLockfiles(t, repo)
	writeFile(t, filepath.Join(repo, CargoLockFile), "version = 4\n")
	writeFile(t, filepath.Join(repo, "Cargo.toml"), "[workspace]\nmembers = []\n")
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{
		"test": "vitest",
	})
	writeFile(t, filepath.Join(repo, GosecConfigFile), "{\"global\":{}}\n")

	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = listingRunner(recorded, repo)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("mixed repo failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("recorded %d stages, want 3", len(got))
	}
	wantVerified := []string{languageGo, languageCargo, languageNode}
	if !slices.Equal(cfg.verified, wantVerified) {
		t.Errorf("verified languages = %q, want %q", cfg.verified, wantVerified)
	}
}

// Negative: two lockfiles refuses naming both files.
func TestToolchainStages_Negative_TwoLockfilesRefused(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{"test": "vitest"})
	writeFile(t, filepath.Join(repo, "yarn.lock"), "\n")

	cfg, _ := newTestConfig(t, repo, false)
	_, err := runPrefetchStage(t.Context(), cfg)
	if err == nil {
		t.Fatalf("prefetch passed with two lockfiles")
	}
	msg := err.Error()
	if !strings.Contains(msg, "package-lock.json") || !strings.Contains(msg, "yarn.lock") {
		t.Errorf("want error naming both lockfiles, got %v", err)
	}
}

// Negative: audit with findings fails the security stage.
func TestToolchainStages_Negative_AuditFindingsFailsSecurity(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{"test": "vitest"})
	cfg, recorded := newTestConfig(t, repo, false)
	auditFindingsJSON := `{"metadata": {"vulnerabilities": {"high": 2, "critical": 1}}}`
	cfg.run = nodeTestRunner(recorded, map[string]commandResponse{
		"npm audit --audit-level=high --json": {out: auditFindingsJSON, err: errors.New("exit status 1")},
	})
	_, err := runSecurityStage(t.Context(), cfg)
	if err == nil {
		t.Fatalf("security passed with audit findings")
	}
	if !strings.Contains(err.Error(), "found 2 high and 1 critical vulnerabilities") {
		t.Errorf("want error naming vulnerabilities, got %v", err)
	}
}

// Negative: audit with Yarn NDJSON findings fails the security stage.
func TestToolchainStages_Negative_AuditYarnNDJSONFindingsFailsSecurity(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "yarn.lock", map[string]string{"test": "vitest"})
	cfg, recorded := newTestConfig(t, repo, false)
	yarnNDJSON := "{\"type\":\"auditSummary\",\"data\":{\"vulnerabilities\":{\"high\":1,\"critical\":0}}}\n"
	cfg.run = nodeTestRunner(recorded, map[string]commandResponse{
		"yarn --version":                 {out: "1.22.22\n"},
		"yarn audit --level=high --json": {out: yarnNDJSON, err: errors.New("exit status 8")},
	})
	_, err := runSecurityStage(t.Context(), cfg)
	if err == nil {
		t.Fatalf("security passed with yarn audit findings")
	}
	if !strings.Contains(err.Error(), "found 1 high and 0 critical vulnerabilities") {
		t.Errorf("want error naming vulnerabilities, got %v", err)
	}
}

// Negative: audit network failure reports network requirement.
func TestToolchainStages_Negative_AuditNetworkErrorFailsSecurity(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{"test": "vitest"})
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = nodeTestRunner(recorded, map[string]commandResponse{
		"npm audit --audit-level=high --json": {out: "npm error ENOTFOUND registry.npmjs.org", err: errors.New("exit status 1")},
	})
	_, err := runSecurityStage(t.Context(), cfg)
	if err == nil {
		t.Fatalf("security passed despite network error")
	}
	if !strings.Contains(err.Error(), "requires network access but failed") {
		t.Errorf("want network failure message, got %v", err)
	}
}

// Negative: failing test script fails test stage naming the script.
func TestToolchainStages_Negative_TestScriptFailureNamesScript(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{
		"test": "exit 1",
		"lint": "eslint .",
	})
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = nodeTestRunner(recorded, map[string]commandResponse{
		"npm run test": {out: "Tests failed: 1", err: errors.New("exit status 1")},
	})
	_, err := runTestStage(t.Context(), cfg)
	if err == nil {
		t.Fatalf("test stage passed despite failing test script")
	}
	if !strings.Contains(err.Error(), "npm run test failed in") {
		t.Errorf("want error naming test script, got %v", err)
	}
}

// Negative: missing manager binary skips stage with hint, and receipt is refused when nothing verified.
func TestToolchainStages_Negative_MissingManagerBinaryRefusesReceipt(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{"test": "vitest"})
	cfg, _ := newTestConfig(t, repo, false)
	cfg.lookPath = lookPathWithout("npm")

	msg, err := runPrefetchStage(t.Context(), cfg)
	if err == nil || partStatus(err) != StageSkipped {
		t.Fatalf("prefetch want skipped, got msg %q err %v", msg, err)
	}
	if !strings.Contains(err.Error(), "install Node.js and npm from https://nodejs.org") {
		t.Errorf("want install hint in prefetch error, got %v", err)
	}

	refused(t, cfg, "package.json and its lockfile are present, but no Node stage ran here")
}

// Boundary: package.json without test and lint scripts is not applicable and not verified.
func TestToolchainStages_Boundary_NoScriptsNotApplicableNotVerified(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{
		"build": "vite build",
	})
	cfg, _ := newTestConfig(t, repo, false)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("stages failed: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed, Message: "node: npm ci --ignore-scripts passed"},
		stageSecurity: {Status: StagePassed, Message: "node: npm audit passed"},
		stageTests:    {Status: StageNotApplicable, Message: "node: no test or lint script declared in package.json: Node tests skipped"},
	})
	if slices.Contains(cfg.verified, languageNode) {
		t.Errorf("node should not count as verified when tests were not applicable: %v", cfg.verified)
	}
	refused(t, cfg, "Prefetch & Lockfiles, Security & SCA Scan and Race-Detector Tests each ran nothing")
}

// Boundary: npm init test script placeholder is not applicable.
func TestToolchainStages_Boundary_NpmInitTestPlaceholderNotApplicable(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{
		"test": `echo "Error: no test specified" && exit 1`,
	})
	cfg, _ := newTestConfig(t, repo, false)
	_, err := runTestStage(t.Context(), cfg)
	if err == nil || partStatus(err) != StageNotApplicable {
		t.Fatalf("want StageNotApplicable for npm init placeholder, got %v", err)
	}
}

// Boundary: only test script declared runs only test script.
func TestToolchainStages_Boundary_OnlyTestScriptRuns(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{
		"test": "vitest",
	})
	cfg, recorded := newTestConfig(t, repo, false)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("stages failed: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed},
		stageSecurity: {Status: StagePassed},
		stageTests:    {Status: StagePassed, Message: "node: npm run test passed"},
	})
	want := []string{
		"npm ci --ignore-scripts",
		"npm audit --audit-level=high --json",
		"npm ci --ignore-scripts",
		"npm run test",
	}
	if lines := commandLines(*recorded); !slices.Equal(lines, want) {
		t.Fatalf("commands = %q, want %q", lines, want)
	}
}

// Boundary: only lint script declared runs only lint script.
func TestToolchainStages_Boundary_OnlyLintScriptRuns(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{
		"lint": "eslint .",
	})
	cfg, recorded := newTestConfig(t, repo, false)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("stages failed: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StagePassed},
		stageSecurity: {Status: StagePassed},
		stageTests:    {Status: StagePassed, Message: "node: npm run lint passed"},
	})
	want := []string{
		"npm ci --ignore-scripts",
		"npm audit --audit-level=high --json",
		"npm ci --ignore-scripts",
		"npm run lint",
	}
	if lines := commandLines(*recorded); !slices.Equal(lines, want) {
		t.Fatalf("commands = %q, want %q", lines, want)
	}
}

// Boundary: dry run skips all node stages without running commands.
func TestToolchainStages_Boundary_DryRunSkipsNode(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{"test": "vitest"})
	cfg, recorded := newTestConfig(t, repo, true)
	got, err := runToolchainStages(t, cfg)
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	wantStages(t, got, map[string]StageResult{
		stagePrefetch: {Status: StageSkipped, Message: "node: dry run: npm install not run"},
		stageSecurity: {Status: StageSkipped, Message: "node: dry run: npm audit not run"},
		stageTests:    {Status: StageSkipped, Message: "node: dry run: npm test and lint not run"},
	})
	if len(*recorded) != 0 {
		t.Fatalf("dry run invoked commands: %+v", *recorded)
	}
}

func assertNpmStageLocations(t *testing.T, recorded []recordedCommand, repo string) {
	t.Helper()
	for i, c := range recorded {
		inWorktree := strings.Contains(c.dir, "worktrees")
		if i == 1 {
			if inWorktree || c.dir != repo {
				t.Errorf("%s %v ran in %s; audit belongs in repository", c.name, c.args, c.dir)
			}
		} else if !inWorktree || c.dir == repo {
			t.Errorf("%s %v ran in %s; prefetch install and tests belong in stage worktree", c.name, c.args, c.dir)
		}
	}
}

func isNpmCi(c recordedCommand) bool {
	return c.name == "npm" && len(c.args) > 0 && c.args[0] == "ci"
}

func isNpmTest(c recordedCommand) bool {
	return c.name == "npm" && len(c.args) >= 2 && c.args[0] == "run" && c.args[1] == "test"
}

func validInstallOrder(testIdx, runIdx int) bool {
	return testIdx != -1 && runIdx != -1 && testIdx < runIdx
}

func assertInstallPrecedesTest(t *testing.T, recorded []recordedCommand, repo string) {
	t.Helper()
	var installDirs []string
	testInstallIdx, runTestIdx := -1, -1
	for i, c := range recorded {
		if isNpmCi(c) {
			installDirs = append(installDirs, c.dir)
			if i > 0 {
				testInstallIdx = i
			}
		}
		if isNpmTest(c) {
			runTestIdx = i
		}
	}
	if len(installDirs) < 2 {
		t.Fatalf("expected at least 2 install commands (prefetch and test), got %d", len(installDirs))
	}
	for _, dir := range installDirs {
		if dir == repo {
			t.Errorf("install command ran in repoDir %s, must run in stage worktree", dir)
		}
	}
	if !validInstallOrder(testInstallIdx, runTestIdx) {
		t.Errorf("test install (index %d) must precede run test (index %d)", testInstallIdx, runTestIdx)
	}
}

// Negative: install commands must run in stage worktree, and test install must precede run test.
func TestToolchainStages_InstallRunsInWorktreeAndPrecedesRunTest(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{
		"test": "vitest run",
	})
	cfg, recorded := newTestConfig(t, repo, false)
	if _, err := runPrefetchStage(t.Context(), cfg); err != nil {
		t.Fatalf("prefetch failed: %v", err)
	}
	if _, err := runTestStage(t.Context(), cfg); err != nil {
		t.Fatalf("test stage failed: %v", err)
	}
	assertInstallPrecedesTest(t, *recorded, repo)
}

// Negative: failing yarn --version refuses to guess classic and fails the part.
func TestToolchainStages_Negative_YarnVersionFailureRefused(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "yarn.lock", map[string]string{"test": "jest"})
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = nodeTestRunner(recorded, map[string]commandResponse{
		"yarn --version": {out: "", err: errors.New("exit status 1")},
	})
	_, err := runPrefetchStage(t.Context(), cfg)
	if err == nil {
		t.Fatalf("prefetch passed despite failing yarn --version")
	}
	if !strings.Contains(err.Error(), "cannot tell the Yarn major version:") {
		t.Errorf("want error stating cannot tell Yarn major version, got: %v", err)
	}
}

// Negative: unparsable yarn --version refuses to guess classic and fails the part.
func TestToolchainStages_Negative_YarnVersionUnparsableRefused(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "yarn.lock", map[string]string{"test": "jest"})
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = nodeTestRunner(recorded, map[string]commandResponse{
		"yarn --version": {out: "not-a-valid-version\n"},
	})
	_, err := runPrefetchStage(t.Context(), cfg)
	if err == nil {
		t.Fatalf("prefetch passed despite unparsable yarn --version")
	}
	if !strings.Contains(err.Error(), "cannot tell the Yarn major version:") {
		t.Errorf("want error stating cannot tell Yarn major version, got: %v", err)
	}
}

// Positive: supportedLanguages names Node with its markers in nothing-verified refusal.
func TestSupportedLanguages_NamesNodeWithMarkers(t *testing.T) {
	repo := newHermeticGitRepo(t)
	cfg, _ := newTestConfig(t, repo, false)
	cfg.rep.WorktreeClean = true
	_, err := runReceiptStage(t.Context(), cfg)
	if err == nil || !errors.Is(err, ErrNothingVerified) {
		t.Fatalf("want ErrNothingVerified, got %v", err)
	}
	wantMarker := "Node (package.json with package-lock.json, pnpm-lock.yaml or yarn.lock)"
	if !strings.Contains(err.Error(), wantMarker) {
		t.Errorf("refusal message does not name Node with its markers:\ngot:  %s\nwant: %s", err.Error(), wantMarker)
	}
}

// Negative: audit error containing 'offline' word is not mistaken for a network error.
func TestToolchainStages_Negative_AuditOfflineTextNotNetworkError(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "package-lock.json", map[string]string{"test": "vitest"})
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = nodeTestRunner(recorded, map[string]commandResponse{
		"npm audit --audit-level=high --json": {
			out: "vulnerability found in offline-cache package",
			err: errors.New("exit status 1"),
		},
	})
	_, err := runSecurityStage(t.Context(), cfg)
	if err == nil {
		t.Fatalf("security passed despite audit failure")
	}
	if strings.Contains(err.Error(), "requires network access but failed") {
		t.Errorf("audit failure with 'offline' text was incorrectly classified as network error: %v", err)
	}
	if !strings.Contains(err.Error(), "npm audit --audit-level=high --json failed:") {
		t.Errorf("want command failure message, got: %v", err)
	}
}

// Negative: Yarn Berry audit non-zero exit code fails the security stage.
func TestToolchainStages_Negative_YarnBerryAuditNonZeroFailsSecurity(t *testing.T) {
	repo := newHermeticGitRepo(t)
	seedNodeWorkspace(t, repo, "yarn.lock", map[string]string{"test": "jest"})
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.run = nodeTestRunner(recorded, map[string]commandResponse{
		"yarn --version": {out: "4.1.0\n"},
		"yarn npm audit --severity=high --json": {
			out: `{"value":"dummy advisory","children":{}}`,
			err: errors.New("exit status 1"),
		},
	})
	_, err := runSecurityStage(t.Context(), cfg)
	if err == nil {
		t.Fatalf("security passed despite berry audit exiting non-zero")
	}
	if !strings.Contains(err.Error(), "yarn npm audit --severity=high --json failed: exit status 1") {
		t.Errorf("want command failure on berry non-zero exit, got: %v", err)
	}
}
