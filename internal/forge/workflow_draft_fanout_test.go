// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// draftFanoutWorkflow models path-filtered CI with three components behind a skipped planner.
const draftFanoutWorkflow = readyTypes + `  changes:
    if: ` + ghworkflow.HostedGateSkipCondition + `
    runs-on: ubuntu-latest
    outputs:
      go: ${{ steps.filter.outputs.go }}
    steps:
      - id: filter
        run: make detect-changed-components
  build:
    needs: changes
    if: needs.changes.outputs.go == 'true'
    runs-on: ubuntu-latest
    steps:
      - run: make build
  lint:
    needs: changes
    runs-on: ubuntu-latest
    steps:
      - run: make lint
  test:
    needs: changes
    runs-on: ubuntu-latest
    steps:
      - run: make test
  result:
    needs: [changes, build, lint, test]
    if: always()
    runs-on: ubuntu-latest
    steps:
      - if: contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled') || needs.changes.result == 'skipped'
        run: exit 1
`

// Positive, negative and boundary fixtures replay the same fan-out in both directions (#1108).
func TestAuditWorkflowTriggers_DraftSkipFanout(t *testing.T) {
	jobs := []string{"changes", "build", "lint", "test", "result"}
	cases := []struct {
		name, workflow string
		want           []string
	}{
		{"positive covered fanout", draftFanoutWorkflow, nil},
		{"negative missing dependent", replaceOnce(t, draftFanoutWorkflow,
			"needs: [changes, build, lint, test]", "needs: [changes, build, lint]"), []string{"test"}},
		{"negative uncovered dependent failure", replaceOnce(t, draftFanoutWorkflow,
			"contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled')",
			"needs.changes.result != 'success' || needs.build.result != 'success' || needs.lint.result != 'success'"), []string{"test"}},
		{"negative passes on skip", replaceOnce(t, draftFanoutWorkflow,
			" || needs.changes.result == 'skipped'", ""), jobs},
		{"boundary dependent reads draft", replaceOnce(t, draftFanoutWorkflow,
			"if: needs.changes.outputs.go == 'true'", "if: "+ghworkflow.HostedGateNotDraft), []string{"build"}},
		{"negative conditional aggregate", replaceOnce(t, draftFanoutWorkflow,
			"if: always()", "if: always() && needs.changes.result == 'success'"), jobs},
		{"boundary unrelated need", replaceOnce(t, draftFanoutWorkflow,
			"  lint:\n    needs: changes", "  lint:\n"), []string{"lint"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := auditTriggers(t, triggerRepository(t, map[string]string{"fanout.yml": tc.workflow}))
			if got := strings.Count(out, "pull_request job "); got != len(tc.want) {
				t.Fatalf("findings = %d, want %d:\n%s", got, len(tc.want), out)
			}
			if len(tc.want) == 0 && !strings.HasPrefix(out, "[PASS]") {
				t.Fatalf("covered fanout did not pass:\n%s", out)
			}
			for _, id := range jobs {
				if got := strings.Contains(out, "pull_request job "+id+" "); got != slices.Contains(tc.want, id) {
					t.Errorf("job %s reported = %t, want %t:\n%s", id, got, !got, out)
				}
			}
		})
	}
}
