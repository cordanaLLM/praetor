// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	slashpath "path"
	"strings"

	"gopkg.in/yaml.v3"
)

// The shell a run: step runs under, as GitHub documents it (workflow syntax,
// jobs.<job_id>.steps[*].shell, defaults.run and jobs.<job_id>.container, read 2026-10-01 at
// docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax):
//
//   - The step's shell: wins, then the job's defaults.run.shell, then the workflow's
//     defaults.run.shell ("GitHub uses the most specific default setting").
//   - A built-in keyword names its program: bash, pwsh, python, sh, cmd, powershell. Any other
//     value is a custom template `command [options] {0}`, whose first whitespace-delimited word
//     is the command.
//   - Without any of them, a job in a container runs sh, a Windows runner runs pwsh, and a
//     Linux or macOS runner runs bash (`bash -e {0}`, sh where bash is missing).
//
// A value holding an expression (${{ ... }}) is decided only when the workflow runs, and so is
// the default of a job whose runner labels name no single operating system, such as
// `runs-on: ${{ matrix.os }}` or a self-hosted runner without an OS label. StepShell reports
// such a step's shell as unresolved (the empty string) instead of guessing it.

const (
	// expressionOpen starts a GitHub Actions expression.
	expressionOpen = "${{"
	// maxRunnerLabelsPerJob bounds the labels read from one job's runs-on (HISS-02).
	maxRunnerLabelsPerJob = 64
	// runsOnLabelsKey is the key of a runs-on mapping that holds its labels; the mapping's other
	// key, group, names a runner group, not a label.
	runsOnLabelsKey = "labels"
)

// Runner operating systems, as RunnerOS names them.
const (
	OSLinux   = "linux"
	OSMacOS   = "macos"
	OSWindows = "windows"
)

// StepShell returns the program GitHub runs step's script with, in job of spec: a built-in
// keyword (bash, sh, pwsh, powershell, cmd, python) or the command of a custom template
// (perl for `perl {0}`), lower-cased and without a directory or an .exe suffix. It returns ""
// when the shell is decided only when the workflow runs.
func StepShell(spec *Spec, job *Job, step *Step) string {
	if value := StepShellValue(spec, job, step); value != "" {
		return shellProgram(value)
	}
	return defaultShell(job)
}

// StepShellValue returns the shell: value that decides step's shell in job of spec, trimmed but
// otherwise as written: the step's own, else the job's defaults.run.shell, else the workflow's.
// It returns "" when none is set and the runner's default shell runs the step. A caller that
// must tell a built-in keyword from a custom template (`bash {0}`) reads it here; StepShell
// reduces both to the program they run.
func StepShellValue(spec *Spec, job *Job, step *Step) string {
	for _, value := range [...]string{step.Shell, job.Defaults.Run.Shell, spec.Defaults.Run.Shell} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// shellProgram returns the program a shell: value names: the first word of a custom template, a
// built-in keyword as itself, or "" for a value an expression decides.
func shellProgram(value string) string {
	if strings.Contains(value, expressionOpen) {
		return ""
	}
	command := strings.Fields(value)[0]
	command = slashpath.Base(strings.ReplaceAll(command, `\`, "/"))
	return strings.TrimSuffix(strings.ToLower(command), ".exe")
}

// defaultShell returns the shell a job's runner starts a step with when nothing names one: sh in
// a container, which GitHub runs only on Linux, pwsh on Windows, bash on Linux and macOS, and ""
// when the runner's operating system is not known from the workflow alone.
func defaultShell(job *Job) string {
	if hasContainer(&job.Container) {
		return "sh"
	}
	switch RunnerOS(&job.RunsOn) {
	case OSWindows:
		return "pwsh"
	case OSLinux, OSMacOS:
		return "bash"
	}
	return ""
}

// hasContainer reports a container: value that names an image or holds a mapping. An empty or
// null value runs the steps on the runner itself.
func hasContainer(node *yaml.Node) bool {
	switch node.Kind {
	case yaml.MappingNode:
		return true
	case yaml.ScalarNode:
		return node.ShortTag() != "!!null" && strings.TrimSpace(node.Value) != ""
	}
	return false
}

// RunnerOS returns the operating system a runs-on value's labels name, or "" when they name
// none or more than one. A GitHub-hosted image label (ubuntu-*, windows-*, macos-*) and a
// self-hosted runner's OS label (linux, windows, macOS) name one, case-insensitively; an
// expression, a runner group and a custom label name none.
func RunnerOS(runsOn *yaml.Node) string {
	found := ""
	for _, label := range RunnerLabels(runsOn) {
		system := labelOS(strings.ToLower(label))
		switch {
		case system == "" || system == found:
		case found != "":
			return ""
		default:
			found = system
		}
	}
	return found
}

// labelOS returns the operating system one lower-cased runner label names, or "".
func labelOS(label string) string {
	switch {
	case label == OSLinux || strings.HasPrefix(label, "ubuntu-"):
		return OSLinux
	case label == OSWindows || strings.HasPrefix(label, "windows-"):
		return OSWindows
	case label == OSMacOS || strings.HasPrefix(label, "macos-"):
		return OSMacOS
	}
	return ""
}

// RunnerLabels returns the literal labels of one runs-on value, in order: the value itself when
// it is a string, every string of a list, and the same of a mapping's labels value. A value
// holding an expression (${{ ... }}) is resolved only when the workflow runs, so it names no
// label here; nor does a runner group.
func RunnerLabels(runsOn *yaml.Node) []string {
	entries := runnerLabelNodes(runsOn)
	labels := make([]string, 0, len(entries))
	for i := 0; i < len(entries) && i < maxRunnerLabelsPerJob; i++ {
		label := strings.TrimSpace(entries[i].Value)
		if entries[i].Kind == yaml.ScalarNode && label != "" && !strings.Contains(label, expressionOpen) {
			labels = append(labels, label)
		}
	}
	return labels
}

// runnerLabelNodes returns the nodes one runs-on value names its labels with: the value itself
// when it is a string, its entries when it is a list, and the same of its labels value when it
// is a mapping.
func runnerLabelNodes(runsOn *yaml.Node) []*yaml.Node {
	value := runsOn
	if value.Kind == yaml.MappingNode {
		value = nil
		for i := 0; i+1 < len(runsOn.Content) && i < 2*maxRunnerLabelsPerJob; i += 2 {
			if runsOn.Content[i].Value == runsOnLabelsKey {
				value = runsOn.Content[i+1]
			}
		}
	}
	switch {
	case value == nil:
		return nil
	case value.Kind == yaml.ScalarNode:
		return []*yaml.Node{value}
	case value.Kind == yaml.SequenceNode:
		return value.Content
	default:
		return nil
	}
}
