// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

// The Node parts of the toolchain stages. Each runs only where package.json and exactly one
// supported lockfile (package-lock.json, pnpm-lock.yaml, yarn.lock) are present at the
// repository root.

const (
	// PackageJsonFile is the Node manifest file name.
	PackageJsonFile = "package.json"
	// NodeAuditTimeout bounds the Node package manager audit command.
	NodeAuditTimeout = 3 * time.Minute
	// maxNodeOutputBytes bounds how much of a failed command output a stage message carries.
	maxNodeOutputBytes = util.MaxErrorBodyBytes
)

const (
	nodeManagerNpm  = "npm"
	nodeManagerPnpm = "pnpm"
	nodeManagerYarn = "yarn"
)

// nodeLockfiles defines the supported lockfiles and their package manager in fixed order.
var nodeLockfiles = []struct {
	file    string
	manager string
}{
	{"package-lock.json", nodeManagerNpm},
	{"pnpm-lock.yaml", nodeManagerPnpm},
	{"yarn.lock", nodeManagerYarn},
}

// inspectNodeLockfiles finds every supported Node lockfile present at the repository root.
func inspectNodeLockfiles(repoDir string) []string {
	if !util.FileExists(filepath.Join(repoDir, PackageJsonFile)) {
		return nil
	}
	var found []string
	for i := 0; i < len(nodeLockfiles); i++ {
		if util.FileExists(filepath.Join(repoDir, nodeLockfiles[i].file)) {
			found = append(found, nodeLockfiles[i].file)
		}
	}
	return found
}

// hasNode reports whether package.json and at least one recognized lockfile exist at repoDir.
func hasNode(repoDir string) bool {
	return len(inspectNodeLockfiles(repoDir)) > 0
}

// hasSingleNodeLockfile reports whether package.json and exactly one lockfile exist at repoDir.
func hasSingleNodeLockfile(repoDir string) bool {
	return len(inspectNodeLockfiles(repoDir)) == 1
}

// resolveNodeManager resolves which package manager to run based on the present lockfiles.
func resolveNodeManager(repoDir string) (string, string, error) {
	if !util.FileExists(filepath.Join(repoDir, PackageJsonFile)) {
		return "", "", fmt.Errorf("no %s found in %s", PackageJsonFile, repoDir)
	}
	found := inspectNodeLockfiles(repoDir)
	if len(found) == 0 {
		return "", "", fmt.Errorf("%s has no lockfile (expected package-lock.json, pnpm-lock.yaml, or yarn.lock)", PackageJsonFile)
	}
	if len(found) > 1 {
		return "", "", fmt.Errorf("%s has multiple lockfiles: %s; keep exactly one", PackageJsonFile, strings.Join(found, ", "))
	}
	lockfile := found[0]
	for i := 0; i < len(nodeLockfiles); i++ {
		if nodeLockfiles[i].file == lockfile {
			return nodeLockfiles[i].manager, lockfile, nil
		}
	}
	return "", "", fmt.Errorf("unrecognised lockfile %s", lockfile)
}

func nodeInstallHint(manager string) string {
	switch manager {
	case nodeManagerNpm:
		return "install Node.js and npm from https://nodejs.org"
	case nodeManagerPnpm:
		return "install pnpm from https://pnpm.io"
	case nodeManagerYarn:
		return "install Yarn from https://yarnpkg.com"
	default:
		return "install the Node toolchain"
	}
}

func requireNodeManager(cfg *stageConfig, manager string) error {
	if _, err := cfg.lookPath(manager); err != nil {
		return skipped(fmt.Sprintf("not run: %s is not on PATH (%s)", manager, nodeInstallHint(manager)))
	}
	return nil
}

func isYarnClassic(ctx context.Context, cfg *stageConfig) (bool, error) {
	qCtx, cancel := context.WithTimeout(ctx, GitQueryTimeout)
	defer cancel()
	out, err := cfg.run(qCtx, cfg.repoDir, "yarn", "--version")
	if err != nil {
		return false, fmt.Errorf("cannot tell the Yarn major version: %w", err)
	}
	v := strings.TrimSpace(out)
	v = strings.TrimPrefix(v, "v")
	majorStr, _, _ := strings.Cut(v, ".")
	major, err := strconv.Atoi(majorStr)
	if err != nil || major < 1 {
		return false, fmt.Errorf("cannot tell the Yarn major version: unrecognised version output %q", strings.TrimSpace(out))
	}
	return major == 1, nil
}

