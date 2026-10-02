// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

// The hook policy resolves its interpreter and its make from fixed candidates, each proven
// before use (#339, #341), and the portability harness asserts the declared hook toolchain
// before the self-tests start any hook. The linter floors of the policy and the sources they
// come from are held together by scripts/test_portability_selftest.py, which reads the floors
// file through the policy's own reader.
const (
	hookLauncher          = ".config/lefthook/python.sh"
	hookToolchainCheck    = ".config/lefthook/scripts/toolchain.py --require"
	hookSelfTests         = "scripts/portability_selftest.py"
	hookToolchainRequired = "REQUIRED"
	// maxLauncherLines bounds the launcher scan; the file is a few dozen lines (HISS-02).
	maxLauncherLines = 256
)

// launcherCandidate matches one candidate line of the launcher: the words it proves and the
// words it then starts. Every line of the launcher ends in a comment sign, so a CRLF checkout
// of it still runs.
var launcherCandidate = regexp.MustCompile(`^if proven (.+); then exec (.+) "\$@"; fi #$`)

func engineText(t *testing.T, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(engineRoot, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}

// launcherCandidates returns the candidates the launcher tries, in order, and reports whether
// every one is started exactly as it was proven.
func launcherCandidates(text string) (candidates [][]string, faithful bool) {
	faithful = true
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines) && i < maxLauncherLines; i++ {
		match := launcherCandidate.FindStringSubmatch(strings.TrimRight(lines[i], "\r"))
		if match == nil {
			continue
		}
		faithful = faithful && match[1] == match[2]
		candidates = append(candidates, strings.Fields(match[1]))
	}
	return candidates, faithful
}

// HISS-19: one candidate list. The operator setting hooks.python defaults to it
// (config.DefaultOperatorSettings, consumed by internal/agenthook) and the shell launcher every
// Lefthook job starts through spells it out, because the launcher runs before any interpreter.
// Positive: the two are equal, in order. Negative and boundary: a candidate started under
// another name than it was proven with, a reordered list and a launcher without candidates
// each differ.
func TestHookLauncherCandidatesAreTheOperatorDefault(t *testing.T) {
	want := config.DefaultOperatorSettings().Hooks.Python
	got, faithful := launcherCandidates(engineText(t, hookLauncher))
	if !faithful || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s tries %v (started as proven: %v), want the hooks.python default %v", hookLauncher, got, faithful, want)
	}
	line := func(proved, started string) string {
		return "if proven " + proved + "; then exec " + started + ` "$@"; fi #`
	}
	cases := []struct {
		name     string
		text     string
		want     [][]string
		faithful bool
	}{
		{"positive", line("python3", "python3") + "\n" + line("py -3", "py -3") + "\n", [][]string{{"python3"}, {"py", "-3"}}, true},
		{"boundary crlf checkout", line("python3", "python3") + "\r\n", [][]string{{"python3"}}, true},
		{"negative started under another name", line("python3", "python") + "\n", [][]string{{"python3"}}, false},
		{"boundary no candidates", "#!/bin/sh\nset -eu\nexit 127\n", nil, true},
		{"boundary comment is no candidate", "# " + line("python3", "python3") + "\n", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, faithful := launcherCandidates(tc.text)
			if !reflect.DeepEqual(got, tc.want) || faithful != tc.faithful {
				t.Fatalf("launcherCandidates = %v, %v; want %v, %v", got, faithful, tc.want, tc.faithful)
			}
		})
	}
	if reordered, _ := launcherCandidates(line("python", "python") + "\n" + line("python3", "python3") + "\n"); reflect.DeepEqual(reordered, want[:2]) {
		t.Fatal("a reordered candidate list compares equal to the default")
	}
}

// hookToolchainGap names why a job does not assert the hook toolchain on every leg before its
// self-tests, or returns "" when it does.
func hookToolchainGap(job workflowJob) string {
	if advisoryJob(job.ContinueOnError) {
		return "job is advisory"
	}
	asserted := false
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		step := job.Steps[i]
		env, readable := stepEnv(step)
		switch {
		case strings.Contains(step.Run, hookToolchainCheck):
			if gap := hookToolchainStepGap(step, env, readable); gap != "" {
				return gap
			}
			asserted = true
		case strings.Contains(step.Run, hookSelfTests):
			return hookSelfTestGap(step, asserted)
		}
	}
	return "no step runs " + hookSelfTests
}

