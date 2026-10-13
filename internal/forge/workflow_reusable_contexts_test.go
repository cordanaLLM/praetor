package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The caller and called workflow of plurano/shared-ci pull request 5 (#1212).
const (
	licenceCaller = "on:\n  pull_request:\njobs:\n  licence:\n    name: Licence\n    permissions:\n      contents: read\n" +
		"    uses: ./.github/workflows/licence.yml\n"
	licenceCalled = "on:\n  workflow_call:\njobs:\n  lint:\n    name: REUSE lint\n    runs-on: ubuntu-latest\n" +
		"    steps:\n      - run: reuse lint\n"
)

// reusableContexts selects the required contexts of files as the ruleset does, with every
// file available to a call.
func reusableContexts(t *testing.T, files map[string]string) ([]string, error) {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	list := make([]workflowFile, 0, len(names))
	for _, name := range names {
		list = append(list, workflowFile{Name: name, Data: []byte(files[name])})
	}
	return selectContexts(list, newCalledWorkflows(list), "", contextOptions{})
}

// Positive (#1212): a job that calls a local reusable workflow requires "<caller> / <called
// job>", never the caller's own name; a caller without a name reports under its id; a called
// workflow that also runs on pull requests keeps its own context beside the composed one.
func TestReusableCallContexts_LocalCall(t *testing.T) {
	got, err := reusableContexts(t, map[string]string{"licence-check.yml": licenceCaller, "licence.yml": licenceCalled})
	if err != nil {
		t.Fatalf("contexts: %v", err)
	}
	if !slices.Equal(got, []string{"Licence / REUSE lint"}) {
		t.Fatalf("contexts = %q, want [Licence / REUSE lint]", got)
	}

	unnamed := strings.Replace(licenceCaller, "    name: Licence\n", "", 1)
	both := strings.Replace(licenceCalled, "on:\n  workflow_call:\n", "on:\n  workflow_call:\n  pull_request:\n", 1)
	got, err = reusableContexts(t, map[string]string{"licence-check.yml": unnamed, "licence.yml": both})
	if err != nil {
		t.Fatalf("contexts: %v", err)
	}
	if !slices.Equal(got, []string{"licence / REUSE lint", "REUSE lint"}) {
		t.Fatalf("contexts = %q, want [licence / REUSE lint, REUSE lint]", got)
	}
}

// Negative (#1212): a call to a workflow of another repository is refused, naming the job and
// the reference; so are a local call to a file that is not there, one into a subdirectory and
// one to a workflow without a workflow_call trigger. None renders the caller's own name.
func TestReusableCallContexts_RefusesWhatItCannotRead(t *testing.T) {
	remote := strings.Replace(licenceCaller, "./.github/workflows/licence.yml", "plurano/shared-ci/.github/workflows/licence.yml@v1", 1)
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"remote": {map[string]string{"licence-check.yml": remote},
			`job "licence" calls the reusable workflow plurano/shared-ci/.github/workflows/licence.yml@v1 of another repository`},
		"missing": {map[string]string{"licence-check.yml": licenceCaller},
			`job "licence" calls ./.github/workflows/licence.yml, which is no workflow in .github/workflows/`},
		"not reusable": {map[string]string{"licence-check.yml": licenceCaller,
			"licence.yml": strings.Replace(licenceCalled, "workflow_call", "push", 1)},
			"declares no workflow_call trigger"},
		"subdirectory": {map[string]string{"licence-check.yml": strings.Replace(licenceCaller, "licence.yml", "sub/licence.yml", 1),
			"licence.yml": licenceCalled}, "which is no workflow in"},
	}
	for name, tc := range cases {
		got, err := reusableContexts(t, tc.files)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: contexts %q, err %v; want an error containing %q", name, got, err, tc.want)
		}
	}
}

