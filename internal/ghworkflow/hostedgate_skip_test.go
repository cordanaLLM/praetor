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
		len(got.Steps) != 1 || got.Steps[0].If != "needs.gate.result != 'success'" {
		t.Fatalf("result job = %+v, want the original context behind always() needing the gate job", got)
	}
	if again, _, err := RenderDraftSkip(hostedGateFixture); err != nil || again != rendered {
		t.Fatalf("the rendering is not deterministic: %v", err)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if len(line) > 80 {
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
