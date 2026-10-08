// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"errors"
	"fmt"
	"strings"
)

// The opt-in draft skip shape of a hosted gate (#857) is derived from the fail-closed shape in
// hostedgate.go, never retyped: RenderDraftSkip rewrites a gate text built from HostedGateOn,
// HostedGateDraftStep and HostedGateStepIf, so the two shapes cannot drift apart.
//
// GitHub reports a job its condition skipped as successful, and a required check accepts that, so
// a gate job that skips a draft cannot be the required check. The skip shape therefore splits the
// gate in two jobs: the gate job, which carries HostedGateSkipCondition and no draft step, and a
// result job named like the original gate job (the required check's context) that needs it, runs
// under always() and fails unless the gate job succeeded. A draft skips the gate job, so the
// result job, and with it the required check, fails until the ready_for_review run reports. The
// audit accepts the shape only while that proof holds for the file (internal/forge,
// DraftSkipFault).
const (
	// HostedGateSkipCondition is the condition of the gate job in the skip shape: it holds on a
	// push and on a pull request that is not a draft.
	HostedGateSkipCondition = "github.event_name != 'pull_request' || " + PullRequestDraftField + " == false"
	// HostedGateSkipGateSuffix ends the name of the gate job in the skip shape, which no longer
	// reports the required context.
	HostedGateSkipGateSuffix = " gate"
	// HostedGateSkipResultSuffix ends the id of the result job in the skip shape.
	HostedGateSkipResultSuffix = "-result"
	// hostedGateSkipResultTimeout is the timeout-minutes of the result job, which runs one echo.
	hostedGateSkipResultTimeout = 5
	// hostedGateSkipStepName names the result job's one step.
	hostedGateSkipStepName = "Fail unless the gate passed"
	// hostedGateSkipMessage is the line the result job's step prints before it exits. It holds
	// only what the aggregate proof reads as a message line (internal/forge gateScript).
	hostedGateSkipMessage = "The gate did not pass or was skipped on a draft."
	// hostedGateSkipDraftStepName names the result job's step that runs when a draft skipped the
	// gate job.
	hostedGateSkipDraftStepName = "Report the draft"
	// hostedGateSkipAnnotation is that step's one message line: the error annotation the
	// checkpoint planner reads as draft pending (HostedGateDraftTitle, HostedGateDraftMessage). It
	// is a plain echo line, so the aggregate proof reads the step as an exit step. It is longer
	// than yamllint's 80 columns and a block scalar cannot fold it, so the step sits between
	// yamllint comments.
	hostedGateSkipAnnotation = "::error title=" + HostedGateDraftTitle + "::" + HostedGateDraftMessage
	hostedGateStepsLine      = "    steps:\n"
	hostedGateStepNamePrefix = "      - name: "
	// maxUnrenderLines bounds the lines UnrenderDraftSkip restores (HISS-02).
	maxUnrenderLines     = 4096
	hostedGateJobsMarker = "\njobs:\n  "
	hostedGateNameMarker = ":\n    name: "
	hostedGateRunsOn     = "\n    runs-on: "
)

// hostedGateSkipGateIf is the gate job's condition as a folded scalar, so the line stays within
// yamllint's 80 columns; a reader sees HostedGateSkipCondition.
const hostedGateSkipGateIf = "    if: >-\n" +
	"      github.event_name != 'pull_request'\n" +
	"      || " + PullRequestDraftField + " == false\n"

// DraftSkipJobs names the two jobs of a gate in the skip shape: the gate job and the result job.
func DraftSkipJobs(gateID string) (gate, result string) {
	return gateID, gateID + HostedGateSkipResultSuffix
}

// RenderDraftSkip renders the skip shape of a hosted gate text in the fail-closed shape: one job
// whose first step is HostedGateDraftStep and whose later steps carry HostedGateStepIf, followed
// by nothing. It returns the text and the id of the gate job. Any other text is an error, so a
// gate whose shape moved cannot be rendered into something that was never audited.
func RenderDraftSkip(text string) (rendered, gateID string, err error) {
	head, jobs, found := strings.Cut(text, hostedGateJobsMarker)
	if !found || strings.Count(text, HostedGateDraftStep) != 1 || !strings.Contains(text, HostedGateStepIf) ||
		strings.Count(jobs, hostedGateNameMarker) != 1 {
		return "", "", errors.New("the gate text is not one job in the fail-closed hosted gate shape: " +
			"it needs a jobs block with one named job, one draft step and conditioned steps")
	}
	gateID, afterID, _ := strings.Cut(jobs, hostedGateNameMarker)
	context, afterName, _ := strings.Cut(afterID, "\n")
	runner, ok := strings.CutPrefix(afterName, hostedGateRunsOn[1:])
	runner, _, _ = strings.Cut(runner, "\n")
	if !ok || gateID == "" || strings.ContainsAny(gateID, " \n") || runner == "" {
		return "", "", errors.New("the gate job has no name line followed by a runs-on line")
	}
	body := strings.ReplaceAll(strings.Replace(afterName, HostedGateDraftStep, "", 1), HostedGateStepIf, "")
	return head + hostedGateJobsMarker + gateID + hostedGateNameMarker + context + HostedGateSkipGateSuffix + "\n" +
		hostedGateSkipGateIf + body + resultJob(gateID, context, runner), gateID, nil
}

