// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// pipedRunsWithoutPipefail returns the 1-based line of every RUN instruction in dockerfile that
// holds a pipe while no SHELL setting -o pipefail is in force, the condition of Hadolint's
// DL4006. A SHELL lasts until the next SHELL or the end of its build stage, as in Docker.
// Instructions are read one per line, so a rendering with a line continuation is refused by
// the callers first.
func pipedRunsWithoutPipefail(dockerfile string) []int {
	var lines []int
	pipefail := false
	for index, line := range strings.Split(dockerfile, "\n") {
		instruction, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch strings.ToUpper(instruction) {
		case "FROM":
			pipefail = false
		case "SHELL":
			pipefail = shellSetsPipefail(rest)
		case "RUN":
			if !pipefail && util.ShellPipes(rest) {
				lines = append(lines, index+1)
			}
		}
	}
	return lines
}

// shellSetsPipefail reports whether the JSON argument vector of a SHELL instruction sets
// -o pipefail, alone or closing a cluster such as -eo.
func shellSetsPipefail(arguments string) bool {
	var argv []string
	if json.Unmarshal([]byte(arguments), &argv) != nil {
		return false
	}
	for i := 1; i+1 < len(argv); i++ {
		if strings.HasPrefix(argv[i], "-") && !strings.HasPrefix(argv[i], "--") && strings.HasSuffix(argv[i], "o") && argv[i+1] == "pipefail" {
			return true
		}
	}
	return false
}

// renderedSpec returns the specification of a ready bundle prepared from the source fixture.
func renderedSpec(t *testing.T) *BootstrapSpec {
	t.Helper()
	spec := preparedBootstrap(t).Spec()
	if spec == nil || spec.State != BootstrapReady {
		t.Fatalf("fixture bundle is not ready: %+v", spec)
	}
	return spec
}

// Positive: the rendered Dockerfile has no RUN whose pipe could hide a failure, and no line
// continuation the line-wise check would misread (#351).
func TestBootstrapDockerfilePipesFollowPipefailShell(t *testing.T) {
	dockerfile := renderBootstrapDockerfile(renderedSpec(t))
	if strings.Contains(dockerfile, "\\\n") {
		t.Fatalf("rendering carries a line continuation the check does not read:\n%s", dockerfile)
	}
	if lines := pipedRunsWithoutPipefail(dockerfile); len(lines) != 0 {
		t.Fatalf("RUN lines %v pipe without a pipefail SHELL before them:\n%s", lines, dockerfile)
	}
}

// Negative: the rendering before #351 piped twice without pipefail, and the check names both
// lines, so it fails on the code the change replaced.
func TestPipefailCheckRefusesThePipedRendering(t *testing.T) {
	spec := renderedSpec(t)
	dockerfile := renderBootstrapDockerfileAs(spec, dockerfilePiped)
	lines := pipedRunsWithoutPipefail(dockerfile)
	first := 4 + spec.ArchiveParts // comment, FROM, WORKDIR and one COPY per part come first
	if !slices.Equal(lines, []int{first, first + 1}) {
		t.Fatalf("piped rendering flagged at %v; want [%d %d]:\n%s", lines, first, first+1, dockerfile)
	}
}

// Boundary: a pipefail SHELL covers the RUN lines after it in its own stage only, a SHELL
// without pipefail or a later one that drops it covers none, and || or a quoted | is no pipe.
func TestPipefailCheckBoundaries(t *testing.T) {
	const ash = `SHELL ["/bin/ash", "-eo", "pipefail", "-c"]`
	for _, tc := range []struct {
		name, dockerfile string
		want             []int
	}{
		{"no shell", "FROM a\nRUN x | y", []int{2}},
		{"ash pipefail", "FROM a\n" + ash + "\nRUN x | y", nil},
		{"bash pipefail", "FROM a\n" + `SHELL ["/bin/bash", "-o", "pipefail", "-c"]` + "\nRUN x | y", nil},
		{"shell without pipefail", "FROM a\n" + `SHELL ["/bin/sh", "-c"]` + "\nRUN x | y", []int{3}},
		{"pipefail dropped", "FROM a\n" + ash + "\n" + `SHELL ["/bin/sh", "-c"]` + "\nRUN x | y", []int{4}},
		{"new stage", "FROM a\n" + ash + "\nRUN x | y\nFROM b\nRUN x | y", []int{5}},
		{"lowercase run", "FROM a\nrun x | y", []int{2}},
		{"or operator", "FROM a\nRUN x || y", nil},
		{"quoted pipe", "FROM a\nRUN echo 'x|y' \"a|b\"", nil},
		{"pipefail as text", "FROM a\n" + `SHELL ["/bin/sh", "-c", "pipefail"]` + "\nRUN x | y", []int{3}},
	} {
		if got := pipedRunsWithoutPipefail(tc.dockerfile); !slices.Equal(got, tc.want) {
			t.Errorf("%s: flagged %v; want %v", tc.name, got, tc.want)
		}
	}
}

// hadolintFindings runs hadolint on dockerfile and returns the rule codes it reports.
func hadolintFindings(t *testing.T, bin, dockerfile string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(path, []byte(dockerfile), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--no-fail", "--no-color", "--format", "json", path).Output()
	if err != nil {
		t.Fatalf("hadolint on %s: %v", path, err)
	}
	var findings []struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(out, &findings); err != nil {
		t.Fatalf("hadolint output is not its JSON format: %v\n%s", err, out)
	}
	codes := make([]string, 0, len(findings))
	for _, finding := range findings {
		codes = append(codes, finding.Code)
	}
	return codes
}

// Hadolint itself, where it is installed: the current rendering draws no DL4006, and the piped
// rendering before #351 draws it, so the run is seen to fail on the defect it guards.
func TestRenderedBootstrapDockerfilePassesHadolintDL4006(t *testing.T) {
	bin, err := exec.LookPath("hadolint")
	if err != nil {
		t.Skip("hadolint is not on PATH; this check runs where it is installed, and TestBootstrapDockerfilePipesFollowPipefailShell enforces DL4006 structurally everywhere")
	}
	spec := renderedSpec(t)
	if codes := hadolintFindings(t, bin, renderBootstrapDockerfile(spec)); slices.Contains(codes, "DL4006") {
		t.Fatalf("hadolint reports DL4006 on the rendering: %v", codes)
	}
	if codes := hadolintFindings(t, bin, renderBootstrapDockerfileAs(spec, dockerfilePiped)); !slices.Contains(codes, "DL4006") {
		t.Fatalf("hadolint no longer reports DL4006 on the piped rendering, so this test cannot fail: %v", codes)
	}
}
