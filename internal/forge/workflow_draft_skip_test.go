// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// readyTypes is a pull_request types list that runs a workflow again when a draft is marked ready.
const readyTypes = "    types: [opened, synchronize, reopened, ready_for_review]\n"

// draftSkipWorkflow is a one-job pull request workflow whose trigger carries types (empty for
// none) and whose job carries condition (empty for none).
func draftSkipWorkflow(types, condition string) []byte {
	job := "    name: Gate\n"
	if condition != "" {
		job += "    if: " + condition + "\n"
	}
	return []byte("on:\n  pull_request:\n" + types + "  push:\n    branches: ['main']\njobs:\n  gate:\n" + job +
		"    runs-on: ubuntu-latest\n    steps:\n      - run: make\n")
}

// Positive: a draft skip keeps the job required where the workflow runs again on
// ready_for_review, bare or as one expression. Negative: without ready_for_review, joined to
// another term or reworded, the skip makes the job conditional. Boundary: a scalar
// ready_for_review type counts, and an unconditional job stays required whatever its types.
func TestDraftSkipKeepsAJobRequiredOnlyWhenReadyReruns(t *testing.T) {
	cases := []struct {
		name, types, condition string
		required               bool
	}{
		{"positive bare skip", readyTypes, draftSkip, true},
		{"positive expression skip", readyTypes, "${{ " + draftSkip + " }}", true},
		{"negative default types", "", draftSkip, false},
		{"negative types without ready_for_review", "    types: [opened, synchronize, reopened]\n", draftSkip, false},
		{"negative joined skip", readyTypes, draftSkip + " && github.actor != 'bot'", false},
		{"negative reworded skip", readyTypes, "github.event.pull_request.draft == false", false},
		{"negative negated skip", readyTypes, "\"!(" + draftSkip + ")\"", false},
		{"negative unclosed expression", readyTypes, "'${{ " + draftSkip + "'", false},
		{"boundary scalar type", "    types: ready_for_review\n", draftSkip, true},
		{"boundary unconditional job", readyTypes, "", true},
		{"boundary unconditional job without types", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contexts, err := workflowContextsIn(draftSkipWorkflow(tc.types, tc.condition), "")
			if err != nil {
				t.Fatalf("workflowContextsIn: %v", err)
			}
			if got := slices.Equal(contexts, []string{"Gate"}); got != tc.required {
				t.Fatalf("required contexts = %v, want Gate required = %v", contexts, tc.required)
			}
		})
	}
}

// Positive: the exact skip is removed, bare or wrapped, when ready reruns. Negative: it stays
// when ready does not rerun, and any other condition is returned unchanged. Boundary: an empty
// condition and surrounding space.
func TestWithoutDraftSkip(t *testing.T) {
	cases := []struct {
		name, condition string
		rerunsOnReady   bool
		want            string
	}{
		{"positive bare", draftSkip, true, ""},
		{"positive wrapped", "${{ " + draftSkip + " }}", true, ""},
		{"negative no ready rerun", draftSkip, false, draftSkip},
		{"negative other condition", "github.actor != 'bot'", true, "github.actor != 'bot'"},
		{"boundary surrounding space", "  " + draftSkip + "  ", true, ""},
		{"boundary empty", "", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withoutDraftSkip(tc.condition, tc.rerunsOnReady); got != tc.want {
				t.Fatalf("withoutDraftSkip(%q, %v) = %q, want %q", tc.condition, tc.rerunsOnReady, got, tc.want)
			}
		})
	}
}

// Positive: a ready_for_review entry or scalar reruns. Negative: no pull_request trigger, a
// bare trigger, types without the entry, or only pull_request_target listing it. Boundary: the
// sequence is read in full, the entry last.
func TestRerunsWhenReady(t *testing.T) {
	cases := []struct {
		name, on string
		want     bool
	}{
		{"positive sequence", "pull_request:\n  types: [ready_for_review]\n", true},
		{"positive scalar", "pull_request:\n  types: ready_for_review\n", true},
		{"negative push only", "push:\n  branches: [main]\n", false},
		{"negative bare trigger", "pull_request:\n", false},
		{"negative scalar event", "pull_request\n", false},
		{"negative other types", "pull_request:\n  types: [opened, edited]\n", false},
		{"negative target only", "pull_request_target:\n  types: [ready_for_review]\npull_request:\n", false},
		{"boundary entry last", "pull_request:\n  branches: [main]\n  types: [opened, synchronize, reopened, ready_for_review]\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var spec workflowSpec
			lines := strings.SplitAfter(strings.TrimSuffix(tc.on, "\n"), "\n")
			if err := yaml.Unmarshal([]byte("on:\n  "+strings.Join(lines, "  ")+"\njobs: {}\n"), &spec); err != nil {
				t.Fatal(err)
			}
			if got := rerunsWhenReady(&spec.On); got != tc.want {
				t.Fatalf("rerunsWhenReady(%q) = %v, want %v", tc.on, got, tc.want)
			}
		})
	}
}