// hookToolchainStepGap judges the assertion step itself.
func hookToolchainStepGap(step workflowStep, env map[string]string, readable bool) string {
	switch {
	case !runsOnEveryLeg(step):
		return "toolchain assertion is conditional: " + step.If
	case !readable:
		return "toolchain assertion has no readable env block"
	}
	required := strings.Split(env[hookToolchainRequired], ",")
	for _, tool := range hookRequiredTools {
		if !slices.Contains(required, tool) {
			return "toolchain assertion does not require " + tool
		}
	}
	return ""
}

// hookRequiredTools are the programs no hook starts without: the interpreter and make the
// policy resolves (toolchain.RESOLVED in .config/lefthook/scripts) and the shell that runs every
// job. scripts/test_portability_selftest.py holds the workflow's list to the declaration itself.
var hookRequiredTools = []string{"python", "make", "sh"}

// hookSelfTestGap judges the self-test step against the assertion before it.
func hookSelfTestGap(step workflowStep, asserted bool) string {
	switch {
	case !asserted:
		return "self-tests run before the hook toolchain is asserted"
	case !runsOnEveryLeg(step):
		return "self-tests are conditional: " + step.If
	}
	return ""
}

// envNode builds a step env block from name and value pairs.
func envNode(pairs ...string) yaml.Node {
	node := yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i+1 < len(pairs) && i < maxStepsPerJob; i += 2 {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: pairs[i]},
			&yaml.Node{Kind: yaml.ScalarNode, Value: pairs[i+1]})
	}
	return node
}

// HISS-21 evidence for the hook toolchain. Positive: the real harness job asserts it on every
// leg, python, make and sh included, before the self-tests. Negative and boundary: no
// assertion, one after the self-tests, an OS-guarded one, one that does not require python,
// make or sh, conditional self-tests and an advisory job.
func TestPortabilityAssertsTheHookToolchainBeforeTheSelfTests(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	var spec workflowSpec
	if err := yaml.Unmarshal(workflows["portability.yml"], &spec); err != nil {
		t.Fatalf("parse portability.yml: %v", err)
	}
	if gap := hookToolchainGap(spec.Jobs["harness"]); gap != "" {
		t.Fatalf("portability harness: %s", gap)
	}
	every := "${{ !cancelled() }}"
	check := func(condition, required string) workflowStep {
		return workflowStep{Run: `"$PYTHON" -B ` + hookToolchainCheck + ` "$REQUIRED"`, If: condition,
			Env: envNode(hookToolchainRequired, required)}
	}
	tests := func(condition string) workflowStep {
		return workflowStep{Run: `"$PYTHON" -B ` + hookSelfTests, If: condition}
	}
	asserted := check(every, "python,make,sh,yamllint")
	cases := []struct {
		name string
		job  workflowJob
		want string
	}{
		{"positive", workflowJob{Steps: []workflowStep{asserted, tests(every)}}, ""},
		{"negative missing", workflowJob{Steps: []workflowStep{tests(every)}}, "before the hook toolchain is asserted"},
		{"negative no self-tests", workflowJob{Steps: []workflowStep{asserted}}, "no step runs"},
		{"boundary order", workflowJob{Steps: []workflowStep{tests(every), asserted}}, "before the hook toolchain is asserted"},
		{"boundary os guard", workflowJob{Steps: []workflowStep{check("runner.os != 'Windows'", "python,make,sh"), tests(every)}}, "assertion is conditional"},
		{"boundary python not required", workflowJob{Steps: []workflowStep{check(every, "make,sh,yamllint"), tests(every)}}, "does not require python"},
		{"boundary make not required", workflowJob{Steps: []workflowStep{check(every, "python,sh,yamllint"), tests(every)}}, "does not require make"},
		{"boundary sh not required", workflowJob{Steps: []workflowStep{check(every, "python,make,yamllint"), tests(every)}}, "does not require sh"},
		{"boundary conditional self-tests", workflowJob{Steps: []workflowStep{asserted, tests("runner.os == 'Linux'")}}, "self-tests are conditional"},
		{"boundary advisory", workflowJob{ContinueOnError: "true", Steps: []workflowStep{asserted, tests(every)}}, "advisory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hookToolchainGap(tc.job)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Fatalf("gap = %q, want containing %q", got, tc.want)
			}
		})
	}
}
