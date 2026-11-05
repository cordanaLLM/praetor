// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// packageManagerPinPattern matches package specifications that pin pnpm or yarn by version or tag.
var packageManagerPinPattern = regexp.MustCompile(`^(?:pnpm|yarn)@[a-zA-Z0-9^~*]`)

// commandPinsPackageManager reports whether a parsed shell command invokes a pinned package manager.
func commandPinsPackageManager(cmd shellCommand) bool {
	if printPrograms[cmd.program] {
		return false
	}
	if packageManagerPinPattern.MatchString(cmd.program) {
		return true
	}
	if cmd.program != "npm" && cmd.program != "npx" && cmd.program != "corepack" {
		return false
	}
	for i := 0; i < len(cmd.args) && i < maxRunScriptFields; i++ {
		arg := strings.TrimPrefix(cmd.args[i], "--package=")
		arg = strings.TrimPrefix(arg, "-p=")
		if packageManagerPinPattern.MatchString(arg) {
			return true
		}
	}
	return false
}

// isCompleteExpression reports whether text is enclosed entirely in a single GitHub Actions expression.
func isCompleteExpression(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "${{") || !strings.HasSuffix(trimmed, "}}") {
		return false
	}
	inner := trimmed[3 : len(trimmed)-2]
	return strings.TrimSpace(inner) != "" && !strings.Contains(inner, "${{") && !strings.Contains(inner, "}}")
}

// hardcodedPackageManagerVersion reports a step that overrides the manifest's manager version.
// A run script the shared shell reader refuses is an error, never a pass: it cannot be shown
// free of pins.
func hardcodedPackageManagerVersion(step ghworkflow.Step) (bool, error) {
	if actionPath(step.Uses) == "pnpm/action-setup" {
		version, set := step.With["version"]
		text := strings.TrimSpace(fmt.Sprint(version))
		if set && text != "" && !isCompleteExpression(text) {
			return true, nil
		}
	}
	if strings.TrimSpace(step.Run) == "" {
		return false, nil
	}
	fields, err := scriptFields(step.Run)
	if err != nil {
		return false, fmt.Errorf("read run script: %w", err)
	}
	commands := scriptCommands(fields)
	for i := 0; i < len(commands) && i < maxRunScriptFields; i++ {
		if commandPinsPackageManager(parseShellCommand(commands[i])) {
			return true, nil
		}
	}
	return false, nil
}

// checkJobSteps enforces the step bound for a job and verifies no step hardcodes a package manager.
func checkJobSteps(jobID string, steps []ghworkflow.Step) error {
	if len(steps) > ghworkflow.MaxStepsPerJob {
		return fmt.Errorf("job %s exceeds %d steps (%d)", jobID, ghworkflow.MaxStepsPerJob, len(steps))
	}
	for j := 0; j < len(steps) && j < ghworkflow.MaxStepsPerJob; j++ {
		pinned, err := hardcodedPackageManagerVersion(steps[j])
		if err != nil {
			return fmt.Errorf("job %s step %d: %w", jobID, j+1, err)
		}
		if pinned {
			return fmt.Errorf("job %s step %d hardcodes a package-manager version; read package.json packageManager", jobID, j+1)
		}
	}
	return nil
}

// assertWorkflowReadsPackageManager checks every step of one parsed workflow document.
func assertWorkflowReadsPackageManager(t *testing.T, file workflowFile) {
	t.Helper()
	spec, err := ghworkflow.Parse(file.Data)
	if err != nil {
		t.Fatalf("%s: %v", file.Name, err)
	}
	for _, id := range ghworkflow.SortedJobIDs(spec.Jobs) {
		if err := checkJobSteps(id, spec.Jobs[id].Steps); err != nil {
			t.Errorf("%s: %v", file.Name, err)
		}
	}
}

// TestEngineWorkflowsReadPackageManager prevents workflow pins drifting from package.json.
func TestEngineWorkflowsReadPackageManager(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), workflowDiscoveryTimeout)
	defer cancel()
	files, err := readWorkflowFiles(ctx, engineRoot)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		assertWorkflowReadsPackageManager(t, files[i])
	}
}

