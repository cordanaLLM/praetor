// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// packageManagerPinPattern matches package specifications that pin pnpm or yarn by version or tag.
var packageManagerPinPattern = regexp.MustCompile(`^(?:pnpm|yarn)@[a-zA-Z0-9^~*]`)

// shellTokenPattern matches whitespace-delimited shell words, including quoted strings.
var shellTokenPattern = regexp.MustCompile(`[^\s'"]+|'[^']*'|"[^"]*"`)

// splitShellCommands divides a script into commands separated by shell operators outside quotes.
func splitShellCommands(script string) []string {
	var commands []string
	var current strings.Builder
	var quote rune
	for _, r := range script {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			current.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			current.WriteRune(r)
		case strings.ContainsRune(";\n&|", r):
			if current.Len() > 0 {
				commands = append(commands, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		commands = append(commands, current.String())
	}
	return commands
}

// splitShellWords splits a single command line into words and strips quotes.
func splitShellWords(cmd string) []string {
	tokens := shellTokenPattern.FindAllString(cmd, -1)
	words := make([]string, len(tokens))
	for i, tok := range tokens {
		words[i] = strings.Trim(tok, `'"`)
	}
	return words
}

// commandPinsPackageManager reports whether words invoke a pinned package manager.
func commandPinsPackageManager(words []string) bool {
	if len(words) == 0 {
		return false
	}
	if len(words) > 1 && (words[0] == "sudo" || words[0] == "env") {
		words = words[1:]
	}
	tool := words[0]
	if packageManagerPinPattern.MatchString(tool) {
		return true
	}
	if tool != "npm" && tool != "npx" && tool != "corepack" {
		return false
	}
	for _, arg := range words[1:] {
		if packageManagerPinPattern.MatchString(arg) {
			return true
		}
	}
	return false
}

// hardcodedPackageManagerVersion reports a step that overrides the manifest's manager version.
func hardcodedPackageManagerVersion(step Step) bool {
	if strings.HasPrefix(step.Uses, "pnpm/action-setup@") {
		version, set := step.With["version"]
		text := strings.TrimSpace(fmt.Sprint(version))
		if set && text != "" && !strings.Contains(text, "${{") {
			return true
		}
	}
	if strings.TrimSpace(step.Run) == "" {
		return false
	}
	clean, err := util.StripHashComments(step.Run)
	if err != nil {
		return false
	}
	clean = strings.ReplaceAll(clean, "\\\r\n", " ")
	clean = strings.ReplaceAll(clean, "\\\n", " ")
	for _, cmd := range splitShellCommands(clean) {
		if commandPinsPackageManager(splitShellWords(cmd)) {
			return true
		}
	}
	return false
}

// workflowInventory reads workflow paths below root, validating that each entry is confined
// to root and that the total count stays within [1, MaxFiles].
func workflowInventory(root string) ([]string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root %q: %w", root, err)
	}
	pattern := filepath.Join(absRoot, Dir, "*")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob workflows under %q: %w", absRoot, err)
	}
	if len(paths) == 0 || len(paths) > MaxFiles {
		return nil, fmt.Errorf("workflow inventory has %d entries; want 1..%d", len(paths), MaxFiles)
	}
	var workflows []string
	for i := 0; i < len(paths) && i < MaxFiles; i++ {
		path := paths[i]
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve path %q: %w", path, err)
		}
		rel, err := filepath.Rel(absRoot, absPath)
		if err != nil {
			return nil, fmt.Errorf("relate %q to %q: %w", absPath, absRoot, err)
		}
		confined, err := util.ConfinePath(absRoot, rel)
		if err != nil {
			return nil, fmt.Errorf("workflow path %q escapes root %q: %w", path, absRoot, err)
		}
		if IsYAMLName(confined) {
			workflows = append(workflows, confined)
		}
	}
	return workflows, nil
}

// readWorkflowFile reads a workflow file after verifying it is confined beneath root.
func readWorkflowFile(root, path string) ([]byte, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root %q: %w", root, err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", path, err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return nil, fmt.Errorf("relate %q to %q: %w", absPath, absRoot, err)
	}
	confined, err := util.ConfinePath(absRoot, rel)
	if err != nil {
		return nil, fmt.Errorf("workflow file %q escapes root %q: %w", path, absRoot, err)
	}
	// #nosec G304 -- path was confined to root by util.ConfinePath.
	data, err := os.ReadFile(confined)
	if err != nil {
		return nil, fmt.Errorf("read workflow file %q: %w", confined, err)
	}
	return data, nil
}

// checkJobSteps enforces the step bound for a job and verifies no step hardcodes a package manager.
func checkJobSteps(jobID string, steps []Step) error {
	if len(steps) > MaxStepsPerJob {
		return fmt.Errorf("job %s exceeds %d steps (%d)", jobID, MaxStepsPerJob, len(steps))
	}
	for j := 0; j < len(steps) && j < MaxStepsPerJob; j++ {
		if hardcodedPackageManagerVersion(steps[j]) {
			return fmt.Errorf("job %s step %d hardcodes a package-manager version; read package.json packageManager", jobID, j+1)
		}
	}
	return nil
}