// resultJob is the result job of the skip shape: it reports the required context and fails unless
// the gate job succeeded. Its condition, always(), makes it run whatever the gate job did, and
// its one exit step is a shape the aggregate proof reads (internal/forge, provenAggregateNeeds).
func resultJob(gateID, context, runner string) string {
	_, id := DraftSkipJobs(gateID)
	return fmt.Sprintf("  %s:\n    name: %s\n    needs: [%s]\n    if: always()\n    runs-on: %s\n"+
		"    timeout-minutes: %d\n    steps:\n"+
		"      # yamllint disable rule:line-length\n"+
		"      - name: %s\n        if: needs.%s.result == 'skipped'\n"+
		"        run: |\n          echo \"%s\"\n          exit 1\n"+
		"      # yamllint enable rule:line-length\n"+
		"      - name: %s\n        if: needs.%s.result != 'success'\n"+
		"        run: |\n          echo \"%s\"\n          exit 1\n",
		id, context, gateID, runner, hostedGateSkipResultTimeout,
		hostedGateSkipDraftStepName, gateID, hostedGateSkipAnnotation,
		hostedGateSkipStepName, gateID, hostedGateSkipMessage)
}

// UnrenderDraftSkip is the inverse of RenderDraftSkip: it returns the fail-closed gate text a skip
// rendering was derived from, or false for any other text. Adoption reads a skip copy as the
// fail-closed text it came from, so one list of earlier texts (managedasset Prior) covers both
// shapes. The result must render back to text byte for byte, so a text that is not exactly a
// rendering returns false.
func UnrenderDraftSkip(text string) (failClosed string, ok bool) {
	head, gateID, context, body, ok := cutSkipGate(text)
	if !ok {
		return "", false
	}
	steps, hasSteps := restoreGateSteps(body + "\n")
	if !hasSteps {
		return "", false
	}
	restored := head + hostedGateJobsMarker + gateID + hostedGateNameMarker + context + "\n" + steps
	if again, _, err := RenderDraftSkip(restored); err != nil || again != text {
		return "", false
	}
	return restored, true
}

// cutSkipGate cuts a skip rendering into the text before its jobs block, the id of the gate job,
// the required context (the gate job's name without HostedGateSkipGateSuffix), and the gate job's
// body after its condition up to the result job. It reports false when any of those is missing.
func cutSkipGate(text string) (head, gateID, context, body string, ok bool) {
	head, jobs, found := strings.Cut(text, hostedGateJobsMarker)
	gateID, afterID, named := strings.Cut(jobs, hostedGateNameMarker)
	if !found || !named || gateID == "" || strings.ContainsAny(gateID, " \n") {
		return "", "", "", "", false
	}
	nameLine, afterName, _ := strings.Cut(afterID, "\n")
	context, suffixed := strings.CutSuffix(nameLine, HostedGateSkipGateSuffix)
	body, hasIf := strings.CutPrefix(afterName, hostedGateSkipGateIf)
	_, result := DraftSkipJobs(gateID)
	body, _, hasResult := strings.Cut(body, "\n  "+result+":\n")
	if !suffixed || !hasIf || !hasResult {
		return "", "", "", "", false
	}
	return head, gateID, context, body, true
}

// restoreGateSteps puts the draft step and the step conditions back into the body of a gate job
// in the skip shape: the draft step opens the steps block, and every step after it carries
// HostedGateStepIf. It returns false for a body with no steps block.
func restoreGateSteps(body string) (string, bool) {
	before, after, found := strings.Cut(body, hostedGateStepsLine)
	if !found {
		return "", false
	}
	lines := strings.Split(after, "\n")
	for i := 0; i < len(lines) && i < maxUnrenderLines; i++ {
		if strings.HasPrefix(lines[i], hostedGateStepNamePrefix) {
			lines[i] += HostedGateStepIf
		}
	}
	return before + hostedGateStepsLine + HostedGateDraftStep + strings.Join(lines, "\n"), true
}
