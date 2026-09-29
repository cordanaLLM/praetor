// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// dottedDashArgument matches an unquoted native argument that starts with a dash and holds a
// period. PowerShell splits such an argument at the period before the native program sees it
// (PowerShell/PowerShell#6291): the portability job's gosec install handed go
// `-modfile=tools/go/go` and `.mod` on the Windows leg, and every later harness self-test that
// needs gosec failed with it (#558).
var dottedDashArgument = regexp.MustCompile("(?:^|\\s)(-[^\\s\"'`]*\\.[^\\s\"'`]*)")

// maxArgumentsPerStep bounds the matches read from one run script (HISS-02).
const maxArgumentsPerStep = 64

// windowsLeg reports whether one of job's matrix legs runs on a Windows image. The leg is
// identified by its image family, not one label, as in the Markdown gate check.
func windowsLeg(job workflowJob) bool {
	images := matrixOSLegs(job)
	for i := 0; i < len(images) && i < maxMatrixLegs; i++ {
		if strings.HasPrefix(images[i], "windows-") {
			return true
		}
	}
	return false
}

// powerShellStep reports whether step's script runs under PowerShell on a Windows runner: the
// default shell there, or pwsh or powershell named outright. No engine workflow sets
// defaults.run.shell, so an empty shell is the runner default.
func powerShellStep(step workflowStep) bool {
	switch strings.TrimSpace(step.Shell) {
	case "", "pwsh", "powershell":
		return step.Run != ""
	}
	return false
}

// powerShellSplitArguments lists each argument that PowerShell would split in a job with a
// Windows leg, prefixed with its step's name.
func powerShellSplitArguments(job workflowJob) []string {
	if !windowsLeg(job) {
		return nil
	}
	var found []string
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		step := job.Steps[i]
		if !powerShellStep(step) {
			continue
		}
		for _, match := range dottedDashArgument.FindAllStringSubmatch(step.Run, maxArgumentsPerStep) {
			found = append(found, step.Name+": "+match[1])
		}
	}
	return found
}

// Positive: no engine workflow passes a dotted dash argument through PowerShell, and the
// portability harness, which has the Windows leg, still installs gosec from tools/go/go.mod,
// so the guard judges the step that failed rather than passing vacuously.
func TestPortabilityPassesNoDottedDashArgumentThroughPowerShell(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	for name, data := range workflows {
		var spec workflowSpec
		if err := yaml.Unmarshal(data, &spec); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for id, job := range spec.Jobs {
			if split := powerShellSplitArguments(job); len(split) != 0 {
				t.Errorf("%s job %s passes arguments PowerShell splits at the period: %q", name, id, split)
			}
		}
	}
	var portability workflowSpec
	if err := yaml.Unmarshal(workflows["portability.yml"], &portability); err != nil {
		t.Fatalf("parse portability.yml: %v", err)
	}
	harness := portability.Jobs["harness"]
	installs := false
	for i := 0; i < len(harness.Steps) && i < maxStepsPerJob; i++ {
		installs = installs || strings.Contains(harness.Steps[i].Run, "-modfile=tools/go/go.mod")
	}
	if !windowsLeg(harness) || !installs {
		t.Fatalf("portability harness: Windows leg %t, installs from tools/go/go.mod %t", windowsLeg(harness), installs)
	}
}

// Negative and boundary: a dotted dash argument under the default shell or pwsh on a Windows
// leg is found; bash, a quoted argument, a dash argument without a period and a job without a
// Windows leg are not.
func TestPowerShellSplitArgumentFixtures(t *testing.T) {
	const install = "go install -modfile=tools/go/go.mod github.com/securego/gosec/v2/cmd/gosec"
	job := func(os, shell, run string) workflowJob {
		var spec workflowJob
		spec.Strategy.Matrix = includeOSMatrix("ubuntu-26.04", os)
		spec.Steps = []workflowStep{{Name: "Install", Shell: shell, Run: run}}
		return spec
	}
	cases := []struct {
		name string
		job  workflowJob
		want string
	}{
		{"default shell", job("windows-2025", "", install), "Install: -modfile=tools/go/go.mod"},
		{"explicit pwsh", job("windows-latest", "pwsh", install), "Install: -modfile=tools/go/go.mod"},
		{"second line", job("windows-2025", "", "go vet ./...\ngo test -coverprofile=cover.out ./..."), "Install: -coverprofile=cover.out"},
		{"bash", job("windows-2025", "bash", install), ""},
		{"quoted", job("windows-2025", "", `go install "-modfile=tools/go/go.mod" example.com/tool`), ""},
		{"no period", job("windows-2025", "", "node tools/markdownlint/verify.mjs --self-test"), ""},
		{"no windows leg", job("macos-26", "", install), ""},
		{"no run", job("windows-2025", "", ""), ""},
	}
	for _, tc := range cases {
		got := strings.Join(powerShellSplitArguments(tc.job), "; ")
		if got != tc.want {
			t.Errorf("%s: found %q, want %q", tc.name, got, tc.want)
		}
	}
}
