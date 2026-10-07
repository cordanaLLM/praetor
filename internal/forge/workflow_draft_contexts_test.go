// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

// #817: praetor's own workflows and the flavor CI templates follow the hosted gate shape. On a
// draft pull request every required check must fail in its draft step before any work runs, and
// marking the draft ready must start the run that replaces the failure. The tests below read that
// from the YAML itself: the committed ruleset's contexts, the jobs that report them, and the
// steps GitHub runs after the draft step has failed.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// afterFailureFunctions are the status functions that let a step run after an earlier step of its
// job failed; a condition without one carries an implicit success() and is skipped.
var afterFailureFunctions = []string{"always()", "failure()", "cancelled()"}

// runsAfterTheDraftStep reports whether GitHub runs step on a draft once the draft step has
// failed: its condition calls a status function that holds after a failure and does not also
// require HostedGateNotDraft as a conjunct.
func runsAfterTheDraftStep(step workflowStep) bool {
	condition, _ := ghworkflow.UnwrapExpression(step.If)
	compact := strings.Join(strings.Fields(condition), "")
	if !slices.ContainsFunc(afterFailureFunctions, func(function string) bool { return strings.Contains(compact, function) }) {
		return false
	}
	notDraft := strings.Join(strings.Fields(ghworkflow.HostedGateNotDraft), "")
	return compact != notDraft && !strings.HasPrefix(compact, notDraft+"&&") && !strings.Contains(compact, "&&"+notDraft)
}

// draftContextFault says how a job reporting a required check departs from failing a draft in
// its draft step before any work, with types the pull_request trigger's activity types, or
// returns nil: the trigger runs on ready_for_review, the job is neither advisory nor skipped on a
// draft by its condition, its first step is the draft step, and no later step runs once that step
// has failed.
func draftContextFault(job workflowJob, types []string) error {
	switch {
	case !slices.Contains(types, ghworkflow.HostedGateReadyType):
		return fmt.Errorf("its pull_request trigger does not run on %s (types %v)", ghworkflow.HostedGateReadyType, types)
	case advisoryJob(job.ContinueOnError):
		return fmt.Errorf("the job continues on error")
	case strings.Contains(job.If, ghworkflow.PullRequestDraftField):
		return fmt.Errorf("the job condition reads the draft flag: %s", job.If)
	case len(job.Steps) == 0:
		return fmt.Errorf("the job has no steps")
	}
	if err := ghworkflow.DraftStepFault(&job.Steps[0]); err != nil {
		return err
	}
	for i := 1; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		if runsAfterTheDraftStep(job.Steps[i]) {
			return fmt.Errorf("step %d (%s) runs after the draft step failed: %s", i+1, job.Steps[i].Name, job.Steps[i].If)
		}
	}
	return nil
}

// draftContextFaults returns, for one workflow document, every context a job reports on every
// pull request inside identity mapped to how that job departs from failing a draft (nil when it
// fails a draft as it must). A workflow that does not run on every pull request reports none.
func draftContextFaults(t *testing.T, data []byte, identity string) map[string]error {
	t.Helper()
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	trigger, declared := declaredTrigger(&spec.On, pullRequestEvent)
	if !declared || !triggersOnEveryPullRequest(&spec.On) {
		return nil
	}
	types := ghworkflow.StringList(util.YAMLMappingValue(trigger.value, "types"))
	faults := map[string]error{}
	for _, id := range sortedJobIDs(spec.Jobs) {
		job := spec.Jobs[id]
		if !reportsOnEveryPullRequest(job.If, identity) || advisoryJob(job.ContinueOnError) {
			continue
		}
		contexts, err := jobCheckContexts(id, job)
		if err != nil {
			t.Fatal(err)
		}
		for _, context := range contexts {
			faults[context] = draftContextFault(job, types)
		}
	}
	return faults
}

// committedRequiredContexts lists the required status contexts of the committed ruleset.
func committedRequiredContexts(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(engineRoot, filepath.FromSlash(RepositoryRulesetPath)))
	if err != nil {
		t.Fatal(err)
	}
	ruleset, err := parseStatusRuleset(data)
	if err != nil {
		t.Fatal(err)
	}
	var contexts []string
	for _, rule := range ruleset.Rules {
		for _, check := range rule.Parameters.RequiredStatusChecks {
			contexts = append(contexts, check.Context)
		}
	}
	return contexts
}