var packageManagerStepCases = []struct {
	name string
	step ghworkflow.Step
	want bool
}{
	{"original console pin", ghworkflow.Step{Run: "npm install --global pnpm@10.28.0"}, true},
	{"yarn global pin", ghworkflow.Step{Run: "npm install --global yarn@1.22.22"}, true},
	{"short global flag", ghworkflow.Step{Run: "npm i -g 'pnpm@10.34.9'"}, true},
	{"global before install", ghworkflow.Step{Run: "npm --global install yarn@latest"}, true},
	{"location global flag", ghworkflow.Step{Run: "npm --location=global install pnpm@10.28.0"}, true},
	{"double quoted pin", ghworkflow.Step{Run: `npm install --global "pnpm@10.34.9"`}, true},
	{"continued command", ghworkflow.Step{Run: "npm install --global \\\n pnpm@10.34.9"}, true},
	{"chained command", ghworkflow.Step{Run: "echo setup && npm install -g pnpm@10"}, true},
	{"sudo prefix", ghworkflow.Step{Run: "sudo npm install --global pnpm@10.28.0"}, true},
	{"env prefix", ghworkflow.Step{Run: "env CI=true npm install --global pnpm@10.28.0"}, true},
	{"corepack pnpm pin", ghworkflow.Step{Run: "corepack prepare pnpm@10.34.9 --activate"}, true},
	{"corepack yarn pin", ghworkflow.Step{Run: "corepack prepare yarn@4.9.0 --activate"}, true},
	{"corepack global pin", ghworkflow.Step{Run: "corepack install --global pnpm@latest"}, true},
	{"corepack use pin", ghworkflow.Step{Run: "corepack use pnpm@10.34.9"}, true},
	{"npx version pin", ghworkflow.Step{Run: "npx --yes pnpm@10.28.0 install"}, true},
	{"npx unflagged pin", ghworkflow.Step{Run: "npx pnpm@10.28.0"}, true},
	{"npx yarn pin", ghworkflow.Step{Run: "npx -y 'yarn@1.22.22'"}, true},
	{"action string pin", ghworkflow.Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": "10.34.9"}}, true},
	{"action numeric pin", ghworkflow.Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": 10}}, true},
	{"action mixed expression pin", ghworkflow.Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": "10.${{ matrix.patch }}"}}, true},
	{"npm exec package flag pin", ghworkflow.Step{Run: "npm exec --package=pnpm@10.28.0 -- pnpm install"}, true},
	{"action empty expression pin", ghworkflow.Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": "${{}}"}}, true},
	{"corepack manifest", ghworkflow.Step{Run: "npm install --global corepack@latest\ncorepack enable pnpm"}, false},
	{"yarn manifest", ghworkflow.Step{Run: "corepack enable yarn\nyarn install --immutable"}, false},
	{"action manifest", ghworkflow.Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"package_json_file": "console/package.json"}}, false},
	{"action expression", ghworkflow.Step{Uses: "pnpm/action-setup@v4", With: map[string]any{"version": "${{ steps.manifest.outputs.version }}"}}, false},
	{"other action version", ghworkflow.Step{Uses: "other/setup@v4", With: map[string]any{"version": "10"}}, false},
	{"comment", ghworkflow.Step{Run: "# npm install --global pnpm@10.28.0\ncorepack enable pnpm"}, false},
	{"comment with semicolon", ghworkflow.Step{Run: "# step 1; npm install --global pnpm@10.28.0\ncorepack enable pnpm"}, false},
	{"inline comment", ghworkflow.Step{Run: "echo setup # npm install --global pnpm@10.28.0"}, false},
	{"inline comment after semicolon", ghworkflow.Step{Run: "corepack enable pnpm ; # npm install --global pnpm@10.28.0"}, false},
	{"empty step", ghworkflow.Step{}, false},
}

// TestHardcodedPackageManagerVersion plants each forbidden form and accepts manifest selection.
func TestHardcodedPackageManagerVersion(t *testing.T) {
	for i := 0; i < len(packageManagerStepCases) && i < maxWorkflowFiles; i++ {
		tc := packageManagerStepCases[i]
		t.Run(tc.name, func(t *testing.T) {
			if got, err := hardcodedPackageManagerVersion(tc.step); err != nil || got != tc.want {
				t.Errorf("hardcodedPackageManagerVersion(%+v) = %v, %v, want %v", tc.step, got, err, tc.want)
			}
		})
	}
}

// TestCheckJobSteps_StepBound plants a job exceeding MaxStepsPerJob steps.
func TestCheckJobSteps_StepBound(t *testing.T) {
	steps := make([]ghworkflow.Step, ghworkflow.MaxStepsPerJob+1)
	err := checkJobSteps("overflow", steps)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("job overflow exceeds %d steps (%d)", ghworkflow.MaxStepsPerJob, ghworkflow.MaxStepsPerJob+1)) {
		t.Fatalf("checkJobSteps with %d steps = %v, want step bound error", ghworkflow.MaxStepsPerJob+1, err)
	}
}

// TestCheckJobSteps_UnreadableScript plants a run script the shell reader refuses: the check
// fails closed instead of passing a script it could not read.
func TestCheckJobSteps_UnreadableScript(t *testing.T) {
	err := checkJobSteps("unreadable", []ghworkflow.Step{{Run: "npm install --global 'pnpm@10"}})
	if err == nil || !strings.Contains(err.Error(), "job unreadable step 1: read run script") {
		t.Fatalf("checkJobSteps with an unclosed quote = %v, want a read error", err)
	}
}

// TestReadWorkflowFiles_RejectsEscapingSymlink verifies readWorkflowFiles rejects a workflow symlink escaping the root.
func TestReadWorkflowFiles_RejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	workflowDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.yml")
	if err := os.WriteFile(outsideFile, []byte("name: escape\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workflowDir, "escape.yml")
	if err := os.Symlink(outsideFile, link); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
			t.Skipf("symlink creation requires privileges or developer mode on Windows: %v", err)
		}
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), workflowDiscoveryTimeout)
	defer cancel()
	files, err := readWorkflowFiles(ctx, root)
	if err == nil {
		t.Fatalf("readWorkflowFiles with escaping symlink succeeded: got %d files", len(files))
	}
}
