package lefthookconfig

import (
	"fmt"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func parse(t *testing.T, body string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed
}

func paths(jobs []Job) []string {
	names := make([]string, 0, len(jobs))
	for _, job := range jobs {
		names = append(names, job.Path())
	}
	return names
}

// Positive: every job of the commands, scripts and jobs-list syntaxes is named hook/kind/name,
// ordered by hook and then by declaration, and carries its run line.
func TestJobs_Positive_EverySyntaxNamedWithItsRunLine(t *testing.T) {
	parsed := parse(t, "pre-push:\n  commands:\n    gate:\n      run: praetorctl gate run\n"+
		"pre-commit:\n  commands:\n    vet:\n      run: go vet ./...\n    fmt:\n      run: gofmt -l .\n"+
		"  scripts:\n    check.sh:\n      runner: bash\n"+
		"  jobs:\n    - name: lint\n      run: make lint\n    - script: other.sh\n")
	want := []Job{
		{Hook: "pre-commit", Name: "commands/fmt", Run: "gofmt -l ."},
		{Hook: "pre-commit", Name: "commands/vet", Run: "go vet ./..."},
		{Hook: "pre-commit", Name: "scripts/check.sh"},
		{Hook: "pre-commit", Name: "commands/lint", Run: "make lint"},
		{Hook: "pre-commit", Name: "scripts/other.sh"},
		{Hook: "pre-push", Name: "commands/gate", Run: "praetorctl gate run"},
	}
	if got := Jobs(parsed); !reflect.DeepEqual(got, want) {
		t.Fatalf("Jobs =\n%+v\nwant\n%+v", got, want)
	}
	if got := HookRuns(parsed, "pre-commit"); !reflect.DeepEqual(got, []string{"gofmt -l .", "go vet ./...", "make lint"}) {
		t.Fatalf("HookRuns(pre-commit) = %q", got)
	}
}

// Negative: keys that hold no job map (min_version, extends, a scalar hook) contribute nothing,
// a run that is not a string is no run line, and a hook nobody declared has no runs.
func TestJobs_Negative_NonJobKeysAndNonStringRunsIgnored(t *testing.T) {
	parsed := parse(t, "min_version: 2.1.14\nextends:\n  - other.yml\npre-commit: nothing\n"+
		"commit-msg:\n  commands:\n    check:\n      run: [not, a, string]\n")
	if got := paths(Jobs(parsed)); !reflect.DeepEqual(got, []string{"commit-msg/commands/check"}) {
		t.Fatalf("Jobs = %q", got)
	}
	if runs := HookRuns(parsed, "commit-msg"); len(runs) != 0 {
		t.Fatalf("a non-string run was read as a run line: %q", runs)
	}
	if runs := HookRuns(parsed, "pre-push"); len(runs) != 0 || len(Jobs(nil)) != 0 {
		t.Fatalf("an absent hook or configuration named jobs: %q", runs)
	}
}

// Boundary: jobs-list entries are named like lefthook names them (script, name, run line, then
// position; a group is its own kind and its jobs are not read), and the walk stops at MaxJobs.
func TestJobs_Boundary_ListNamesGroupsAndBound(t *testing.T) {
	cases := []struct {
		job      map[string]any
		name     string
		hasRunIn bool
	}{
		{map[string]any{"name": "fmt", "script": "fmt.sh", "run": "x"}, "scripts/fmt.sh", false},
		{map[string]any{"name": "fmt", "run": "gofmt -l ."}, "commands/fmt", true},
		{map[string]any{"run": "gofmt -l ."}, "commands/gofmt -l .", true},
		{map[string]any{"name": "checks", "run": "x", "group": map[string]any{"jobs": []any{}}}, "jobs/checks", false},
		{map[string]any{}, "jobs/[3]", false},
		{nil, "jobs/[3]", false},
	}
	for _, tc := range cases {
		if got := listJobName(tc.job, 3); got != tc.name {
			t.Errorf("listJobName(%v) = %q, want %q", tc.job, got, tc.name)
		}
		if got := listJobRun(tc.job) != ""; got != tc.hasRunIn {
			t.Errorf("listJobRun(%v) read a run line: %v, want %v", tc.job, got, tc.hasRunIn)
		}
	}
	commands := make(map[string]any, MaxJobs+10)
	for i := 0; i < MaxJobs+10; i++ {
		commands[fmt.Sprintf("job-%04d", i)] = map[string]any{"run": "true"}
	}
	jobs := Jobs(map[string]any{"pre-commit": map[string]any{"commands": commands, "jobs": []any{map[string]any{"run": "late"}}}})
	if len(jobs) != MaxJobs || jobs[MaxJobs-1].Name != fmt.Sprintf("commands/job-%04d", MaxJobs-1) {
		t.Fatalf("the walk read %d jobs, last %+v; want the first %d by name", len(jobs), jobs[len(jobs)-1], MaxJobs)
	}
}