// nodeInstallArgs returns the install arguments for manager.
//
// The exact flags are read from each manager's own CLI help:
//
//	npm ci --help:
//	  --ignore-scripts: "If true, npm does not run scripts specified in package.json files."
//	pnpm install --help:
//	  --frozen-lockfile: "Don't generate a lockfile, and fail if an update to it is needed."
//	  --ignore-scripts: "Don't run lifecycle scripts of the project or its dependencies."
//	yarn install --help (v1 Classic):
//	  --frozen-lockfile: "Don't generate a yarn.lock lockfile and fail if an update is needed"
//	  --ignore-scripts: "Do not execute any scripts defined in the project's package.json and its dependencies"
//	yarn install --help (v2+ Berry):
//	  --immutable: "Abort with an error exit code if the lockfile was to be modified"
//	  --mode=skip-build: "skip-build will not run the build scripts at all"
func nodeInstallArgs(manager string, yarnClassic bool) []string {
	switch manager {
	case nodeManagerNpm:
		return []string{"ci", "--ignore-scripts"}
	case nodeManagerPnpm:
		return []string{"install", "--frozen-lockfile", "--ignore-scripts"}
	case nodeManagerYarn:
		if yarnClassic {
			return []string{"install", "--frozen-lockfile", "--ignore-scripts"}
		}
		return []string{"install", "--immutable", "--mode=skip-build"}
	default:
		return nil
	}
}

