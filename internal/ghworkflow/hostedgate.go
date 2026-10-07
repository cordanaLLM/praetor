// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// The hosted gate shape is the trigger and draft handling of every hosted gate Praetor emits
// (tools/apicompat, tools/markdownlint), defined once here (HISS-19): the gates render their text
// from these constants, and HostedGateFault checks a parsed workflow against the same shape, so
// an audit of a gate (#817) and the emitter cannot drift apart.
//
// A gate runs on every pull request activity that can change what merges (opened, synchronize,
// reopened, ready_for_review) and on a push to the default branch only (#815). Its job has no
// condition: GitHub reports a job its condition skipped as successful, and a required check
// accepts that, so a skipped draft run could pass the check once the draft is marked ready and
// before the ready_for_review run reports. The job therefore fails on a draft by design: its first
// step (HostedGateDraftStep) runs on a draft only, prints an error annotation and a step summary
// line saying that the gate did not run and runs when the pull request is marked ready, and
// exits 1; every other step carries HostedGateNotDraft. The red check blocks a merge until the
// ready_for_review run reports the same context on the same head commit; its result, the newest
// check of that name, replaces the failure.
const (
	// HostedGateDefaultBranch is the default branch the emitted gate texts name; adoption renders
	// them for the repository's own (managedasset.Family.ForBranch).
	HostedGateDefaultBranch = "main"
	// HostedGatePushBranchesPrefix opens the one line of a gate's push trigger that names the
	// default branch, at the indentation of a key under 'on': push:. The branch is single-quoted,
	// so a branch name YAML would read as a number or a boolean stays a string.
	HostedGatePushBranchesPrefix = "\n    branches: ['"
	// hostedGateTypes are the pull_request activity types a gate runs on, in their order.
	hostedGateTypes = "opened, synchronize, reopened, ready_for_review"
	// HostedGateOn is a gate's whole 'on' block, rendered for HostedGateDefaultBranch.
	HostedGateOn = "'on':\n  pull_request:\n    types: [" + hostedGateTypes + "]\n  push:" +
		HostedGatePushBranchesPrefix + HostedGateDefaultBranch + "']\n"
	// HostedGateDraft is the condition of the draft step: it holds on a draft pull request only.
	// On a push run github.event.pull_request is absent, which GitHub's loose comparison reads
	// as 0, not true (1).
	HostedGateDraft = "github.event.pull_request.draft == true"
	// HostedGateNotDraft is the condition every gate step after the draft step carries: it holds
	// on a push and on a pull request that is not a draft.
	HostedGateNotDraft = "github.event.pull_request.draft != true"
	// HostedGateStepIf is the HostedGateNotDraft line at a gate step's indentation, led by its
	// line break: a gate text appends it to the step's name line, so every uses: line stays a
	// line of its own in the Go source, where Renovate's line-based extractor finds it.
	HostedGateStepIf = "\n        if: " + HostedGateNotDraft
	// HostedGateDraftStepName names the draft step.
	HostedGateDraftStepName = "Stop on a draft pull request"
	// HostedGateDraftTitle is the title of the error annotation the draft step prints. A
	// workflow command property cannot carry ':' or ',' unescaped, and this one carries neither.
	HostedGateDraftTitle = "Gate not run on a draft"
	// hostedGateDraftReason and hostedGateDraftReady are the two halves of the annotation
	// message, one per line of the folded env value.
	hostedGateDraftReason = "The gate did not run because the pull request is a draft."
	hostedGateDraftReady  = "It runs when the pull request is marked ready for review."
	// HostedGateDraftMessage is the message of the draft step's error annotation and its step
	// summary line. The checkpoint planner reads a failed check carrying exactly this title and
	// message on a draft as draft pending (.config/lefthook/scripts/checkpoint.py).
	HostedGateDraftMessage = hostedGateDraftReason + " " + hostedGateDraftReady
	// The three lines of the draft step's script.
	hostedGateDraftAnnotate = `echo "::error title=$TITLE::$MESSAGE"`
	hostedGateDraftSummary  = `echo "$MESSAGE" >> "$GITHUB_STEP_SUMMARY"`
	hostedGateDraftExit     = "exit 1"
	// hostedGateDraftScript is the draft step's run: value as a literal block scalar reads it.
	hostedGateDraftScript = hostedGateDraftAnnotate + "\n" + hostedGateDraftSummary + "\n" + hostedGateDraftExit + "\n"
	// HostedGateDraftStep is a gate job's first step, at step indentation.
	HostedGateDraftStep = "      - name: " + HostedGateDraftStepName + "\n" +
		"        if: " + HostedGateDraft + "\n" +
		"        env:\n" +
		"          TITLE: " + HostedGateDraftTitle + "\n" +
		"          MESSAGE: >-\n" +
		"            " + hostedGateDraftReason + "\n" +
		"            " + hostedGateDraftReady + "\n" +
		"        run: |\n" +
		"          " + hostedGateDraftAnnotate + "\n" +
		"          " + hostedGateDraftSummary + "\n" +
		"          " + hostedGateDraftExit + "\n"
)

// UnwrapExpression returns condition trimmed, with one ${{ }} wrapper around the whole of it
// removed and the inside trimmed, so a bare condition and the same condition as one expression
// read alike. closed is false for an unclosed wrapper, which is returned trimmed as it stands.
func UnwrapExpression(condition string) (expression string, closed bool) {
	expression = strings.TrimSpace(condition)
	inner, wrapped := strings.CutPrefix(expression, expressionOpen)
	if !wrapped {
		return expression, true
	}
	body, closed := strings.CutSuffix(inner, "}}")
	if !closed {
		return expression, false
	}
	return strings.TrimSpace(body), true
}

