// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The hook lint toolchain (black, flake8, yamllint) is pinned once, in requirements.in and
// the hash-locked requirements.txt compiled from it. make hooks-lint and the portability job
// both install that lock, so a version bump moves every consumer together (HISS-19).
const (
	hookLintInput = ".config/hook-lint/requirements.in"
	hookLintLock  = ".config/hook-lint/requirements.txt"
	// maxHookLintLines bounds the requirements.in scan; the file holds a handful of pins.
	maxHookLintLines = 256
)

var hookLintPin = regexp.MustCompile(`^([A-Za-z0-9_.-]+)==`)

// hookLintTools returns the package names requirements.in pins with ==.
func hookLintTools(in string) []string {
	var tools []string
	lines := strings.Split(in, "\n")
	for i := 0; i < len(lines) && i < maxHookLintLines; i++ {
		if match := hookLintPin.FindStringSubmatch(strings.TrimSpace(lines[i])); match != nil {
			tools = append(tools, strings.ToLower(match[1]))
		}
	}
	return tools
}

// rawHookLintPins returns "<job>: <step>" for each step whose run string pins one of tools
// by version itself instead of installing the lock.
func rawHookLintPins(spec workflowSpec, tools []string) []string {
	jobs := make([]string, 0, len(spec.Jobs))
	for name := range spec.Jobs {
		jobs = append(jobs, name)
	}
	slices.Sort(jobs)
	var found []string
	for i := 0; i < len(jobs) && i < maxJobsPerFile; i++ {
		steps := spec.Jobs[jobs[i]].Steps
		for j := 0; j < len(steps) && j < maxStepsPerJob; j++ {
			if pinsHookLintTool(steps[j].Run, tools) {
				found = append(found, jobs[i]+": "+steps[j].Name)
			}
		}
	}
	return found
}

// pinsHookLintTool reports whether run names one of tools with an == version pin.
func pinsHookLintTool(run string, tools []string) bool {
	lower := strings.ToLower(run)
	for i := 0; i < len(tools) && i < maxHookLintLines; i++ {
		if strings.Contains(lower, tools[i]+"==") {
			return true
		}
	}
	return false
}

// installsHookLintLock reports whether a step of job installs the lock with hash checking.
func installsHookLintLock(job workflowJob) bool {
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		run := job.Steps[i].Run
		if strings.Contains(run, "--require-hashes") && strings.Contains(run, "-r "+hookLintLock) {
			return true
		}
	}
	return false
}

func engineHookLintTools(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(engineRoot, filepath.FromSlash(hookLintInput)))
	if err != nil {
		t.Fatalf("read %s: %v", hookLintInput, err)
	}
	tools := hookLintTools(string(data))
	if !slices.Contains(tools, "yamllint") {
		t.Fatalf("%s pins %v, want yamllint among them", hookLintInput, tools)
	}
	return tools
}

// Positive: no engine workflow pins a hook lint tool itself, and the portability harness,
// which runs checks.py's yamllint on every leg, installs the hash-locked lock.
func TestWorkflowsInstallHookLintToolsOnlyFromTheLock(t *testing.T) {
	tools := engineHookLintTools(t)
	workflows, _ := engineWorkflows(t)
	for name, data := range workflows {
		var spec workflowSpec
		if err := yaml.Unmarshal(data, &spec); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if raw := rawHookLintPins(spec, tools); len(raw) != 0 {
			t.Errorf("%s pins a hook lint tool outside %s: %v", name, hookLintLock, raw)
		}
	}
	var portability workflowSpec
	if err := yaml.Unmarshal(workflows["portability.yml"], &portability); err != nil {
		t.Fatalf("parse portability.yml: %v", err)
	}
	if !installsHookLintLock(portability.Jobs["harness"]) {
		t.Fatalf("portability harness does not install %s with --require-hashes", hookLintLock)
	}
}

// Negative and boundary: a raw pin is found whatever its case, an unpinned mention or a
// lock install is not, and a lock install without hash checking does not count.
func TestHookLintPinGuardFixtures(t *testing.T) {
	tools := hookLintTools("# comment==1\nblack==26.5.1\n  flake8==7.4.1\nYamllint==1.38.0\n\n")
	if want := []string{"black", "flake8", "yamllint"}; !slices.Equal(tools, want) {
		t.Fatalf("hookLintTools = %v, want %v", tools, want)
	}
	job := func(runs ...string) workflowJob {
		steps := make([]workflowStep, 0, len(runs))
		for _, run := range runs {
			steps = append(steps, workflowStep{Name: "step", Run: run})
		}
		return workflowJob{Steps: steps}
	}
	lock := `"$PYTHON" -m pip install --require-hashes -r ` + hookLintLock
	cases := []struct {
		name      string
		run       string
		raw, lock bool
	}{
		{"raw pin", `"$PYTHON" -m pip install yamllint==1.38.0`, true, false},
		{"raw pin, other case", "pip install YAMLlint==1.38.0", true, false},
		{"lock with hashes", lock, false, true},
		{"lock without hashes", "pip install -r " + hookLintLock, false, false},
		{"unpinned mention", "yamllint --version", false, false},
	}
	for _, tc := range cases {
		spec := workflowSpec{Jobs: map[string]workflowJob{"harness": job(tc.run)}}
		if got := len(rawHookLintPins(spec, tools)) != 0; got != tc.raw {
			t.Errorf("%s: raw pin found = %v, want %v", tc.name, got, tc.raw)
		}
		if got := installsHookLintLock(spec.Jobs["harness"]); got != tc.lock {
			t.Errorf("%s: installs lock = %v, want %v", tc.name, got, tc.lock)
		}
	}
	if len(rawHookLintPins(workflowSpec{}, tools)) != 0 || installsHookLintLock(workflowJob{}) {
		t.Fatal("an empty workflow reported a pin or a lock install")
	}
}