// Positive: a draft pull request event leaves every context the committed ruleset requires failed
// by its draft step, and the context names are unchanged: each is still reported by a job of
// this repository's workflows that runs on every pull request, and every such job fails a draft
// before its work, with ready_for_review starting the run that replaces the failure.
func TestEngineRequiredContextsFailOnADraft(t *testing.T) {
	workflows, identity := engineWorkflows(t)
	reported := map[string]error{}
	for name, data := range workflows {
		for context, fault := range draftContextFaults(t, data, identity) {
			if fault != nil {
				fault = fmt.Errorf("%s: %w", name, fault)
			}
			reported[context] = fault
		}
	}
	required := committedRequiredContexts(t)
	if len(required) < 10 {
		t.Fatalf("the committed ruleset requires %d contexts, want every gate", len(required))
	}
	for _, context := range required {
		fault, found := reported[context]
		switch {
		case !found:
			t.Errorf("required context %q is reported by no job that runs on every pull request", context)
		case fault != nil:
			t.Errorf("required context %q does not fail a draft in its draft step: %v", context, fault)
		}
	}
}

// Positive: every flavor CI template, rendered, fails its required check (the "test" job) on a
// draft in its draft step, the shape templates/hostedgate.go renders.
func TestFlavorCIRenderingsFailOnADraft(t *testing.T) {
	for _, source := range []string{"go/ci-go.yml.tmpl", "rust/ci-rust.yml.tmpl", "jvm/ci-jvm.yml.tmpl", "flutter/ci-flutter.yml.tmpl", "node/ci-node.yml.tmpl"} {
		body, err := templates.RenderFile(source, templates.Context{RepoName: "widget", Owner: "acme"})
		if err != nil {
			t.Fatal(err)
		}
		faults := draftContextFaults(t, []byte(body), "acme/widget")
		if fault, found := faults["test"]; !found || fault != nil || len(faults) != 1 {
			t.Errorf("%s: draft faults %v", source, faults)
		}
	}
}

// Negative: a job without the draft step, a trigger without ready_for_review, a later step that
// runs on after a failure, a draft-skipping job condition and an advisory job are each refused.
// Boundary: a step running after a failure but held off a draft passes, and so does a step with
// a plain condition, which carries an implicit success().
func TestDraftContextFault(t *testing.T) {
	spec, err := ghworkflow.Parse([]byte("jobs:\n  gate:\n    steps:\n" + ghworkflow.HostedGateDraftStep + "      - run: make\n"))
	if err != nil {
		t.Fatal(err)
	}
	gate := spec.Jobs["gate"]
	ready := []string{"opened", ghworkflow.HostedGateReadyType}
	with := func(step workflowStep) workflowJob {
		job := gate
		job.Steps = append(slices.Clone(gate.Steps), step)
		return job
	}
	cases := []struct {
		name  string
		job   workflowJob
		types []string
		want  string
	}{
		{"positive", gate, ready, ""},
		{"boundary held off a draft", with(workflowStep{Run: "make", If: "${{ !cancelled() && " + ghworkflow.HostedGateNotDraft + " }}"}), ready, ""},
		{"boundary plain condition", with(workflowStep{Run: "make", If: "github.event_name == 'pull_request'"}), ready, ""},
		{"negative no draft step", workflowJob{Steps: gate.Steps[1:]}, ready, "first step"},
		{"negative no ready_for_review", gate, []string{"opened"}, ghworkflow.HostedGateReadyType},
		{"negative runs after a failure", with(workflowStep{Name: "Vet", Run: "go vet", If: "${{ !cancelled() }}"}), ready, "runs after the draft step failed"},
		{"negative always", with(workflowStep{Name: "Report", Run: "make", If: "always()"}), ready, "runs after the draft step failed"},
		{"negative draft skip", workflowJob{If: ghworkflow.HostedGateNotDraft, Steps: gate.Steps}, ready, "reads the draft flag"},
		{"negative advisory", workflowJob{ContinueOnError: "true", Steps: gate.Steps}, ready, "continues on error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := draftContextFault(tc.job, tc.types)
			if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("fault = %v, want containing %q", err, tc.want)
			}
		})
	}
}
