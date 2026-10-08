// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

// skipGateJob is the gate job id of the managed API gate.
const skipGateJob = "api-compatibility"

// skipRendering returns the managed API gate rendered in the draft skip shape.
func skipRendering(t *testing.T) string {
	t.Helper()
	rendered, gate, err := ghworkflow.RenderDraftSkip(apiassets.Workflow)
	if err != nil || gate != skipGateJob {
		t.Fatalf("render the draft skip: gate %q, %v", gate, err)
	}
	return rendered
}

// replaceOnce replaces old with replacement in text once, failing when old is absent so a case
// never judges the unchanged text.
func replaceOnce(t *testing.T, text, old, replacement string) string {
	t.Helper()
	if strings.Count(text, old) != 1 {
		t.Fatalf("%q does not occur exactly once", old)
	}
	return strings.Replace(text, old, replacement, 1)
}

// Positive (#857): the managed gates rendered in the draft skip shape pass the guard, because
// the result job reporting the required context is a proven aggregate that fails on the skip.
func TestDraftSkipFaultAcceptsTheRenderedShape(t *testing.T) {
	if err := DraftSkipFault([]byte(skipRendering(t)), skipGateJob, apiassets.StatusContext); err != nil {
		t.Fatalf("the rendered skip shape is refused: %v", err)
	}
}

// Negative (#857): each missing condition is refused and named: no aggregate (the gate job itself
// reports the context), no job reporting it, an aggregate that passes while the gate job is
// skipped, an aggregate the proof cannot read, and a gate job without the exact condition.
func TestDraftSkipFaultRefusesWhatLetsADraftPass(t *testing.T) {
	rendered := skipRendering(t)
	resultJob := rendered[strings.Index(rendered, "  "+skipGateJob+"-result:"):]
	cases := map[string]struct{ text, want string }{
		"no aggregate": {
			text: replaceOnce(t, strings.TrimSuffix(rendered, resultJob), "name: Go API Compatibility gate", "name: Go API Compatibility"),
			want: "is the gate job api-compatibility itself, whose draft skip reports success",
		},
		"no reporting job": {
			text: replaceOnce(t, rendered, "name: Go API Compatibility\n    needs", "name: Another Check\n    needs"),
			want: `no job of the workflow reports the required check "Go API Compatibility"`,
		},
		"aggregate passes while skipped": {
			text: replaceOnce(t, rendered, "if: needs.api-compatibility.result != 'success'",
				"if: needs.api-compatibility.result == 'failure' || needs.api-compatibility.result == 'cancelled'"),
			want: "passes while api-compatibility is skipped",
		},
		"aggregate only echoes": {
			text: replaceOnce(t, rendered, "          exit 1\n", ""),
			want: "is not a proven aggregate covering api-compatibility",
		},
		"aggregate not under always": {
			text: replaceOnce(t, rendered, "    if: always()\n", ""),
			want: "is not a proven aggregate covering api-compatibility",
		},
		"gate job without the skip condition": {
			text: replaceOnce(t, rendered, "      || github.event.pull_request.draft == false\n", "      || github.event.pull_request.draft != true\n"),
			want: "does not carry the draft skip condition",
		},
	}
	for name, tc := range cases {
		err := DraftSkipFault([]byte(tc.text), skipGateJob, apiassets.StatusContext)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want %q", name, err, tc.want)
		}
	}
	if err := DraftSkipFault([]byte(rendered), "missing", apiassets.StatusContext); err == nil {
		t.Fatal("a gate job the workflow does not hold was accepted")
	}
	if err := DraftSkipFault([]byte("jobs: ["), skipGateJob, apiassets.StatusContext); err == nil {
		t.Fatal("an unparseable workflow was accepted")
	}
}

// Boundary (#857): the trigger audit accepts the rendered skip shape and nothing weaker. The
// same job condition without an aggregate that fails on the skip is reported as the draft skip
// it is, and so is a workflow whose aggregate passes while the gate job is skipped.
func TestAuditWorkflowTriggersAcceptsTheDraftSkipOnlyBehindItsAggregate(t *testing.T) {
	rendered := skipRendering(t)
	passes := "[PASS] Workflow triggers (HISS-18): workflows read: 1; no push trigger runs on every branch and no job a " +
		"pull request starts runs its work on a draft."
	if out := auditTriggers(t, triggerRepository(t, map[string]string{"api.yml": rendered})); out != passes {
		t.Fatalf("the rendered skip shape:\n%s\nwant:\n%s", out, passes)
	}
	bare := replaceOnce(t, rendered, "    if: always()\n", "")
	passing := replaceOnce(t, rendered, "if: needs.api-compatibility.result != 'success'",
		"if: needs.api-compatibility.result == 'failure' || needs.api-compatibility.result == 'cancelled'")
	for name, text := range map[string]string{"aggregate not under always": bare, "aggregate passing on a skip": passing} {
		out := auditTriggers(t, triggerRepository(t, map[string]string{"api.yml": text}))
		if !strings.Contains(out, "skips a draft with the job condition") {
			t.Fatalf("%s was not reported:\n%s", name, out)
		}
	}
}
