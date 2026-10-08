// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// rulesetNote returns the note of the ruleset setting in a flavor apply report.
func rulesetNote(t *testing.T, report *flavor.ApplyReport) string {
	t.Helper()
	for _, setting := range report.Settings {
		if setting.Path == ".github/rulesets/main.json" {
			return setting.Note
		}
	}
	t.Fatalf("no ruleset setting in %+v", report.Settings)
	return ""
}

// Positive: with a declared merge queue, apply names each workflow the queue ruleset leaves out
// because it lacks merge_group. Negative: without a queue the note names none.
func TestApplyFlavor_MergeQueueNamesOmittedWorkflows(t *testing.T) {
	files := map[string]string{
		"go.mod": "module example.com/svc\n", "cmd/svc/main.go": "package main\n",
		".github/workflows/deploy.yml": "on: pull_request\njobs:\n  deploy:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n",
	}
	queued := map[string]string{".standards.yaml": "overrides:\n  branch_protection:\n    merge_queue: true\n"}
	for name, value := range files {
		queued[name] = value
	}
	report, err := flavor.ApplyFlavor(t.Context(), repoWithFiles(t, queued), "go-service", false)
	if err != nil {
		t.Fatal(err)
	}
	note := rulesetNote(t, report)
	if !strings.Contains(note, "not required by the merge queue ruleset") || !strings.Contains(note, "merge_group:") {
		t.Fatalf("note = %q, want the omitted workflows named", note)
	}
	report, err = flavor.ApplyFlavor(t.Context(), repoWithFiles(t, files), "go-service", false)
	if err != nil {
		t.Fatal(err)
	}
	if note := rulesetNote(t, report); strings.Contains(note, "merge queue") {
		t.Fatalf("note = %q, want no queue omission without a queue", note)
	}
}