// HostedGateFault reports how the job jobID of spec departs from the hosted gate shape rendered
// for the default branch branch, or nil when it holds. Only the exact shape passes: the trigger
// is pull_request with exactly the gate's types and push with exactly branch; the job has no
// condition and is not advisory; its first step is HostedGateDraftStep; every later step carries
// HostedGateNotDraft, bare or as one expression, and none continues on error.
func HostedGateFault(spec *Spec, jobID, branch string) error {
	if err := hostedGateTriggerFault(&spec.On, branch); err != nil {
		return err
	}
	job, found := spec.Jobs[jobID]
	switch {
	case !found:
		return fmt.Errorf("the hosted gate has no job %q", jobID)
	case strings.TrimSpace(job.If) != "":
		return fmt.Errorf("job %s carries the condition %q: a job its condition skips reports success, which a required check accepts", jobID, job.If)
	case strings.TrimSpace(job.ContinueOnError) != "":
		return fmt.Errorf("job %s continues on error, so its check passes whatever the gate finds", jobID)
	}
	return hostedGateStepsFault(jobID, job.Steps)
}

// hostedGateTriggerFault reports how an 'on' node departs from HostedGateOn rendered for branch.
func hostedGateTriggerFault(on *yaml.Node, branch string) error {
	if on.Kind != yaml.MappingNode || len(on.Content) != 4 {
		return errors.New("the hosted gate's trigger is not exactly pull_request and push")
	}
	types := sequenceValues(onlyKey(util.YAMLMappingValue(on, "pull_request"), "types"))
	if !slices.Equal(types, strings.Split(hostedGateTypes, ", ")) {
		return fmt.Errorf("the hosted gate's pull_request trigger does not run on exactly the types [%s]", hostedGateTypes)
	}
	branches := sequenceValues(onlyKey(util.YAMLMappingValue(on, "push"), "branches"))
	if !slices.Equal(branches, []string{branch}) {
		return fmt.Errorf("the hosted gate's push trigger does not run on exactly the default branch %s", branch)
	}
	return nil
}

// onlyKey returns the value of key in node when node is a mapping holding key alone, and nil
// for any other node.
func onlyKey(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode || len(node.Content) != 2 || node.Content[0].Value != key {
		return nil
	}
	return node.Content[1]
}

// sequenceValues returns the scalar entries of a sequence node of at most MaxStepsPerJob
// entries, and nil for any other node.
func sequenceValues(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) > MaxStepsPerJob {
		return nil
	}
	values := make([]string, 0, len(node.Content))
	for i := 0; i < len(node.Content) && i < MaxStepsPerJob; i++ {
		if node.Content[i].Kind != yaml.ScalarNode {
			return nil
		}
		values = append(values, node.Content[i].Value)
	}
	return values
}

// hostedGateStepsFault reports how a gate job's steps depart from the draft step followed by
// gate steps that each carry HostedGateNotDraft.
func hostedGateStepsFault(jobID string, steps []Step) error {
	if len(steps) < 2 || len(steps) > MaxStepsPerJob {
		return fmt.Errorf("job %s does not run the draft step and at least one gate step", jobID)
	}
	if err := hostedGateDraftStepFault(&steps[0]); err != nil {
		return fmt.Errorf("job %s: %w", jobID, err)
	}
	for i := 1; i < len(steps) && i < MaxStepsPerJob; i++ {
		condition, closed := UnwrapExpression(steps[i].If)
		if !closed || condition != HostedGateNotDraft || strings.TrimSpace(steps[i].ContinueOnError) != "" {
			return fmt.Errorf("job %s step %d (%s) does not run only when the pull request is not a draft (%s)",
				jobID, i+1, steps[i].Name, HostedGateNotDraft)
		}
	}
	return nil
}

// hostedGateDraftStepFault reports how a step departs from HostedGateDraftStep.
func hostedGateDraftStepFault(step *Step) error {
	if condition, closed := UnwrapExpression(step.If); !closed || condition != HostedGateDraft {
		return fmt.Errorf("the first step does not run on a draft alone (%s)", HostedGateDraft)
	}
	if !runsDraftScript(step) {
		return errors.New("the first step does not run the draft step's script")
	}
	if !carriesDraftMarker(&step.Env) {
		return errors.New("the first step does not carry the draft annotation's title and message")
	}
	return nil
}

// runsDraftScript reports whether step is the named draft step running exactly its script under
// the default shell, without continue-on-error, which would let the refused job pass.
func runsDraftScript(step *Step) bool {
	return step.Name == HostedGateDraftStepName && step.Uses == "" && step.Shell == "" &&
		step.Run == hostedGateDraftScript && strings.TrimSpace(step.ContinueOnError) == ""
}

// carriesDraftMarker reports whether env holds exactly TITLE and MESSAGE with the draft
// annotation's title and message.
func carriesDraftMarker(env *yaml.Node) bool {
	return len(env.Content) == 4 && scalarValue(env, "TITLE") == HostedGateDraftTitle &&
		scalarValue(env, "MESSAGE") == HostedGateDraftMessage
}

// scalarValue returns the scalar value of key in a mapping node, and "" for anything else.
func scalarValue(node *yaml.Node, key string) string {
	value := util.YAMLMappingValue(node, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return value.Value
}
