// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// gateJob is a hosted gate's job header at the indentation the shape's step texts assume.
const gateJob = "jobs:\n  gate:\n    name: Gate\n    runs-on: ubuntu-latest\n    steps:\n"

// gateStep is one gate step carrying the not-a-draft condition.
const gateStep = "      - name: Run the gate" + HostedGateStepIf + "\n        run: make gate\n"

// hostedGateFixture is a one-job workflow in the hosted gate shape for main.
const hostedGateFixture = HostedGateOn + gateJob + HostedGateDraftStep + gateStep

// hostedGateFault parses doc and judges its job gate against the shape for branch.
func hostedGateFault(t *testing.T, doc, branch string) error {
	t.Helper()
	spec, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse %q: %v", doc, err)
	}
	return HostedGateFault(&spec, "gate", branch)
}

// mutate replaces old with replacement in hostedGateFixture once, failing when old is absent so
// a case never tests the unchanged text.
func mutate(t *testing.T, old, replacement string) string {
	t.Helper()
	if !strings.Contains(hostedGateFixture, old) {
		t.Fatalf("the fixture lacks %q", old)
	}
	return strings.Replace(hostedGateFixture, old, replacement, 1)
}

// Positive: the shape the gates render passes, with a gate step condition bare or as one
// expression, and rendered for another default branch it passes for that branch. Boundary: one
// gate step after the draft step is enough.
func TestHostedGateFault_Positive(t *testing.T) {
	wrapped := mutate(t, HostedGateStepIf, "\n        if: ${{ "+HostedGateNotDraft+" }}")
	develop := mutate(t, HostedGatePushBranchesPrefix+HostedGateDefaultBranch, HostedGatePushBranchesPrefix+"develop")
	for name, test := range map[string]struct{ doc, branch string }{
		"rendered, one gate step": {hostedGateFixture, HostedGateDefaultBranch},
		"wrapped condition":       {wrapped, HostedGateDefaultBranch},
		"develop rendering":       {develop, "develop"},
		"two gate steps":          {hostedGateFixture + gateStep, HostedGateDefaultBranch},
	} {
		if err := hostedGateFault(t, test.doc, test.branch); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Negative: every departure from the shape is refused, a job-level draft skip first among them,
// since GitHub reports the skipped job as successful and a required check accepts it.
func TestHostedGateFault_Negative(t *testing.T) {
	jobLine := "    name: Gate\n"
	cases := map[string]struct{ doc, fault string }{
		"job draft skip":        {mutate(t, jobLine, jobLine+"    if: "+HostedGateNotDraft+"\n"), "carries the condition"},
		"advisory job":          {mutate(t, jobLine, jobLine+"    continue-on-error: true\n"), "continues on error"},
		"no ready_for_review":   {mutate(t, ", ready_for_review]", "]"), "pull_request trigger"},
		"default types":         {mutate(t, "\n    types: [opened, synchronize, reopened, ready_for_review]", ""), "pull_request trigger"},
		"path filter":           {mutate(t, "  push:", "    paths: ['**']\n  push:"), "pull_request trigger"},
		"every branch":          {mutate(t, HostedGatePushBranchesPrefix+"main']", ""), "push trigger"},
		"other branch":          {mutate(t, "['main']", "['develop']"), "push trigger"},
		"two branches":          {mutate(t, "['main']", "['main', 'release']"), "push trigger"},
		"tags too":              {mutate(t, "['main']\n", "['main']\n    tags: ['v*']\n"), "push trigger"},
		"extra trigger":         {mutate(t, "  push:", "  workflow_dispatch:\n  push:"), "not exactly pull_request and push"},
		"draft step missing":    {mutate(t, HostedGateDraftStep, "") + gateStep, "first step does not run on a draft"},
		"draft step reworded":   {mutate(t, "exit 1\n", "exit 0\n"), "draft step's script"},
		"draft step tolerated":  {mutate(t, "        run: |\n", "        continue-on-error: true\n        run: |\n"), "draft step's script"},
		"draft marker edited":   {mutate(t, "TITLE: "+HostedGateDraftTitle, "TITLE: Draft"), "title and message"},
		"draft marker extended": {mutate(t, "        env:\n", "        env:\n          EXTRA: x\n"), "title and message"},
		"gate step unguarded":   {mutate(t, HostedGateStepIf, ""), "step 2 (Run the gate)"},
		"gate step joined":      {mutate(t, HostedGateStepIf, HostedGateStepIf+" && github.actor != 'bot'"), "step 2"},
		"gate step unclosed":    {mutate(t, HostedGateStepIf, "\n        if: '${{ "+HostedGateNotDraft+"'"), "step 2"},
		"gate step tolerated":   {mutate(t, "        run: make gate\n", "        continue-on-error: true\n        run: make gate\n"), "step 2"},
		"draft step only":       {mutate(t, gateStep, ""), "at least one gate step"},
	}
	for name, test := range cases {
		err := hostedGateFault(t, test.doc, HostedGateDefaultBranch)
		if err == nil || !strings.Contains(err.Error(), test.fault) {
			t.Errorf("%s: fault = %v, want one naming %q", name, err, test.fault)
		}
	}
	spec, err := Parse([]byte(hostedGateFixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := HostedGateFault(&spec, "other", HostedGateDefaultBranch); err == nil || !strings.Contains(err.Error(), "no job") {
		t.Errorf("an absent job: %v", err)
	}
}

// Boundary: the rendered draft step folds its message into HostedGateDraftMessage, the text the
// checkpoint planner matches, and the planner carries the same title and message
// (.config/lefthook/scripts/checkpoint.py); neither carries a character a workflow command would
// need escaped.
func TestHostedGateDraftMarker_Boundary(t *testing.T) {
	spec, err := Parse([]byte(hostedGateFixture))
	if err != nil {
		t.Fatal(err)
	}
	step := spec.Jobs["gate"].Steps[0]
	if got := scalarValue(&step.Env, "MESSAGE"); got != HostedGateDraftMessage {
		t.Fatalf("folded message = %q, want %q", got, HostedGateDraftMessage)
	}
	if strings.ContainsAny(HostedGateDraftTitle, ":,%\r\n") || strings.ContainsAny(HostedGateDraftMessage, "%\r\n") {
		t.Fatal("the draft marker carries a character a workflow command escapes")
	}
	planner, err := os.ReadFile(filepath.Join("..", "..", ".config", "lefthook", "scripts", "checkpoint.py"))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout under core.autocrlf holds the script in CRLF.
	text, _ := util.NormalizeLineEndings(string(planner))
	for _, lines := range []string{
		"DRAFT_GATE_TITLE = " + strconv.Quote(HostedGateDraftTitle),
		"DRAFT_GATE_MESSAGE = (\n    " + strconv.Quote(hostedGateDraftReason) + "\n    " +
			strconv.Quote(" "+hostedGateDraftReady) + "\n)",
	} {
		if !strings.Contains(text, "\n"+lines+"\n") {
			t.Errorf("checkpoint.py lacks the lines\n%s", lines)
		}
	}
}

// UnwrapExpression. Positive: a bare condition and one ${{ }} expression read alike. Negative: an
// unclosed wrapper is not closed. Boundary: surrounding space, an empty condition and a wrapper
// that does not span the whole condition.
func TestUnwrapExpression(t *testing.T) {
	cases := []struct {
		condition, want string
		closed          bool
	}{
		{"github.ref == 'x'", "github.ref == 'x'", true},
		{"${{ github.ref == 'x' }}", "github.ref == 'x'", true},
		{"${{ github.ref", "${{ github.ref", false},
		{"  ${{false}}  ", "false", true},
		{"", "", true},
		{"a && ${{ b }}", "a && ${{ b }}", true},
	}
	for _, tc := range cases {
		got, closed := UnwrapExpression(tc.condition)
		if got != tc.want || closed != tc.closed {
			t.Errorf("UnwrapExpression(%q) = %q, %v; want %q, %v", tc.condition, got, closed, tc.want, tc.closed)
		}
	}
}
