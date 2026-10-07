// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package templates_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/templates"
)

// ciJob is the job ID of every CI workflow flavor apply scaffolds; the branch ruleset requires it
// by that name.
const ciJob = "test"

// ciWorkflowTemplates are the CI workflow templates of the flavors (#817) with the contexts each
// renders against: the hosted gate bodies and the Node body.
func ciWorkflowTemplates() map[string][]branchingVariant {
	ci := map[string][]branchingVariant{}
	for name, variants := range renderedYAMLTemplates() {
		if strings.Contains(name, "/ci-") {
			ci[name] = variants
		}
	}
	return ci
}

// Positive: every rendering of every flavor CI template is the hosted gate shape for the default
// branch the emitted gates name, the one check ghworkflow.HostedGateFault holds the emitted gates
// to: the trigger, the draft step first and every later step held off a draft.
func TestCIWorkflowTemplatesRenderTheHostedGateShape(t *testing.T) {
	ci := ciWorkflowTemplates()
	if len(ci) != len(hostedGateYAMLTemplates)+1 {
		t.Fatalf("CI workflow templates = %d, want the %d hosted gate bodies and the Node body", len(ci), len(hostedGateYAMLTemplates))
	}
	for name, variants := range ci {
		for _, variant := range variants {
			body, err := templates.RenderFile(name, variant.ctx)
			if err != nil {
				t.Fatalf("render %s as %s: %v", name, variant.name, err)
			}
			spec, err := ghworkflow.Parse([]byte(body))
			if err != nil {
				t.Fatalf("%s as %s: %v", name, variant.name, err)
			}
			if err := ghworkflow.HostedGateFault(&spec, ciJob, ghworkflow.HostedGateDefaultBranch); err != nil {
				t.Errorf("%s as %s departs from the hosted gate shape: %v\n%s", name, variant.name, err, body)
			}
		}
	}
}

// Negative: the check above fails a rendering without the draft step, one whose later step lost
// its draft condition, one without ready_for_review and one pushing on every branch, so a
// template that stops rendering any part of the shape cannot pass it.
func TestCIWorkflowTemplatesRenderTheHostedGateShape_Negative(t *testing.T) {
	body, err := templates.RenderFile("go/ci-go.yml.tmpl", sampleContext)
	if err != nil {
		t.Fatal(err)
	}
	for label, mutated := range map[string]string{
		"no draft step":         strings.Replace(body, ghworkflow.HostedGateDraftStep, "", 1),
		"a step off its guard":  strings.Replace(body, "- name: Test"+ghworkflow.HostedGateStepIf, "- name: Test", 1),
		"no ready_for_review":   strings.Replace(body, ", "+ghworkflow.HostedGateReadyType, "", 1),
		"every branch push run": strings.Replace(body, ghworkflow.HostedGatePushBranchesPrefix+"main']", "\n    branches: ['**']", 1),
	} {
		if mutated == body {
			t.Fatalf("%s: the mutation changed nothing", label)
		}
		spec, err := ghworkflow.Parse([]byte(mutated))
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if ghworkflow.HostedGateFault(&spec, ciJob, ghworkflow.HostedGateDefaultBranch) == nil {
			t.Errorf("%s: the hosted gate check accepted the mutated rendering", label)
		}
	}
}

// Boundary: the functions render the hosted gate constants byte for byte through either renderer,
// the inline one (Render, default delimiters) included.
func TestHostedGateFunctionsRenderTheConstants(t *testing.T) {
	got, err := templates.Render("hosted-gate", "{{ hostedGateOn }}|{{ hostedGateDraftStep }}|{{ hostedGateStepIf }}", sampleContext)
	if err != nil {
		t.Fatal(err)
	}
	want := ghworkflow.HostedGateOn + "|" + ghworkflow.HostedGateDraftStep + "|" + ghworkflow.HostedGateStepIf
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}
