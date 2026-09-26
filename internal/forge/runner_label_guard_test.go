package forge

// Every GitHub-hosted runner in this repository names an explicit image. An alias such as
// ubuntu-latest retargets a job the day GitHub promotes the next image: ubuntu-latest still
// resolved to 24.04 when the tree moved to ubuntu-26.04, so every job using it silently ran a
// release behind the baseline, and two jobs added after that move reintroduced the alias with
// nothing to notice. internal/config/hierarchy.go and docs/standards/hiss-21-platform-neutrality.md
// state the same rule for the Darwin defaults and the portability matrix.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// maxWorkflowLines bounds how many lines one workflow scan reads (HISS-02). The largest
// workflow in this tree is under 400 lines.
const maxWorkflowLines = 20000

// floatingRunnerLabel matches a hosted image alias and its larger-runner forms
// (macos-latest-large, macos-latest-xlarge).
var floatingRunnerLabel = regexp.MustCompile(`\b(?:ubuntu|macos|windows)-latest(?:-[a-z0-9]+)?\b`)

// runnerLabelKey matches the two keys that name a runner in a workflow: a job's runs-on and a
// matrix entry's os, in scalar or flow-sequence form.
var runnerLabelKey = regexp.MustCompile(`^\s*(?:-\s+)?(?:runs-on|os):\s*(.*)$`)

// floatingRunnerLabels reports every runner alias one workflow names, as "<name>:<line>: <label>".
// Comments are ignored: prose explaining why an alias is avoided is not a runner.
func floatingRunnerLabels(name string, data []byte) []string {
	lines := strings.Split(string(data), "\n")
	var found []string
	for i := 0; i < len(lines) && i < maxWorkflowLines; i++ {
		match := runnerLabelKey.FindStringSubmatch(strings.TrimRight(lines[i], "\r"))
		if match == nil {
			continue
		}
		value, _, _ := strings.Cut(match[1], "#")
		for _, label := range floatingRunnerLabel.FindAllString(value, -1) {
			found = append(found, fmt.Sprintf("%s:%d: %s", name, i+1, label))
		}
	}
	return found
}

// Positive: every runner in the real workflow files is an explicit image.
func TestEngineWorkflowsNameExplicitRunnerImages(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	if len(workflows) == 0 {
		t.Fatal("no workflow files read; the guard would pass vacuously")
	}
	for name, data := range workflows {
		for _, finding := range floatingRunnerLabels(name, data) {
			t.Errorf("%s names a floating runner alias; pin the image label (ubuntu-26.04, macos-26, windows-2025)", finding)
		}
	}
}

// Negative and boundary: the scan finds aliases in every form a workflow can carry one, and
// stays silent on explicit images, expressions and comments.
func TestFloatingRunnerLabelsSyntheticShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		document string
		want     []string
	}{
		"job runner alias":        {"jobs:\n  a:\n    runs-on: ubuntu-latest\n", []string{"w.yml:3: ubuntu-latest"}},
		"matrix entry alias":      {"        include:\n          - os: windows-latest\n", []string{"w.yml:2: windows-latest"}},
		"flow sequence aliases":   {"        os: [ubuntu-latest, macos-latest]\n", []string{"w.yml:1: ubuntu-latest", "w.yml:1: macos-latest"}},
		"larger runner alias":     {"    runs-on: [self-hosted, macos-latest-xlarge]\n", []string{"w.yml:1: macos-latest-xlarge"}},
		"CRLF line":               {"    runs-on: ubuntu-latest\r\n", []string{"w.yml:1: ubuntu-latest"}},
		"explicit image":          {"    runs-on: ubuntu-26.04\n          - os: macos-26\n", nil},
		"matrix expression":       {"    runs-on: ${{ matrix.os }}\n", nil},
		"alias in a comment":      {"      # macos-latest already resolves to macOS 26\n", nil},
		"alias in trailing note":  {"    runs-on: ubuntu-26.04 # not ubuntu-latest\n", nil},
		"alias under another key": {"      key: cache-ubuntu-latest\n", nil},
		"empty document":          {"", nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := floatingRunnerLabels("w.yml", []byte(tc.document))
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("floatingRunnerLabels = %q, want %q", got, tc.want)
			}
		})
	}
}
