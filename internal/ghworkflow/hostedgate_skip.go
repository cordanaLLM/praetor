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
	hostedGateJobsMarker  = "\njobs:\n  "
	hostedGateNameMarker  = ":\n    name: "
	hostedGateRunsOn      = "\n    runs-on: "
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
		"    timeout-minutes: %d\n    steps:\n      - name: %s\n        if: needs.%s.result != 'success'\n"+
		"        run: |\n          echo \"%s\"\n          exit 1\n",
		id, context, gateID, runner, hostedGateSkipResultTimeout, hostedGateSkipStepName, gateID, hostedGateSkipMessage)
}
