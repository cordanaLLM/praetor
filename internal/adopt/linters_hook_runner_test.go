// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"
)

const (
	// hookCIWorkflow runs the hook runner the way lusoris/home-zeus does (#1313).
	hookCIWorkflow = "on: pull_request\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n      - run: %s\n"
	// gitleaksPreCommit is a .pre-commit-config.yaml whose remote gitleaks hook runs at the
	// default stage, beside a yamllint hook.
	gitleaksPreCommit = "repos:\n  - repo: https://github.com/gitleaks/gitleaks\n    rev: v8.30.0\n    hooks:\n      - id: gitleaks\n" +
		"  - repo: https://github.com/adrienverge/yamllint\n    rev: v1.38.0\n    hooks:\n      - id: yamllint\n"
)

// auditGitleaksWith runs the declared-linters gate for gitleaks over a tree holding files.
func auditGitleaksWith(t *testing.T, files map[string]string) (string, error) {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		writeLinterFixtureFile(t, root, rel, body)
	}
	return AuditDeclaredLinters(t.Context(), DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("security:high", "gitleaks"), Today: testToday,
	})
}

// workflowRunning is a CI workflow whose one step runs command.
func workflowRunning(command string) string {
	return strings.Replace(hookCIWorkflow, "%s", command, 1)
}

// Positive (#1313): a linter the hook configuration starts counts as run when CI runs the hook
// runner: pre-commit run --all-files, the pre-commit action, a local hook whose entry runs the
// linter, an explicit stage the hook declares, and lefthook run with a command or a jobs entry
// that runs it.
func TestAuditDeclaredLinters_HookRunnerStartsLinter(t *testing.T) {
	localEntry := "repos:\n  - repo: local\n    hooks:\n      - id: secrets\n        name: secrets\n        entry: gitleaks protect --staged\n        language: system\n"
	prePush := strings.Replace(gitleaksPreCommit, "      - id: gitleaks\n", "      - id: gitleaks\n        stages: [pre-push]\n", 1)
	cases := map[string]map[string]string{
		"pre-commit run": {".github/workflows/ci.yml": workflowRunning("pre-commit run --all-files --show-diff-on-failure"),
			".pre-commit-config.yaml": gitleaksPreCommit},
		"pre-commit action": {".github/workflows/ci.yml": "on: pull_request\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n" +
			"      - uses: pre-commit/action@v3.0.1\n", ".pre-commit-config.yaml": gitleaksPreCommit},
		"local entry": {".github/workflows/ci.yml": workflowRunning("pre-commit run --all-files"), ".pre-commit-config.yaml": localEntry},
		"named hook":  {".github/workflows/ci.yml": workflowRunning("pre-commit run gitleaks --all-files"), ".pre-commit-config.yaml": gitleaksPreCommit},
		"explicit stage": {".github/workflows/ci.yml": workflowRunning("pre-commit run --hook-stage pre-push --all-files"),
			".pre-commit-config.yaml": prePush},
		"lefthook command": {".github/workflows/ci.yml": workflowRunning("lefthook run pre-commit --all-files"),
			"lefthook.yml": "pre-commit:\n  commands:\n    secrets:\n      run: gitleaks protect --staged\n"},
		"lefthook job": {".github/workflows/ci.yml": workflowRunning("lefthook run pre-commit --job gitleaks"),
			"lefthook.yml": "pre-commit:\n  jobs:\n    - name: gitleaks\n      run: gitleaks protect --staged\n"},
	}
	for name, files := range cases {
		out, err := auditGitleaksWith(t, files)
		if err != nil || !strings.Contains(out, "[PASS]") {
			t.Errorf("%s: out %q, err %v; want gitleaks counted as run", name, out, err)
		}
	}
}

