// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// stepShellOf decodes a one-job workflow and resolves the shell of its first step.
func stepShellOf(t *testing.T, document string) string {
	t.Helper()
	spec, err := Parse([]byte(document))
	if err != nil {
		t.Fatalf("parse %q: %v", document, err)
	}
	job := spec.Jobs["j"]
	if len(job.Steps) == 0 {
		t.Fatalf("no step in %q", document)
	}
	return StepShell(&spec, &job, &job.Steps[0])
}

// Positive: the step's shell: wins over the job's defaults.run.shell, which wins over the
// workflow's; without any, the runner's operating system decides, and a container runs sh.
func TestStepShell_Precedence(t *testing.T) {
	cases := map[string]string{
		"defaults: {run: {shell: sh}}\njobs:\n  j:\n    runs-on: windows-latest\n    defaults: {run: {shell: python}}\n    steps:\n      - {run: x, shell: bash}\n": "bash",
		"defaults: {run: {shell: sh}}\njobs:\n  j:\n    runs-on: windows-latest\n    defaults: {run: {shell: python}}\n    steps:\n      - run: x\n":                "python",
		"defaults: {run: {shell: sh}}\njobs:\n  j:\n    runs-on: windows-latest\n    steps:\n      - run: x\n":                                                      "sh",
		"jobs:\n  j:\n    runs-on: windows-2025\n    steps:\n      - run: x\n":                                                                                      "pwsh",
		"jobs:\n  j:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: x\n":                                                                                      "bash",
		"jobs:\n  j:\n    runs-on: macos-26\n    steps:\n      - run: x\n":                                                                                          "bash",
		"jobs:\n  j:\n    runs-on: [self-hosted, Linux, x64]\n    steps:\n      - run: x\n":                                                                         "bash",
		"jobs:\n  j:\n    runs-on: {group: large, labels: [self-hosted, windows]}\n    steps:\n      - run: x\n":                                                    "pwsh",
		"jobs:\n  j:\n    runs-on: ubuntu-latest\n    container: node:24\n    steps:\n      - run: x\n":                                                             "sh",
		"jobs:\n  j:\n    runs-on: ubuntu-latest\n    container: {image: node:24}\n    steps:\n      - run: x\n":                                                    "sh",
	}
	for document, want := range cases {
		if got := stepShellOf(t, document); got != want {
			t.Errorf("StepShell = %q, want %q for\n%s", got, want, document)
		}
	}
}

// Positive and boundary: a custom template names its command, without a directory, an .exe
// suffix or upper case, so `/usr/bin/bash -x {0}` is bash and `perl {0}` is perl.
func TestStepShell_CustomTemplates(t *testing.T) {
	for shell, want := range map[string]string{
		"bash {0}": "bash", "/usr/bin/bash -euo pipefail {0}": "bash", `C:\tools\Bash.EXE {0}`: "bash",
		"perl {0}": "perl", "  sh -e {0}  ": "sh", "cmd": "cmd", "powershell": "powershell",
	} {
		document := "jobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: x\n        shell: '" + shell + "'\n"
		if got := stepShellOf(t, document); got != want {
			t.Errorf("shell %q resolved to %q, want %q", shell, got, want)
		}
	}
}

// Negative: a shell an expression names, and a default on a runner whose labels name no single
// operating system, are decided only when the workflow runs and resolve to "".
func TestStepShell_UnresolvedWhenTheRunDecides(t *testing.T) {
	for _, document := range []string{
		"jobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - {run: x, shell: '${{ matrix.shell }}'}\n",
		"jobs:\n  j:\n    runs-on: ubuntu-latest\n    defaults: {run: {shell: '${{ inputs.shell }}'}}\n    steps:\n      - run: x\n",
		"jobs:\n  j:\n    runs-on: ${{ matrix.os }}\n    steps:\n      - run: x\n",
		"jobs:\n  j:\n    runs-on: [self-hosted, linux, windows]\n    steps:\n      - run: x\n",
		"jobs:\n  j:\n    runs-on: arc-runner-set-linux-amd64\n    steps:\n      - run: x\n",
		"jobs:\n  j:\n    runs-on: {group: large}\n    steps:\n      - run: x\n",
		"jobs:\n  j:\n    steps:\n      - run: x\n",
	} {
		if got := stepShellOf(t, document); got != "" {
			t.Errorf("StepShell = %q, want unresolved for\n%s", got, document)
		}
	}
}

// Boundary: an empty or null container runs the steps on the runner itself, so the runner's
// default applies.
func TestStepShell_EmptyContainerIsTheRunner(t *testing.T) {
	for _, container := range []string{"''", "null", "~"} {
		document := "jobs:\n  j:\n    runs-on: ubuntu-latest\n    container: " + container + "\n    steps:\n      - run: x\n"
		if got := stepShellOf(t, document); got != "bash" {
			t.Errorf("container %s: StepShell = %q, want bash", container, got)
		}
	}
}

// Positive, negative and boundary: RunnerOS reads one operating system from literal labels and
// none from an expression, a group, a custom label or two systems at once.
func TestRunnerOS(t *testing.T) {
	for value, want := range map[string]string{
		"ubuntu-slim": OSLinux, "windows-11-arm": OSWindows, "macos-latest": OSMacOS,
		"[self-hosted, macOS, ARM64]": OSMacOS, "[ubuntu-24.04, linux]": OSLinux,
		"[ubuntu-24.04, windows-2025]": "", "${{ matrix.os }}": "", "gpu-runner": "", "{group: g}": "", "''": "",
	} {
		var node yaml.Node
		if err := yaml.Unmarshal([]byte("runs-on: "+value), &node); err != nil {
			t.Fatal(err)
		}
		runsOn := node.Content[0].Content[1]
		if got := RunnerOS(runsOn); got != want {
			t.Errorf("RunnerOS(%s) = %q, want %q", value, got, want)
		}
	}
}
