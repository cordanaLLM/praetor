// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// A locked asset is byte-compared by audit, so an adopter that runs a linter over every tracked
// file cannot fix a finding in one (#842, #845, #578). These tests hold every managed asset any
// family can write, enumerated from Families rather than listed by hand, to the linters at the
// settings Praetor's own gates and emitted templates use, per language. A linter that is not
// installed skips its subtest with the reason, and fails it where requiredToolsEnv is set (CI),
// so it never passes silently (HISS-21). The Python formatter policy is black's default with
// flake8 at 100 columns, linted by scripts/test_emitted_hook_lint.py from the same registry
// paths; this test adds the ruff lint rules the Python template selects and does not format.
const (
	// lintTimeout bounds one linter run (HISS-02).
	lintTimeout = 3 * time.Minute
	// maxLintOutput bounds the output kept from one linter run.
	maxLintOutput = 1 << 20
	// maxLintAssets bounds the assets one lint pass reads.
	maxLintAssets = 256
	// requiredToolsEnv is the variable the hook-lint gate (scripts/test_emitted_hook_lint.py)
	// sets to the directory of its pinned toolchain; CI sets it. Where it is set, a missing
	// linter fails the test instead of skipping it, as that gate does (HISS-21).
	requiredToolsEnv = "PRAETOR_HOOK_LINT_BIN"
	// pythonLineLength is the line length of templates/python/ruff.toml.tmpl.
	pythonLineLength = "100"
	// pythonRules are the ruff rules of templates/python/ruff.toml.tmpl plus RUF100, the unused
	// noqa directive an adopter that selects RUF reports.
	pythonRules = "E,F,I,N,UP,B,A,C4,T20,SIM,RUF100"
	// pythonTarget is a Python release every managed script must run on.
	pythonTarget = "py312"
)

// lintAsset is one managed file as adoption writes it.
type lintAsset struct {
	rel  string
	data []byte
}

// managedLintAssets returns every file any registered family writes, with its canonical bytes.
func managedLintAssets(t testing.TB) []lintAsset {
	t.Helper()
	families := Families()
	var assets []lintAsset
	for index := 0; index < len(families) && index < MaxFamilies; index++ {
		for _, rel := range families[index].ManagedPaths() {
			data, owned, err := families[index].Canonical(rel)
			if err != nil || !owned {
				t.Fatalf("read the managed asset %s of %s: owned=%v err=%v", rel, families[index].Name, owned, err)
			}
			assets = append(assets, lintAsset{rel: rel, data: data})
		}
	}
	if len(assets) == 0 || len(assets) > maxLintAssets {
		t.Fatalf("the registry yields %d managed assets, want 1..%d", len(assets), maxLintAssets)
	}
	return assets
}

// withExtension keeps the assets whose path ends in one of extensions.
func withExtension(assets []lintAsset, extensions ...string) []lintAsset {
	var kept []lintAsset
	for _, asset := range assets {
		if slices.Contains(extensions, path.Ext(asset.rel)) {
			kept = append(kept, asset)
		}
	}
	return kept
}

// writeLintTree writes assets under a fresh directory at their repository-relative paths and
// returns it.
func writeLintTree(t testing.TB, assets []lintAsset) string {
	t.Helper()
	root := t.TempDir()
	for _, asset := range assets {
		target := filepath.Join(root, filepath.FromSlash(asset.rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			t.Fatalf("create the directory of %s: %v", asset.rel, err)
		}
		if err := os.WriteFile(target, asset.data, 0o600); err != nil {
			t.Fatalf("write %s: %v", asset.rel, err)
		}
	}
	return root
}

// findTool returns the path of a linter, looked up in the pinned toolchain directory first and
// then on PATH, and the lookup error when it is in neither.
func findTool(name string) (string, error) {
	if dir := os.Getenv(requiredToolsEnv); dir != "" {
		if found, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return found, nil
		}
	}
	return exec.LookPath(name)
}

// requireTool returns the path of a linter, or skips the test with the reason. Where
// requiredToolsEnv is set, a missing linter fails the test.
func requireTool(t testing.TB, name string) string {
	t.Helper()
	found, err := findTool(name)
	if err != nil {
		if os.Getenv(requiredToolsEnv) != "" {
			t.Fatalf("%s is required where %s is set and was not found: %v", name, requiredToolsEnv, err)
		}
		t.Skipf("%s is not installed on this leg, so its check did not run: %v", name, err)
	}
	return found
}

