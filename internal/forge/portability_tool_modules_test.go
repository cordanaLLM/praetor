// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"strings"
	"testing"
)

const (
	toolModulesStep = "- name: Download Pinned Tool Modules"
	testPackageStep = "- name: Test Every Package"
	toolModulesRun  = "run: go mod download -modfile=tools/go/go.mod"
)

// toolModulesFirstProblem reports how the matrix departs from downloading the pinned tool
// modules, in bash, before it tests every package; "" when it holds that shape.
func toolModulesFirstProblem(workflow string) string {
	download, tests := strings.Index(workflow, toolModulesStep), strings.Index(workflow, testPackageStep)
	switch {
	case download < 0:
		return "no " + toolModulesStep + " step"
	case tests < 0:
		return "no " + testPackageStep + " step"
	case download > tests:
		return "the tool modules download after the tests"
	}
	step := workflow[download:tests]
	if !strings.Contains(step, toolModulesRun) || !strings.Contains(step, "shell: bash") {
		return "the download step does not run " + toolModulesRun + " in bash"
	}
	return ""
}

// Positive (main c565cba50, macOS leg): the pinned tool modules are in the module cache before
// the tests that build the pinned gosec and install the fallback scanners under short bounds.
func TestPortability_ToolModulesDownloadBeforeTests(t *testing.T) {
	if problem := toolModulesFirstProblem(readPortability(t)); problem != "" {
		t.Fatalf("%s: %s", portabilityWorkflow, problem)
	}
}

// Negative: a matrix without the step, with it after the tests, or with another command or
// shell is refused.
func TestPortability_Negative_PlantedToolModuleStepsAreRefused(t *testing.T) {
	workflow := readPortability(t)
	planted := map[string]string{
		"no step":       strings.Replace(workflow, toolModulesStep, "- name: Download Something Else", 1),
		"after tests":   strings.Replace(strings.Replace(workflow, testPackageStep, "- name: TMP", 1), toolModulesStep, testPackageStep, 1),
		"other command": strings.Replace(workflow, toolModulesRun, "run: go mod download", 1),
		"default shell": strings.Replace(workflow, toolModulesStep+"\n        if: ${{ !cancelled() && github.event.pull_request.draft != true }}\n        shell: bash", toolModulesStep+"\n        if: ${{ !cancelled() && github.event.pull_request.draft != true }}", 1),
	}
	for name, text := range planted {
		if text == workflow {
			t.Fatalf("%s: the planted change did not apply", name)
		}
		if toolModulesFirstProblem(text) == "" {
			t.Errorf("%s: a broken matrix passed", name)
		}
	}
}
