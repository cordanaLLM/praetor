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

	"github.com/cordanaLLM/praetor/internal/semver"
	"gopkg.in/yaml.v3"
)

// The hook linters are held to the version floors of one file (#343). Each floor names the
// place its version comes from, and this guard holds those places to it: a version this
// repository installs or measures against must never be older than the floor the hooks enforce.
const (
	hookToolFloors  = ".config/lefthook/tool-floors.txt"
	actionlintFile  = ".github/actionlint.yaml"
	shellcheckPin   = "SHELLCHECK_VERSION"
	shellcheckStep  = "Install Shell Linter"
	maxFloorLines   = 256
	yamllintPinName = "yamllint"
)

var (
	hookToolFloor = regexp.MustCompile(`^([a-z][a-z0-9_.-]*)>=(\d+\.\d+\.\d+)$`)
	yamllintPin   = regexp.MustCompile(`(?m)^yamllint==(\d+\.\d+\.\d+)\s*$`)
)

// hookToolFloorsOf returns the tool>=version lines of the floors file, comments aside.
func hookToolFloorsOf(text string) map[string]string {
	floors := map[string]string{}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines) && i < maxFloorLines; i++ {
		line, _, _ := strings.Cut(lines[i], "#")
		if match := hookToolFloor.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			floors[match[1]] = match[2]
		}
	}
	return floors
}

// floorGap names why an installed version does not satisfy a tool's floor, or returns "".
func floorGap(tool, installed string, floors map[string]string) string {
	floor, declared := floors[tool]
	if !declared {
		return tool + " has no floor in " + hookToolFloors
	}
	want, wantOK := semver.Parse(floor)
	got, gotOK := semver.Parse(installed)
	switch {
	case !wantOK:
		return tool + " floor " + floor + " is not a version"
	case !gotOK:
		return tool + " version " + installed + " is not a version"
	case semver.Compare(got, want) < 0:
		return tool + " " + installed + " is below the declared floor " + floor
	}
	return ""
}

func engineText(t *testing.T, relative string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(engineRoot, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(data)
}

// shellcheckVersion returns the release the portability harness installs.
func shellcheckVersion(t *testing.T) string {
	t.Helper()
	workflows, _ := engineWorkflows(t)
	var spec workflowSpec
	if err := yaml.Unmarshal(workflows["portability.yml"], &spec); err != nil {
		t.Fatalf("parse portability.yml: %v", err)
	}
	job := spec.Jobs["harness"]
	index := stepIndex(job, func(step workflowStep) bool { return step.Name == shellcheckStep })
	if index < 0 {
		t.Fatalf("portability harness has no %q step", shellcheckStep)
	}
	env, ok := stepEnv(job.Steps[index])
	if !ok || env[shellcheckPin] == "" {
		t.Fatalf("%q does not set %s", shellcheckStep, shellcheckPin)
	}
	return env[shellcheckPin]
}

// Positive: the floors file declares the four hook linters, the yamllint pin and the
// shellcheck release the Platform Neutrality legs install satisfy their floors, and
// .github/actionlint.yaml measures against the actionlint floor itself.
func TestInstalledHookLintersSatisfyTheDeclaredFloors(t *testing.T) {
	floors := hookToolFloorsOf(engineText(t, hookToolFloors))
	tools := make([]string, 0, len(floors))
	for tool := range floors {
		tools = append(tools, tool)
	}
	slices.Sort(tools)
	if want := []string{"actionlint", "hadolint", "shellcheck", "yamllint"}; !slices.Equal(tools, want) {
		t.Fatalf("%s declares %v, want %v", hookToolFloors, tools, want)
	}
	pin := yamllintPin.FindStringSubmatch(engineText(t, hookLintInput))
	if pin == nil {
		t.Fatalf("%s pins no yamllint version", hookLintInput)
	}
	if gap := floorGap(yamllintPinName, pin[1], floors); gap != "" {
		t.Errorf("%s: %s", hookLintInput, gap)
	}
	if gap := floorGap("shellcheck", shellcheckVersion(t), floors); gap != "" {
		t.Errorf("portability.yml %s: %s", shellcheckPin, gap)
	}
	if measured := "actionlint v" + floors["actionlint"]; !strings.Contains(engineText(t, actionlintFile), measured) {
		t.Errorf("%s does not name %s, the floor its runner-label measurement holds for", actionlintFile, measured)
	}
}

// Negative and boundary: a version below its floor, a tool without one and a value that is
// no version are each named; the floor itself and anything newer pass, with or without the
// tag prefix a release carries.
func TestHookToolFloorGuardFixtures(t *testing.T) {
	floors := hookToolFloorsOf("# header\n\nshellcheck>=0.11.0  # source\nyamllint>=1.38.0\r\nbroken==1.0.0\nloose >= 1.0.0\n")
	if len(floors) != 2 || floors["shellcheck"] != "0.11.0" || floors["yamllint"] != "1.38.0" {
		t.Fatalf("hookToolFloorsOf = %v", floors)
	}
	cases := []struct {
		name, tool, installed, want string
	}{
		{"at the floor", "shellcheck", "0.11.0", ""},
		{"tagged release at the floor", "shellcheck", "v0.11.0", ""},
		{"newer", "yamllint", "1.39.0", ""},
		{"numeric, not textual", "shellcheck", "0.9.0", "below the declared floor 0.11.0"},
		{"one patch short", "yamllint", "1.37.9", "below the declared floor 1.38.0"},
		{"no floor", "hadolint", "2.14.0", "has no floor"},
		{"not a version", "shellcheck", "stable", "is not a version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := floorGap(tc.tool, tc.installed, floors)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Fatalf("floorGap = %q, want containing %q", got, tc.want)
			}
		})
	}
}