// Boundary (#1212): a matrix job in the called workflow reports one composed context per leg,
// and a matrix caller one per caller leg; a called job whose condition skips it on some runs is
// no required check, as in a workflow a trigger starts; a caller whose condition does so is not
// expanded at all.
func TestReusableCallContexts_MatrixAndConditions(t *testing.T) {
	called := "on:\n  workflow_call:\njobs:\n  test:\n    name: Test (${{ matrix.os }})\n    strategy:\n      matrix:\n" +
		"        os: [linux, windows]\n    runs-on: ubuntu-latest\n    steps:\n      - run: make test\n" +
		"  optional:\n    if: inputs.extra\n    runs-on: ubuntu-latest\n    steps:\n      - run: make extra\n"
	caller := "on:\n  pull_request:\njobs:\n  ci:\n    name: CI\n    uses: ./.github/workflows/test.yml\n"
	got, err := reusableContexts(t, map[string]string{"ci.yml": caller, "test.yml": called})
	if err != nil {
		t.Fatalf("contexts: %v", err)
	}
	if !slices.Equal(got, []string{"CI / Test (linux)", "CI / Test (windows)"}) {
		t.Fatalf("contexts = %q", got)
	}

	matrixCaller := "on:\n  pull_request:\njobs:\n  ci:\n    name: CI ${{ matrix.go }}\n    strategy:\n      matrix:\n" +
		"        go: ['1.26', '1.27']\n    uses: ./.github/workflows/test.yml\n"
	got, err = reusableContexts(t, map[string]string{"ci.yml": matrixCaller, "test.yml": called})
	if err != nil {
		t.Fatalf("contexts: %v", err)
	}
	want := []string{"CI 1.26 / Test (linux)", "CI 1.26 / Test (windows)", "CI 1.27 / Test (linux)", "CI 1.27 / Test (windows)"}
	if !slices.Equal(got, want) {
		t.Fatalf("contexts = %q, want %q", got, want)
	}

	skipped := strings.Replace(caller, "    name: CI\n", "    name: CI\n    if: inputs.extra\n", 1)
	got, err = reusableContexts(t, map[string]string{"ci.yml": skipped, "test.yml": called})
	if err != nil || len(got) != 0 {
		t.Fatalf("a conditional caller: contexts %q, err %v; want none", got, err)
	}
}

// nestedChain is a chain of levels workflows: w1 is started by pull_request, each wN calls
// w(N+1), and the last one runs a job of its own.
func nestedChain(levels int) map[string]string {
	files := map[string]string{}
	for level := 1; level <= levels; level++ {
		trigger := "on:\n  workflow_call:\n"
		if level == 1 {
			trigger = "on:\n  pull_request:\n"
		}
		job := fmt.Sprintf("    uses: ./.github/workflows/w%d.yml\n", level+1)
		if level == levels {
			job = "    runs-on: ubuntu-latest\n    steps:\n      - run: make check\n"
		}
		files[fmt.Sprintf("w%d.yml", level)] = trigger + fmt.Sprintf("jobs:\n  j%d:\n    name: L%d\n", level, level) + job
	}
	return files
}

// Boundary (#1212): nested calls are followed through every level GitHub runs, ten, each level
// joining its job's name; one level more is refused, naming the call.
func TestReusableCallContexts_NestingBound(t *testing.T) {
	got, err := reusableContexts(t, nestedChain(maxReusableLevels))
	if err != nil {
		t.Fatalf("ten levels: %v", err)
	}
	if want := "L1 / L2 / L3 / L4 / L5 / L6 / L7 / L8 / L9 / L10"; !slices.Equal(got, []string{want}) {
		t.Fatalf("ten levels: contexts %q, want [%s]", got, want)
	}
	_, err = reusableContexts(t, nestedChain(maxReusableLevels+1))
	want := `job "j10" calls ./.github/workflows/w11.yml at level 11, deeper than the 10 levels GitHub runs`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("eleven levels: err %v, want %q", err, want)
	}
}

// Boundary (#1212): selecting only the calling workflow still reads the one it calls, and a
// planned removal of the called workflow is read like the tree.
func TestRequiredStatusContextsOf_ReadsUnselectedCalledWorkflow(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"licence-check.yml": licenceCaller, "licence.yml": licenceCalled} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := RequiredStatusContextsOf(t.Context(), root, []string{".github/workflows/licence-check.yml"})
	if err != nil || !slices.Equal(got, []string{"Licence / REUSE lint"}) {
		t.Fatalf("selected caller: contexts %q, err %v", got, err)
	}
	got, err = RequiredStatusContextsPlanned(t.Context(), root, map[string][]byte{".github/workflows/licence.yml": nil})
	if err == nil || !strings.Contains(err.Error(), "which is no workflow in") {
		t.Fatalf("planned removal of the called workflow: contexts %q, err %v", got, err)
	}
}