// Negative (#1313): the same CI step does not count a linter the hook configuration does not
// start for that run: the hook removed, a hook at another stage than the run selects, a run
// limited to another hook, a run of another configuration file, a step that only installs the
// hooks, a forgiven step, a Lefthook command that skips, a Lefthook run limited by tag, and no
// hook configuration at all.
func TestAuditDeclaredLinters_HookRunnerDoesNotStartLinter(t *testing.T) {
	withoutGitleaks := "repos:\n  - repo: https://github.com/adrienverge/yamllint\n    rev: v1.38.0\n    hooks:\n      - id: yamllint\n"
	prePush := strings.Replace(gitleaksPreCommit, "      - id: gitleaks\n", "      - id: gitleaks\n        stages: [pre-push]\n", 1)
	lefthook := "pre-commit:\n  commands:\n    gitleaks:\n      run: gitleaks protect --staged\n"
	cases := map[string]map[string]string{
		"hook removed":        {".github/workflows/ci.yml": workflowRunning("pre-commit run --all-files"), ".pre-commit-config.yaml": withoutGitleaks},
		"other stage":         {".github/workflows/ci.yml": workflowRunning("pre-commit run --all-files"), ".pre-commit-config.yaml": prePush},
		"other hook":          {".github/workflows/ci.yml": workflowRunning("pre-commit run yamllint --all-files"), ".pre-commit-config.yaml": gitleaksPreCommit},
		"other config":        {".github/workflows/ci.yml": workflowRunning("pre-commit run --config ci.yaml --all-files"), ".pre-commit-config.yaml": gitleaksPreCommit},
		"install only":        {".github/workflows/ci.yml": workflowRunning("pre-commit install"), ".pre-commit-config.yaml": gitleaksPreCommit},
		"no config":           {".github/workflows/ci.yml": workflowRunning("pre-commit run --all-files")},
		"lefthook skip":       {".github/workflows/ci.yml": workflowRunning("lefthook run pre-commit"), "lefthook.yml": strings.Replace(lefthook, "      run:", "      skip: true\n      run:", 1)},
		"lefthook tag":        {".github/workflows/ci.yml": workflowRunning("lefthook run pre-commit --tag fast"), "lefthook.yml": lefthook},
		"lefthook other job":  {".github/workflows/ci.yml": workflowRunning("lefthook run pre-commit --job yamllint"), "lefthook.yml": lefthook},
		"lefthook other hook": {".github/workflows/ci.yml": workflowRunning("lefthook run pre-push"), "lefthook.yml": lefthook},
		"forgiven step": {".github/workflows/ci.yml": "on: pull_request\njobs:\n  lint:\n    runs-on: ubuntu-latest\n    steps:\n" +
			"      - run: pre-commit run --all-files\n        continue-on-error: true\n", ".pre-commit-config.yaml": gitleaksPreCommit},
	}
	for name, files := range cases {
		_, err := auditGitleaksWith(t, files)
		if err == nil || !strings.Contains(err.Error(), "gitleaks") {
			t.Errorf("%s: err %v; want gitleaks reported as not run", name, err)
		}
	}
}

// Boundary (#1313): the legacy stage name commit is the pre-commit stage, default_stages
// applies to a hook without stages, and a hook ID that names the linter as a dash-separated
// word counts (markdownlint-cli2 names markdownlint).
func TestAuditDeclaredLinters_HookRunnerStageAndNameRules(t *testing.T) {
	legacy := strings.Replace(gitleaksPreCommit, "      - id: gitleaks\n", "      - id: gitleaks\n        stages: [commit]\n", 1)
	if out, err := auditGitleaksWith(t, map[string]string{".github/workflows/ci.yml": workflowRunning("pre-commit run --all-files"),
		".pre-commit-config.yaml": legacy}); err != nil {
		t.Fatalf("legacy stage commit: out %q, err %v", out, err)
	}
	defaults := "default_stages: [pre-push]\n" + gitleaksPreCommit
	if _, err := auditGitleaksWith(t, map[string]string{".github/workflows/ci.yml": workflowRunning("pre-commit run --all-files"),
		".pre-commit-config.yaml": defaults}); err == nil {
		t.Fatal("default_stages pre-push: gitleaks was counted for a pre-commit stage run")
	}
	root := t.TempDir()
	writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", workflowRunning("pre-commit run --all-files"))
	writeLinterFixtureFile(t, root, ".pre-commit-config.yaml",
		"repos:\n  - repo: https://github.com/DavidAnson/markdownlint-cli2\n    rev: v0.18.1\n    hooks:\n      - id: markdownlint-cli2\n")
	writeLinterFixtureFile(t, root, "README.md", "# Readme\n")
	out, err := AuditDeclaredLinters(t.Context(), DeclaredLintersOptions{
		RootDir: root, Policy: singleLinterPolicy("profile", "markdownlint"), Today: testToday,
	})
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("markdownlint-cli2 hook: out %q, err %v", out, err)
	}
}

// Boundary (#1313): a YAML tree without a Kubernetes object skips kubeconform; an object in a
// later document of a stream makes it apply.
func TestAuditDeclaredLinters_KubernetesManifestDetection(t *testing.T) {
	cases := []struct {
		name, body string
		applies    bool
	}{
		{"workflow only", "on: push\njobs: {}\n", false},
		{"apiVersion without kind", "apiVersion: v1\nmetadata: {}\n", false},
		{"kind is no string", "apiVersion: v1\nkind: [Service]\n", false},
		{"second document", "a: 1\n---\napiVersion: v1\nkind: ConfigMap\n", true},
	}
	for _, tc := range cases {
		root := t.TempDir()
		writeLinterFixtureFile(t, root, "deploy/doc.yaml", tc.body)
		out, err := AuditDeclaredLinters(t.Context(), DeclaredLintersOptions{
			RootDir: root, Policy: singleLinterPolicy("profile", "kubeconform"), Today: testToday,
		})
		skipped := strings.Contains(out, "[SKIP] kubeconform")
		if skipped == tc.applies || (tc.applies && err == nil) {
			t.Errorf("%s: out %q, err %v; want applies=%v", tc.name, out, err, tc.applies)
		}
	}
}
