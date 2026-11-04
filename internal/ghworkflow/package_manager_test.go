// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// packageManagerInstallPin matches literal manager versions in install commands, including
// release tags. Corepack itself may be installed by version; pnpm and Yarn read packageManager.
var packageManagerInstallPin = regexp.MustCompile(`(?m)(?:^|[;&|])\s*(?:npm\s+(?:install|i|--global|-g)\b[^\n;&|]*|corepack\s+(?:prepare|install)\b[^\n;&|]*)\b(?:pnpm|yarn)@[a-zA-Z0-9^~*]`)

// hardcodedPackageManagerVersion reports a step that overrides the manifest's manager version.
func hardcodedPackageManagerVersion(step Step) bool {
	if strings.HasPrefix(step.Uses, "pnpm/action-setup@") {
		version, set := step.With["version"]
		text := strings.TrimSpace(fmt.Sprint(version))
		if set && text != "" && !strings.Contains(text, "${{") {
			return true
		}
	}
	return packageManagerInstallPin.MatchString(strings.ReplaceAll(step.Run, "\\\n", " "))
}

// TestEngineWorkflowsReadPackageManager prevents workflow pins drifting from package.json.
func TestEngineWorkflowsReadPackageManager(t *testing.T) {
	paths, err := filepath.Glob("../../" + Dir + "/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 || len(paths) > MaxFiles {
		t.Fatalf("workflow inventory has %d entries; want 1..%d", len(paths), MaxFiles)
	}
	for i := 0; i < len(paths) && i < MaxFiles; i++ {
		if IsYAMLName(paths[i]) {
			assertWorkflowReadsPackageManager(t, paths[i])
		}
	}
}

// assertWorkflowReadsPackageManager checks every step of one parsed workflow document.
func assertWorkflowReadsPackageManager(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, id := range SortedJobIDs(spec.Jobs) {
		steps := spec.Jobs[id].Steps
		if len(steps) > MaxStepsPerJob {
			t.Fatalf("%s/%s exceeds %d steps", path, id, MaxStepsPerJob)
		}
		for j := 0; j < len(steps) && j < MaxStepsPerJob; j++ {
			if hardcodedPackageManagerVersion(steps[j]) {
				t.Errorf("%s/%s step %d hardcodes a package-manager version; read package.json packageManager", path, id, j+1)
			}
		}
	}
}

// TestHardcodedPackageManagerVersion plants each forbidden form and accepts manifest selection.
func TestHardcodedPackageManagerVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		step Step
		want bool
	}{
		{"original console pin", Step{Run: "npm install --global pnpm@10.28.0"}, true},
		{"yarn global pin", Step{Run: "npm install --global yarn@1.22.22"}, true},
		{"short global flag", Step{Run: "npm i -g 'pnpm@10.34.9'"}, true},
		{"global before install", Step{Run: "npm --global install yarn@latest"}, true},
		{"continued command", Step{Run: "npm install --global \\\n pnpm@10.34.9"}, true},
		{"chained command", Step{Run: "echo setup && npm install -g pnpm@10"}, true},
		{"corepack pnpm pin", Step{Run: "corepack prepare pnpm@10.34.9 --activate"}, true},
		{"corepack yarn pin", Step{Run: "corepack prepare yarn@4.9.0 --activate"}, true},
		{"corepack global pin", Step{Run: "corepack install --global pnpm@latest"}, true},
		{"action string pin", Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": "10.34.9"}}, true},
		{"action numeric pin", Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": 10}}, true},
		{"corepack manifest", Step{Run: "npm install --global corepack@latest\ncorepack enable pnpm"}, false},
		{"yarn manifest", Step{Run: "corepack enable yarn\nyarn install --immutable"}, false},
		{"action manifest", Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"package_json_file": "console/package.json"}}, false},
		{"action expression", Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": "${{ steps.manifest.outputs.version }}"}}, false},
		{"other action version", Step{Uses: "other/setup@v4", With: map[string]any{"version": "10"}}, false},
		{"comment", Step{Run: "# npm install --global pnpm@10.28.0\ncorepack enable pnpm"}, false},
		{"empty step", Step{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hardcodedPackageManagerVersion(tc.step); got != tc.want {
				t.Errorf("hardcodedPackageManagerVersion(%+v) = %v, want %v", tc.step, got, tc.want)
			}
		})
	}
}