// optionalTool is requireTool for a check that must not stop its siblings: where the linter is
// missing and not required, it logs the skipped check and returns "".
func optionalTool(t testing.TB, name string) string {
	t.Helper()
	found, err := findTool(name)
	if err == nil {
		return found
	}
	if os.Getenv(requiredToolsEnv) != "" {
		t.Fatalf("%s is required where %s is set and was not found: %v", name, requiredToolsEnv, err)
	}
	t.Logf("%s is not installed on this leg, so only its check was skipped: %v", name, err)
	return ""
}

// runLinter runs one linter in dir and returns its combined output and whether it exited 0.
func runLinter(t testing.TB, dir, tool string, args ...string) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), lintTimeout)
	defer cancel()
	result, err := util.RunCommandBytes(ctx, dir, tool, maxLintOutput, args...)
	output := strings.TrimSpace(string(result.Stdout) + string(result.Stderr))
	if ctx.Err() != nil {
		t.Fatalf("%s %v did not finish within %s", tool, args, lintTimeout)
	}
	return output, err == nil
}

var goVersionPattern = regexp.MustCompile(`^go(\d+\.\d+)`)

// goModule is the go.mod of the scratch module: the running toolchain's language version.
func goModule(t testing.TB) string {
	t.Helper()
	match := goVersionPattern.FindStringSubmatch(runtime.Version())
	if match == nil {
		t.Skipf("toolchain version %q names no go release, so the scratch module has no go directive", runtime.Version())
	}
	return "module managedassetlint\n\ngo " + match[1] + "\n"
}

// goFindings runs gofmt, gofumpt (when installed) and a per-package go vet over the Go assets in a scratch
// module. The vet runs name the package directory alone, with no build tag, as the pre-commit
// hooks of an adopter do.
func goFindings(t testing.TB, assets []lintAsset) []string {
	t.Helper()
	files := withExtension(assets, ".go")
	if len(files) == 0 {
		return nil
	}
	gofmt, goTool, gofumpt := requireTool(t, "gofmt"), requireTool(t, "go"), optionalTool(t, "gofumpt")
	files = append(files, lintAsset{rel: "go.mod", data: []byte(goModule(t))})
	root := writeLintTree(t, files)
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	var findings []string
	formatters := [][]string{{gofmt, "-l", "."}}
	if gofumpt != "" {
		formatters = append(formatters, []string{gofumpt, "-l", "."})
	}
	for _, check := range formatters {
		output, ok := runLinter(t, root, check[0], check[1:]...)
		if !ok || output != "" {
			findings = append(findings, filepath.Base(check[0])+" reports unformatted files or failed: "+output)
		}
	}
	for _, dir := range goPackageDirs(files) {
		if output, ok := runLinter(t, root, goTool, "vet", "./"+dir); !ok {
			findings = append(findings, "go vet ./"+dir+": "+output)
		}
	}
	return findings
}

// goPackageDirs returns the distinct directories of the Go files, sorted.
func goPackageDirs(files []lintAsset) []string {
	var dirs []string
	for _, file := range withExtension(files, ".go") {
		if dir := path.Dir(file.rel); !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	slices.Sort(dirs)
	return dirs
}

// pythonFindings runs ruff check at the template rule set over the Python assets, ignoring any
// configuration file of the host. Formatting is black's, checked by the hook-lint gate.
func pythonFindings(t testing.TB, assets []lintAsset) []string {
	t.Helper()
	files := withExtension(assets, ".py")
	if len(files) == 0 {
		return nil
	}
	ruff := requireTool(t, "ruff")
	root := writeLintTree(t, files)
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.rel)
	}
	args := []string{
		"check", "--isolated", "--no-cache", "--line-length", pythonLineLength,
		"--target-version", pythonTarget, "--select", pythonRules,
	}
	if output, ok := runLinter(t, root, ruff, append(args, names...)...); !ok {
		return []string{"ruff check: " + output}
	}
	return nil
}

