// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"strings"
	"testing"
)

// Positive: the skip shape of a fail-closed gate drops the draft step and every step condition,
// puts the folded draft skip on the gate job, renames the gate job so the original context moves
// to a result job that needs it under always(), and parses as the workflow it claims to be.
func TestRenderDraftSkipPositive(t *testing.T) {
	rendered, gateID, err := RenderDraftSkip(hostedGateFixture)
	if err != nil {
		t.Fatal(err)
	}
	if gateID != "gate" {
		t.Fatalf("gate job = %q, want gate", gateID)
	}
	for _, absent := range []string{HostedGateDraftStepName, HostedGateStepIf, HostedGateDraft} {
		if strings.Contains(rendered, absent) {
			t.Fatalf("the skip shape still holds %q:\n%s", absent, rendered)
		}
	}
	spec, err := Parse([]byte(rendered))
	if err != nil {
		t.Fatalf("the skip shape does not parse: %v\n%s", err, rendered)
	}
	gate, result := DraftSkipJobs(gateID)
	if got := spec.Jobs[gate]; got.If != HostedGateSkipCondition || got.Name != "Gate"+HostedGateSkipGateSuffix ||
		len(got.Steps) != 1 || got.Steps[0].Name != "Run the gate" {
		t.Fatalf("gate job = %+v, want the folded skip condition, the renamed job and the one gate step", got)
	}
	if got := spec.Jobs[result]; got.Name != "Gate" || got.If != "always()" || strings.Join(got.NeedIDs(), ",") != gateID ||
		len(got.Steps) != 2 || got.Steps[0].If != "needs.gate.result == 'skipped'" ||
		got.Steps[1].If != "needs.gate.result != 'success'" {
		t.Fatalf("result job = %+v, want the original context behind always() needing the gate job", got)
	}
	if again, _, err := RenderDraftSkip(hostedGateFixture); err != nil || again != rendered {
		t.Fatalf("the rendering is not deterministic: %v", err)
	}
	// The annotation line is the one line allowed past 80 columns, between yamllint comments.
	exempt := "echo \"::error title="
	for _, line := range strings.Split(rendered, "\n") {
		if len(line) > 80 && !strings.Contains(line, exempt) {
			t.Fatalf("line %q is %d columns, over yamllint's 80", line, len(line))
		}
	}
}

// Negative: a text that is not one fail-closed gate job, one with no draft step, one with an
// unconditioned later step layout it cannot read, or two jobs is refused rather than rendered
// into a shape nothing audited.
func TestRenderDraftSkipNegative(t *testing.T) {
	for name, text := range map[string]string{
		"no jobs block":     HostedGateOn,
		"no draft step":     HostedGateOn + gateJob + gateStep,
		"no step condition": HostedGateOn + gateJob + HostedGateDraftStep + "      - name: Run\n        run: make gate\n",
		"two draft steps":   HostedGateOn + gateJob + HostedGateDraftStep + HostedGateDraftStep + gateStep,
		"two jobs":          hostedGateFixture + "  other:\n    name: Other\n    runs-on: ubuntu-latest\n",
		"no runs-on":        HostedGateOn + "jobs:\n  gate:\n    name: Gate\n    steps:\n" + HostedGateDraftStep + gateStep,
	} {
		if rendered, _, err := RenderDraftSkip(text); err == nil {
			t.Fatalf("%s rendered:\n%s", name, rendered)
		}
	}
}

// Boundary: the result job's draft step prints the error annotation the checkpoint planner reads
// as draft pending, with exactly the title and message of the fail-closed draft step, so a draft
// reads as draft pending in either shape (.config/lefthook/scripts/checkpoint.py).
func TestRenderDraftSkip_Boundary_CarriesTheDraftMarker(t *testing.T) {
	rendered, _, err := RenderDraftSkip(hostedGateFixture)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := Parse([]byte(rendered))
	if err != nil {
		t.Fatal(err)
	}
	step := spec.Jobs["gate-result"].Steps[0]
	want := "echo \"::error title=" + HostedGateDraftTitle + "::" + HostedGateDraftMessage + "\"\nexit 1\n"
	if step.Run != want || step.If != "needs.gate.result == 'skipped'" {
		t.Fatalf("draft step = if %q run %q, want the annotation %q", step.If, step.Run, want)
	}
	if strings.ContainsAny(HostedGateDraftTitle+HostedGateDraftMessage, "%\r\n") {
		t.Fatal("the marker carries a character a workflow command escapes")
	}
}

// Positive: UnrenderDraftSkip returns the fail-closed text a rendering came from, byte for byte.
func TestUnrenderDraftSkipPositive(t *testing.T) {
	rendered, _, err := RenderDraftSkip(hostedGateFixture)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := UnrenderDraftSkip(rendered)
	if !ok || got != hostedGateFixture {
		t.Fatalf("unrender = %v, text equal %v:\n%s", ok, got == hostedGateFixture, got)
	}
}

// Negative: the fail-closed text itself, a rendering whose result job lost its condition, a rendering without its result job
// and plain text are not renderings and return false.
func TestUnrenderDraftSkipNegative(t *testing.T) {
	rendered, _, err := RenderDraftSkip(hostedGateFixture)
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"fail-closed text": hostedGateFixture,
		"edited":           strings.Replace(rendered, "    if: always()\n", "", 1),
		"no result job":    rendered[:strings.Index(rendered, "  gate-result:")],
		"plain":            "name: x\n",
		"empty":            "",
	} {
		if got, ok := UnrenderDraftSkip(text); ok {
			t.Errorf("%s: unrender returned %q", name, got)
		}
	}
}
