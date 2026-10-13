// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/supplychain"
	"github.com/cordanaLLM/praetor/internal/util"
)

// The lint stage runs the two checks a pushed head was refused for although the gate had signed
// it (#1312): golangci-lint, which the pre-push hook runs on the changed packages
// (.config/lefthook/scripts/checks.py), and reuse lint, which CI's Compliance workflow runs
// where the repository declares REUSE (supplychain.ReuseDeclared).
const (
	// golangciLintModule is how the Makefile's lint target and the pre-push hook start
	// golangci-lint, so the gate runs the same release they do.
	golangciLintModule = "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"
	// reuseBinary is the REUSE tool; the gate requires the release line CI's action runs
	// (supplychain.ReuseMajor).
	reuseBinary = "reuse"
	// LintAllowance bounds the lint stage and is added to a run's deadline where a go.mod is at
	// the root (ResolveRepoRunBudget): a first `go run` of golangci-lint builds it, and a lint of
	// the changed packages of this repository takes 2 to 35 seconds once built.
	LintAllowance = 3 * time.Minute
	// maxChangedPathBytes bounds git's list of the paths a head changed (HISS-02).
	maxChangedPathBytes = 4 << 20
	// maxLintPackages bounds the package arguments; more changed packages lint the whole module.
	maxLintPackages = 512
	// allGoPackages is the pattern of every package of the module.
	allGoPackages = "./..."
)

// golangciConfigs are the configuration file names golangci-lint reads at the root.
var golangciConfigs = []string{".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json"}

// goModuleFiles are the root files whose change can change any package's lint result.
var goModuleFiles = []string{"go.mod", "go.sum", "go.work", "go.work.sum"}

// lintPart is what one linter of the stage did: its line for the stage message, whether it
// applied to the repository, and its failure.
type lintPart struct {
	line    string
	applies bool
	err     error
}

// runLintStage runs golangci-lint on the Go packages a head changed and reuse lint where the
// repository declares REUSE, and fails on either's findings, printed in full. A part that does not
// apply says so in the stage message, which the receipt signs, so nobody reads it as a check that
// ran. A dry run runs neither: `go run` of golangci-lint can fetch it.
func runLintStage(ctx context.Context, cfg *stageConfig) (string, error) {
	if cfg.dryRun {
		return "", skipped("dry run: golangci-lint and reuse lint not run")
	}
	lintCtx, cancel := context.WithTimeout(ctx, LintAllowance)
	defer cancel()
	parts := []lintPart{runGolangciLint(lintCtx, cfg), runReuseLint(lintCtx, cfg)}
	var lines []string
	applies := false
	for _, part := range parts {
		if part.err != nil {
			return "", part.err
		}
		applies = applies || part.applies
		lines = append(lines, part.line)
	}
	message := strings.Join(lines, "; ")
	if !applies {
		return "", notApplicable(message)
	}
	return message, nil
}

// runGolangciLint lints the packages golangciScope selects with the repository's configuration.
func runGolangciLint(ctx context.Context, cfg *stageConfig) lintPart {
	if !util.FileExists(filepath.Join(cfg.repoDir, "go.mod")) {
		return lintPart{line: "golangci-lint: no go.mod"}
	}
	if !slices.ContainsFunc(golangciConfigs, func(name string) bool { return util.FileExists(filepath.Join(cfg.repoDir, name)) }) {
		return lintPart{line: "golangci-lint: not run, no " + strings.Join(golangciConfigs, ", ")}
	}
	packages, scope, err := golangciScope(ctx, cfg)
	if err != nil {
		return lintPart{err: err}
	}
	if len(packages) == 0 {
		return lintPart{line: "golangci-lint: " + scope, applies: true}
	}
	args := append([]string{"run", golangciLintModule, "run"}, packages...)
	if out, err := cfg.run(ctx, cfg.repoDir, "go", args...); err != nil {
		return lintPart{err: fmt.Errorf("golangci-lint found issues in %s: %w\n%s", scope, err, out)}
	}
	return lintPart{line: "golangci-lint passed on " + scope, applies: true}
}

// golangciScope returns the package patterns to lint and their description: the packages whose
// Go files a head changed since its merge base with the origin remote's default branch, the
// scope the pre-push hook lints. A change to go.mod, go.sum or the linter configuration, too many
// packages, or no recorded default branch lints the whole module.
func golangciScope(ctx context.Context, cfg *stageConfig) ([]string, string, error) {
	branch, recorded, err := util.ReadOriginHeadBranch(ctx, cfg.repoDir)
	if err != nil {
		return nil, "", fmt.Errorf("golangci-lint scope: %w", err)
	}
	if !recorded {
		return []string{allGoPackages}, "every package (no origin default branch recorded to compare with)", nil
	}
	base := "refs/remotes/origin/" + branch
	changed, err := changedSinceMergeBase(ctx, cfg.repoDir, base)
	if err != nil {
		return nil, "", fmt.Errorf("golangci-lint scope against %s: %w", base, err)
	}
	return lintPackages(cfg.repoDir, changed, "origin/"+branch)
}