// TestEngineWorkflowsReadPackageManager prevents workflow pins drifting from package.json.
func TestEngineWorkflowsReadPackageManager(t *testing.T) {
	root := filepath.Clean("../..")
	paths, err := workflowInventory(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(paths) && i < MaxFiles; i++ {
		assertWorkflowReadsPackageManager(t, root, paths[i])
	}
}

// assertWorkflowReadsPackageManager checks every step of one parsed workflow document.
func assertWorkflowReadsPackageManager(t *testing.T, root, path string) {
	t.Helper()
	data, err := readWorkflowFile(root, path)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := Parse(data)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, id := range SortedJobIDs(spec.Jobs) {
		if err := checkJobSteps(id, spec.Jobs[id].Steps); err != nil {
			t.Errorf("%s: %v", path, err)
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
		{"location global flag", Step{Run: "npm --location=global install pnpm@10.28.0"}, true},
		{"double quoted pin", Step{Run: `npm install --global "pnpm@10.34.9"`}, true},
		{"continued command", Step{Run: "npm install --global \\\n pnpm@10.34.9"}, true},
		{"chained command", Step{Run: "echo setup && npm install -g pnpm@10"}, true},
		{"corepack pnpm pin", Step{Run: "corepack prepare pnpm@10.34.9 --activate"}, true},
		{"corepack yarn pin", Step{Run: "corepack prepare yarn@4.9.0 --activate"}, true},
		{"corepack global pin", Step{Run: "corepack install --global pnpm@latest"}, true},
		{"corepack use pin", Step{Run: "corepack use pnpm@10.34.9"}, true},
		{"npx version pin", Step{Run: "npx --yes pnpm@10.28.0 install"}, true},
		{"npx unflagged pin", Step{Run: "npx pnpm@10.28.0"}, true},
		{"npx yarn pin", Step{Run: "npx -y 'yarn@1.22.22'"}, true},
		{"action string pin", Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": "10.34.9"}}, true},
		{"action numeric pin", Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": 10}}, true},
		{"corepack manifest", Step{Run: "npm install --global corepack@latest\ncorepack enable pnpm"}, false},
		{"yarn manifest", Step{Run: "corepack enable yarn\nyarn install --immutable"}, false},
		{"action manifest", Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"package_json_file": "console/package.json"}}, false},
		{"action expression", Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": "${{ steps.manifest.outputs.version }}"}}, false},
		{"other action version", Step{Uses: "other/setup@v4", With: map[string]any{"version": "10"}}, false},
		{"comment", Step{Run: "# npm install --global pnpm@10.28.0\ncorepack enable pnpm"}, false},
		{"comment with semicolon", Step{Run: "# step 1; npm install --global pnpm@10.28.0\ncorepack enable pnpm"}, false},
		{"inline comment", Step{Run: "echo setup # npm install --global pnpm@10.28.0"}, false},
		{"inline comment after semicolon", Step{Run: "corepack enable pnpm ; # npm install --global pnpm@10.28.0"}, false},
		{"empty step", Step{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hardcodedPackageManagerVersion(tc.step); got != tc.want {
				t.Errorf("hardcodedPackageManagerVersion(%+v) = %v, want %v", tc.step, got, tc.want)
			}
		})
	}
}

// TestWorkflowInventory_Bounds plants inventory counts outside the allowed range [1, MaxFiles].
func TestWorkflowInventory_Bounds(t *testing.T) {
	t.Run("empty inventory", func(t *testing.T) {
		tmp := t.TempDir()
		if err := os.MkdirAll(filepath.Join(tmp, Dir), 0o750); err != nil {
			t.Fatal(err)
		}
		_, err := workflowInventory(tmp)
		if err == nil || !strings.Contains(err.Error(), "workflow inventory has 0 entries; want 1..64") {
			t.Fatalf("workflowInventory(empty) = %v, want error indicating 0 entries", err)
		}
	})

	t.Run("exceeds max files", func(t *testing.T) {
		tmp := t.TempDir()
		workflowDir := filepath.Join(tmp, Dir)
		if err := os.MkdirAll(workflowDir, 0o750); err != nil {
			t.Fatal(err)
		}
		for i := 0; i <= MaxFiles; i++ {
			name := filepath.Join(workflowDir, "workflow-"+strconv.Itoa(i)+".yml")
			if err := os.WriteFile(name, []byte("name: test\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		_, err := workflowInventory(tmp)
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("workflow inventory has %d entries; want 1..%d", MaxFiles+1, MaxFiles)) {
			t.Fatalf("workflowInventory(exceeds) = %v, want count error", err)
		}
	})
}

// TestWorkflowInventory_EscapesRoot plants a symlink escaping the repository root.
func TestWorkflowInventory_EscapesRoot(t *testing.T) {
	tmp := t.TempDir()
	workflowDir := filepath.Join(tmp, Dir)
	if err := os.MkdirAll(workflowDir, 0o750); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.yml")
	if err := os.WriteFile(outsideFile, []byte("name: escape\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workflowDir, "escape.yml")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatal(err)
	}

	_, err := workflowInventory(tmp)
	if err == nil || !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Fatalf("workflowInventory with escaping symlink = %v, want ErrPathEscapesRoot", err)
	}

	_, err = readWorkflowFile(tmp, link)
	if err == nil || !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Fatalf("readWorkflowFile with escaping symlink = %v, want ErrPathEscapesRoot", err)
	}
}

// TestCheckJobSteps_StepBound plants a job exceeding MaxStepsPerJob steps.
func TestCheckJobSteps_StepBound(t *testing.T) {
	steps := make([]Step, MaxStepsPerJob+1)
	err := checkJobSteps("overflow", steps)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("job overflow exceeds %d steps (%d)", MaxStepsPerJob, MaxStepsPerJob+1)) {
		t.Fatalf("checkJobSteps with %d steps = %v, want step bound error", MaxStepsPerJob+1, err)
	}
}