// markdownFindings holds the Markdown assets to markdownlint's default rules: the in-process
// subset the generators' tests use, and the real markdownlint-cli2 when it is installed.
func markdownFindings(t testing.TB, assets []lintAsset) []string {
	t.Helper()
	files := withExtension(assets, ".md")
	var findings []string
	for _, file := range files {
		for _, finding := range testsupport.MarkdownFindings(string(file.data)) {
			findings = append(findings, file.rel+": "+finding)
		}
	}
	if len(files) == 0 {
		return findings
	}
	cli, err := findTool("markdownlint-cli2")
	if err != nil {
		t.Logf("markdownlint-cli2 is not installed on this leg; only the in-process default-rule subset ran: %v", err)
		return findings
	}
	root := writeLintTree(t, files)
	config := filepath.Join(root, "markdownlint-default.json")
	if err := os.WriteFile(config, []byte(`{"config":{"default":true},"noProgress":true}`), 0o600); err != nil {
		t.Fatalf("write the default markdownlint configuration: %v", err)
	}
	args := []string{"--config", config}
	for _, file := range files {
		args = append(args, file.rel)
	}
	if output, ok := runLinter(t, root, cli, args...); !ok {
		findings = append(findings, "markdownlint-cli2: "+output)
	}
	return findings
}

// shellFindings runs shellcheck at its defaults over the shell assets.
func shellFindings(t testing.TB, assets []lintAsset) []string {
	t.Helper()
	files := withExtension(assets, ".sh")
	if len(files) == 0 {
		return nil
	}
	shellcheck := requireTool(t, "shellcheck")
	root := writeLintTree(t, files)
	args := []string{}
	for _, file := range files {
		args = append(args, file.rel)
	}
	if output, ok := runLinter(t, root, shellcheck, args...); !ok {
		return []string{"shellcheck: " + output}
	}
	return nil
}

// Positive: every managed asset passes the linters of its language.
func TestManagedAssetsAreLintClean(t *testing.T) {
	assets := managedLintAssets(t)
	for name, lint := range map[string]func(testing.TB, []lintAsset) []string{
		"go": goFindings, "python": pythonFindings, "markdown": markdownFindings, "shell": shellFindings,
	} {
		t.Run(name, func(t *testing.T) {
			for _, finding := range lint(t, assets) {
				t.Error(finding)
			}
		})
	}
}

// Boundary: the enumeration reaches the files the issues name, so a registry change that drops
// one fails here instead of shrinking the lint pass.
func TestManagedAssetsLintEnumerationCoversTheNamedAssets(t *testing.T) {
	assets := managedLintAssets(t)
	for _, rel := range []string{
		"tools/apicompat/gate/main.go",
		"tools/figures/mkdocs_hook.py",
		"tools/figures/third_party/interfig/VENDOR.md",
	} {
		if !slices.ContainsFunc(assets, func(asset lintAsset) bool { return asset.rel == rel }) {
			t.Errorf("the lint enumeration does not reach %s", rel)
		}
	}
}

// Negative: one planted defect per language is refused, so a green result above is not a
// linter that finds nothing (rule 13).
func TestManagedAssetsLintRefusesPlantedDefects(t *testing.T) {
	cases := []struct {
		name  string
		lint  func(testing.TB, []lintAsset) []string
		asset lintAsset
	}{
		{"go unformatted", goFindings, lintAsset{"tools/planted/main.go", []byte("package main\n\nfunc main() {\nprintln( 1 )\n}\n")}},
		{"go gofumpt only", goFindings, lintAsset{"tools/planted/main.go", []byte("package main\n\nfunc args() []string {\n\treturn []string{\"a\",\n\t\t\"b\"}\n}\n\nfunc main() { _ = args() }\n")}},
		{"go build tag", goFindings, lintAsset{"tools/planted/main.go", []byte("//go:build plantedtag\n\npackage main\n\nfunc main() {}\n")}},
		{"python import order", pythonFindings, lintAsset{"tools/planted/hook.py", []byte("import sys\nimport os\n\nprint(os.name, sys.argv)\n")}},
		{"python unused noqa", pythonFindings, lintAsset{"tools/planted/hook.py", []byte("VALUE = 1  # noqa: ARG001\n")}},
		{"python long line", pythonFindings, lintAsset{"tools/planted/hook.py", []byte("VALUE = [" + strings.Repeat("1, ", 40) + "1]\n")}},
		{"markdown long line", markdownFindings, lintAsset{"tools/planted/NOTES.md", []byte("# Notes\n\n" + strings.Repeat("word ", 30) + "\n")}},
		{"shell unquoted", shellFindings, lintAsset{"tools/planted/run.sh", []byte("#!/bin/sh\nrm -rf $1/*\n")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "go gofumpt only" {
				requireTool(t, "gofumpt") // gofmt accepts this defect, so only gofumpt can refuse it
			}
			if findings := tc.lint(t, []lintAsset{tc.asset}); len(findings) == 0 {
				t.Fatalf("the planted %s asset passed its linters", tc.name)
			}
		})
	}
}
