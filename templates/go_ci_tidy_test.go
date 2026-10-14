// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package templates

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const goCIRendering = "testdata/rendered/go/ci-go.yml/default.yml"

// goCITidyCommand returns the argv of the rendered Go CI job's tidiness step and fails unless
// that step runs before Vet.
func goCITidyCommand(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(goCIRendering)
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	steps := workflow.Jobs["test"].Steps
	names := make([]string, len(steps))
	for index := range steps {
		names[index] = steps[index].Name
	}
	tidy, vet := slices.Index(names, "Module metadata is tidy"), slices.Index(names, "Vet")
	if tidy < 0 || vet < 0 || tidy > vet {
		t.Fatalf("the Go CI job runs no tidiness step before Vet: %q", names)
	}
	return strings.Fields(steps[tidy].Run)
}

// goModule writes a module that imports nothing beyond the standard library; with unused it also
// requires a local module through a replace directive, so the check needs no network.
func goModule(t *testing.T, unused bool) string {
	t.Helper()
	root := t.TempDir()
	goMod := "module example.test/widgets\n\ngo 1.23\n"
	if unused {
		goMod += "\nrequire example.test/unused v0.0.0\n\nreplace example.test/unused => ./unused\n"
		writeModuleFile(t, root, "unused/go.mod", "module example.test/unused\n\ngo 1.23\n")
		writeModuleFile(t, root, "unused/unused.go", "package unused\n")
	}
	writeModuleFile(t, root, "go.mod", goMod)
	writeModuleFile(t, root, "widgets.go", "package widgets\n\nfunc Count() int { return 1 }\n")
	return root
}

func writeModuleFile(t *testing.T, root, rel, text string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runTidyStep(t *testing.T, argv []string, dir string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOPROXY=off", "GOWORK=off", "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// #1214. Positive: the rendered step passes a tidy module. Negative: it fails a module that
// requires what no package imports, naming the requirement in its diff. Boundary: it changes no
// file of the module it fails.
func TestGoCITidyStepFailsAnUntidyModule(t *testing.T) {
	argv := goCITidyCommand(t)
	if _, err := exec.LookPath(argv[0]); err != nil {
		t.Skipf("%s not on PATH", argv[0])
	}
	if out, err := runTidyStep(t, argv, goModule(t, false)); err != nil {
		t.Fatalf("tidy module refused: %v\n%s", err, out)
	}
	untidy := goModule(t, true)
	before, err := os.ReadFile(filepath.Join(untidy, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runTidyStep(t, argv, untidy)
	if err == nil || !strings.Contains(out, "-require example.test/unused v0.0.0") {
		t.Fatalf("untidy module passed or the diff does not name the requirement: %v\n%s", err, out)
	}
	if after, _ := os.ReadFile(filepath.Join(untidy, "go.mod")); string(after) != string(before) {
		t.Fatalf("the step rewrote go.mod:\n%s", after)
	}
}