// changedSinceMergeBase lists the paths HEAD changed since its merge base with base.
func changedSinceMergeBase(ctx context.Context, repoDir, base string) ([]string, error) {
	mergeBase, err := util.RunGitProbe(ctx, repoDir, 4096, "merge-base", base, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("merge base: %w", err)
	}
	diff, err := util.RunGitProbe(ctx, repoDir, maxChangedPathBytes, "diff", "--name-only", "-z", "--no-renames",
		"--no-ext-diff", strings.TrimSpace(string(mergeBase.Stdout)), "HEAD", "--")
	if err != nil {
		return nil, fmt.Errorf("changed paths: %w", err)
	}
	var paths []string
	for _, entry := range bytes.Split(bytes.TrimSuffix(diff.Stdout, []byte{0}), []byte{0}) {
		if len(entry) > 0 {
			paths = append(paths, string(entry))
		}
	}
	return paths, nil
}

// lintPackages maps the changed paths to package patterns, one per directory below repoDir that
// still exists and held a changed Go file outside testdata, and describes the scope against base.
func lintPackages(repoDir string, changed []string, base string) ([]string, string, error) {
	var packages []string
	for _, rel := range changed {
		if changesEveryPackage(rel) {
			return []string{allGoPackages}, "every package (" + rel + " changed since " + base + ")", nil
		}
		if pattern, ok := changedPackage(repoDir, rel); ok && !slices.Contains(packages, pattern) {
			packages = append(packages, pattern)
		}
	}
	if len(packages) > maxLintPackages {
		return []string{allGoPackages}, fmt.Sprintf("every package (%d changed since %s)", len(packages), base), nil
	}
	if len(packages) == 0 {
		return nil, "no Go package changed since " + base, nil
	}
	slices.Sort(packages)
	return packages, fmt.Sprintf("%d changed package(s) since %s", len(packages), base), nil
}

// changesEveryPackage reports whether a change to rel can change any package's lint result: a
// root module file or a golangci-lint configuration.
func changesEveryPackage(rel string) bool {
	return (slices.Contains(goModuleFiles, path.Base(rel)) && path.Dir(rel) == ".") || slices.Contains(golangciConfigs, rel)
}

// changedPackage returns the package pattern of a changed Go file outside testdata whose
// directory still exists below repoDir; a package the head removed has nothing to lint.
func changedPackage(repoDir, rel string) (string, bool) {
	if !strings.HasSuffix(rel, ".go") || slices.Contains(strings.Split(rel, "/"), "testdata") {
		return "", false
	}
	info, err := os.Stat(filepath.Join(repoDir, filepath.FromSlash(path.Dir(rel))))
	if err != nil || !info.IsDir() {
		return "", false
	}
	return "./" + path.Dir(rel), true
}

// runReuseLint runs reuse lint where the repository declares REUSE, with the release line CI's
// action runs; a missing tool or another release line fails the stage, as a missing scanner
// does.
func runReuseLint(ctx context.Context, cfg *stageConfig) lintPart {
	declared, err := supplychain.ReuseDeclared(ctx, cfg.repoDir)
	if err != nil {
		return lintPart{err: fmt.Errorf("REUSE declaration: %w", err)}
	}
	if !declared {
		return lintPart{line: "reuse lint: no " + supplychain.ReuseFile + " or " + supplychain.LicensesDir + "/"}
	}
	major := supplychain.ReuseMajor()
	if err := requireScanner(cfg, reuseBinary, "pipx install 'reuse=="+major+".*'"); err != nil {
		return lintPart{err: err}
	}
	version, err := cfg.run(ctx, cfg.repoDir, reuseBinary, "--version")
	if err != nil {
		return lintPart{err: fmt.Errorf("reuse --version: %w: %s", err, version)}
	}
	if got := reuseVersionMajor(version); got != major {
		return lintPart{err: fmt.Errorf("reuse %q is installed; the gate runs reuse %s.x, the release line CI runs (%s)",
			strings.TrimSpace(firstLine(version)), major, supplychain.ReuseActionRef())}
	}
	if out, err := cfg.run(ctx, cfg.repoDir, reuseBinary, "lint"); err != nil {
		return lintPart{err: fmt.Errorf("reuse lint failed: %w\n%s", err, out)}
	}
	return lintPart{line: "reuse lint passed (" + strings.TrimSpace(firstLine(version)) + ")", applies: true}
}

// reuseVersionMajor reads the major of `reuse --version`, whose first line ends in the version
// ("reuse, version 6.2.0").
func reuseVersionMajor(out string) string {
	fields := strings.Fields(firstLine(out))
	if len(fields) == 0 {
		return ""
	}
	major, _, _ := strings.Cut(fields[len(fields)-1], ".")
	return major
}

// firstLine is the first line of out.
func firstLine(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	return line
}