// runNodePrefetch is the prefetch stage's Node part: installs locked dependencies without lifecycle scripts in an isolated worktree.
func runNodePrefetch(ctx context.Context, cfg *stageConfig) (string, error) {
	manager, _, err := resolveNodeManager(cfg.repoDir)
	if err != nil {
		return "", err
	}
	if cfg.dryRun {
		return "", skipped(fmt.Sprintf("dry run: %s install not run", manager))
	}
	if reqErr := requireNodeManager(cfg, manager); reqErr != nil {
		return "", reqErr
	}
	var classic bool
	if manager == nodeManagerYarn {
		var yarnErr error
		classic, yarnErr = isYarnClassic(ctx, cfg)
		if yarnErr != nil {
			return "", yarnErr
		}
	}
	args := nodeInstallArgs(manager, classic)
	what := manager + " " + strings.Join(args, " ")
	err = inStageWorktree(ctx, cfg, DefaultPrefetchTimeout, func(tCtx context.Context, dir string) error {
		out, runErr := cfg.run(tCtx, dir, manager, args...)
		if runErr != nil {
			return commandFailure(ctx, tCtx, what, DefaultPrefetchTimeout, out, maxNodeOutputBytes, runErr)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return what + " passed", nil
}

// nodeAuditArgs returns the audit arguments for manager.
//
// The exact flags are read from each manager's own CLI help:
//
//	npm audit --help:
//	  --audit-level: "The minimum level of vulnerability for npm audit to exit with a non-zero exit code."
//	  --json: "Whether or not to output JSON data, rather than the normal output."
//	pnpm audit --help:
//	  --audit-level: "Only print advisories with severity greater than or equal to this level [possible values: info, low, moderate, high, critical]"
//	  --json: "Output audit report in JSON format"
//	yarn audit --help (v1 Classic):
//	  --level: "The minimum level of vulnerability for yarn audit to exit with a non-zero exit code"
//	  --json: "Format the output as an NDJSON stream"
//	yarn npm audit --help (v2+ Berry):
//	  --severity: "Minimal severity requested for packages to be displayed"
//	  --json: "Format the output as an NDJSON stream"
func nodeAuditArgs(manager string, yarnClassic bool) []string {
	switch manager {
	case nodeManagerNpm:
		return []string{"audit", "--audit-level=high", "--json"}
	case nodeManagerPnpm:
		return []string{"audit", "--audit-level=high", "--json"}
	case nodeManagerYarn:
		if yarnClassic {
			return []string{"audit", "--level=high", "--json"}
		}
		return []string{"npm", "audit", "--severity=high", "--json"}
	default:
		return nil
	}
}

func isNetworkFailure(out string, err error) bool {
	combined := strings.ToLower(out)
	if err != nil {
		combined += " " + strings.ToLower(err.Error())
	}
	markers := []string{
		"enotfound",
		"eai_again",
		"econnrefused",
		"econnreset",
		"etimedout",
		"enetunreach",
		"getaddrinfo",
		"fetch failed",
		"network error",
		"network failure",
		"network access required",
		"request to http",
		"failed to fetch",
	}
	for i := 0; i < len(markers); i++ {
		if strings.Contains(combined, markers[i]) {
			return true
		}
	}
	return false
}

type npmAuditReport struct {
	Metadata struct {
		Vulnerabilities struct {
			High     int `json:"high"`
			Critical int `json:"critical"`
			Total    int `json:"total"`
		} `json:"vulnerabilities"`
	} `json:"metadata"`
}

type yarnAuditSummary struct {
	Type string `json:"type"`
	Data struct {
		Vulnerabilities struct {
			High     int `json:"high"`
			Critical int `json:"critical"`
		} `json:"vulnerabilities"`
	} `json:"data"`
}

func checkNpmAuditFindings(trimmed string) (string, bool) {
	var report npmAuditReport
	if err := json.Unmarshal([]byte(trimmed), &report); err != nil {
		return "", false
	}
	high := report.Metadata.Vulnerabilities.High
	crit := report.Metadata.Vulnerabilities.Critical
	if high > 0 || crit > 0 {
		return fmt.Sprintf("found %d high and %d critical vulnerabilities", high, crit), true
	}
	return "", false
}

// checkYarnAuditFindings parses Yarn Classic's NDJSON audit stream for auditSummary records.
// Yarn Berry's `yarn npm audit --json` output is not the classic auditSummary stream, so
// checkYarnAuditFindings sees no finding there and the verdict rests on the exit code alone.
func checkYarnAuditFindings(trimmed string) (string, bool) {
	lines := strings.Split(trimmed, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var summary yarnAuditSummary
		if err := json.Unmarshal([]byte(line), &summary); err == nil && summary.Type == "auditSummary" {
			high := summary.Data.Vulnerabilities.High
			crit := summary.Data.Vulnerabilities.Critical
			if high > 0 || crit > 0 {
				return fmt.Sprintf("found %d high and %d critical vulnerabilities", high, crit), true
			}
		}
	}
	return "", false
}

func checkAuditOutputFindings(manager, out string) (string, bool) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return "", false
	}
	if manager == nodeManagerNpm || manager == nodeManagerPnpm {
		return checkNpmAuditFindings(trimmed)
	}
	if manager == nodeManagerYarn {
		return checkYarnAuditFindings(trimmed)
	}
	return "", false
}

func handleAuditError(ctx, aCtx context.Context, manager, what, out string, runErr error) error {
	if aCtx.Err() != nil && ctx.Err() == nil {
		return commandFailure(ctx, aCtx, what, NodeAuditTimeout, out, maxNodeOutputBytes, runErr)
	}
	excerpt := util.TruncateExcerpt(out, maxNodeOutputBytes)
	if isNetworkFailure(out, runErr) {
		return fmt.Errorf("%s audit requires network access but failed: %w: %s", manager, runErr, excerpt)
	}
	if summary, hasFindings := checkAuditOutputFindings(manager, out); hasFindings {
		return fmt.Errorf("%s found vulnerabilities: %s", what, summary)
	}
	return fmt.Errorf("%s failed: %w: %s", what, runErr, excerpt)
}

// runNodeSecurity is the security stage's Node part: runs package manager audit at severity high.
func runNodeSecurity(ctx context.Context, cfg *stageConfig) (string, error) {
	manager, _, err := resolveNodeManager(cfg.repoDir)
	if err != nil {
		return "", err
	}
	if cfg.dryRun {
		return "", skipped(fmt.Sprintf("dry run: %s audit not run", manager))
	}
	if reqErr := requireNodeManager(cfg, manager); reqErr != nil {
		return "", reqErr
	}
	var classic bool
	if manager == nodeManagerYarn {
		var yarnErr error
		classic, yarnErr = isYarnClassic(ctx, cfg)
		if yarnErr != nil {
			return "", yarnErr
		}
	}
	args := nodeAuditArgs(manager, classic)
	aCtx, cancel := context.WithTimeout(ctx, NodeAuditTimeout)
	defer cancel()
	what := manager + " " + strings.Join(args, " ")
	out, runErr := cfg.run(aCtx, cfg.repoDir, manager, args...)
	if runErr != nil {
		return "", handleAuditError(ctx, aCtx, manager, what, out, runErr)
	}
	if summary, hasFindings := checkAuditOutputFindings(manager, out); hasFindings {
		return "", fmt.Errorf("%s found vulnerabilities: %s", what, summary)
	}
	return manager + " audit passed", nil
}

func parseNodeManifest(repoDir string) (nodemanifest.Manifest, error) {
	manifestPath := filepath.Join(repoDir, PackageJsonFile)
	data, err := util.ReadFileLimited(manifestPath, nodemanifest.MaxManifestBytes)
	if err != nil {
		return nodemanifest.Manifest{}, fmt.Errorf("read %s: %w", PackageJsonFile, err)
	}
	manifest, err := nodemanifest.ParseManifest(data)
	if err != nil {
		return nodemanifest.Manifest{}, fmt.Errorf("parse %s: %w", PackageJsonFile, err)
	}
	return manifest, nil
}

func runNodeScript(tCtx context.Context, cfg *stageConfig, bound time.Duration, dir, manager, script string) error {
	what := manager + " run " + script
	out, err := cfg.run(tCtx, dir, manager, "run", script)
	if err == nil {
		return nil
	}
	excerpt := util.TruncateExcerpt(out, maxNodeOutputBytes)
	if cutErr := cutError(tCtx, what, bound, dir, fmt.Sprintf("%s [%v]", excerpt, err)); cutErr != nil {
		return cutErr
	}
	return fmt.Errorf("%s failed in %s: %s (%w)", what, dir, excerpt, err)
}

func runNodeWorktreeScripts(tCtx context.Context, cfg *stageConfig, bound time.Duration, dir, manager string, installArgs []string, hasTest, hasLint bool) error {
	installWhat := manager + " " + strings.Join(installArgs, " ")
	out, err := cfg.run(tCtx, dir, manager, installArgs...)
	if err != nil {
		excerpt := util.TruncateExcerpt(out, maxNodeOutputBytes)
		if cutErr := cutError(tCtx, installWhat, bound, dir, fmt.Sprintf("%s [%v]", excerpt, err)); cutErr != nil {
			return cutErr
		}
		return fmt.Errorf("%s failed in %s: %s (%w)", installWhat, dir, excerpt, err)
	}
	if hasTest {
		if runErr := runNodeScript(tCtx, cfg, bound, dir, manager, "test"); runErr != nil {
			return runErr
		}
	}
	if hasLint {
		if runErr := runNodeScript(tCtx, cfg, bound, dir, manager, "lint"); runErr != nil {
			return runErr
		}
	}
	return nil
}

func resolveNodeTestPlan(repoDir string) (manager string, hasTest, hasLint bool, err error) {
	manager, _, err = resolveNodeManager(repoDir)
	if err != nil {
		return "", false, false, err
	}
	manifest, err := parseNodeManifest(repoDir)
	if err != nil {
		return "", false, false, err
	}
	hasTest = nodemanifest.ScriptRuns(manifest.Scripts["test"])
	hasLint = nodemanifest.ScriptRuns(manifest.Scripts["lint"])
	return manager, hasTest, hasLint, nil
}

func nodeTestsPassedMessage(manager string, hasTest, hasLint bool, note string) string {
	var ran []string
	if hasTest {
		ran = append(ran, manager+" run test")
	}
	if hasLint {
		ran = append(ran, manager+" run lint")
	}
	msg := strings.Join(ran, " and ") + " passed"
	if note != "" {
		msg += "; " + note
	}
	return msg
}

// runNodeTests is the test stage's Node part: runs test and lint scripts in an isolated worktree.
func runNodeTests(ctx context.Context, cfg *stageConfig) (string, error) {
	manager, hasTest, hasLint, err := resolveNodeTestPlan(cfg.repoDir)
	if err != nil {
		return "", err
	}
	if !hasTest && !hasLint {
		cfg.unverifyLanguage(languageNode)
		return "", notApplicable("no test or lint script declared in package.json: Node tests skipped")
	}
	if cfg.dryRun {
		return "", skipped(fmt.Sprintf("dry run: %s test and lint not run", manager))
	}
	if reqErr := requireNodeManager(cfg, manager); reqErr != nil {
		return "", reqErr
	}
	budget, err := stageBudget(ctx, cfg.repoDir)
	if err != nil {
		return "", err
	}
	bound := budget.StageBound
	var classic bool
	if manager == nodeManagerYarn {
		var yarnErr error
		classic, yarnErr = isYarnClassic(ctx, cfg)
		if yarnErr != nil {
			return "", yarnErr
		}
	}
	installArgs := nodeInstallArgs(manager, classic)
	err = inStageWorktree(ctx, cfg, bound, func(tCtx context.Context, dir string) error {
		return runNodeWorktreeScripts(tCtx, cfg, bound, dir, manager, installArgs, hasTest, hasLint)
	})
	if err != nil {
		return "", err
	}
	return nodeTestsPassedMessage(manager, hasTest, hasLint, budget.Note), nil
}
