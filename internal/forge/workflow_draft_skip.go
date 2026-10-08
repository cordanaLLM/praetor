// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// The opt-in draft skip (hosted_gates.draft: skip, #857) skips a gate job on a draft with a
// job-level condition. GitHub reports a skipped job as successful and a required check accepts
// that, so the skip is safe only where the required check is not the gate job but a proven
// aggregate (provenAggregateNeeds, #76) that needs it and fails when the gate job is skipped:
// the draft then fails the required check until the ready_for_review run reports. DraftSkipFault
// is the audit's guard for a managed workflow; provenDraftSkips gives the trigger audit
// (workflow_trigger_audit.go) the same proof for every workflow, so the two cannot disagree.

// skipConditionSpelling returns condition with whitespace collapsed and one ${{ }} wrapper
// removed, the form HostedGateSkipCondition is compared in.
func skipConditionSpelling(condition string) string {
	expression, closed := ghworkflow.UnwrapExpression(condition)
	if !closed {
		return ""
	}
	return strings.Join(strings.Fields(expression), " ")
}

// carriesDraftSkip reports whether job's condition is exactly ghworkflow.HostedGateSkipCondition.
func carriesDraftSkip(job *workflowJob) bool {
	return skipConditionSpelling(job.If) == ghworkflow.HostedGateSkipCondition
}

// draftSkipAggregateFault says why aggregate does not keep a required check failing on a draft
// that skips the gate job gateID, or returns "" when it does: aggregate is a proven aggregate
// (provenAggregateNeeds) covering gateID whose steps fail when gateID is skipped and every other
// need succeeded. The skip is what a draft leaves of the gate job, so an aggregate that passes
// on it lets the draft satisfy the required check.
func draftSkipAggregateFault(spec *workflowSpec, aggregateID, gateID string) string {
	aggregate := spec.Jobs[aggregateID]
	needs := provenAggregateNeeds(spec, &aggregate)
	if !slices.Contains(needs, gateID) {
		return fmt.Sprintf("job %s is not a proven aggregate covering %s: it must need %s, run under always() alone and "+
			"hold only exit and alls-green steps that fail when %s fails or is cancelled (workflow_aggregate.go)",
			aggregateID, gateID, gateID, gateID)
	}
	steps, _, _ := aggregateShape(spec, &aggregate)
	if fails, known := aggregateFails(steps, needsResults(aggregate.NeedIDs(), gateID, needsResultSkipped)); !known || !fails {
		return fmt.Sprintf("aggregate %s passes while %s is skipped, which is what a draft leaves of it: a step must fail "+
			"the job when needs.%s.result is %q", aggregateID, gateID, gateID, needsResultSkipped)
	}
	return ""
}

// DraftSkipFault reports why the workflow data may not skip a draft at the job level, or nil when
// it may. gateJobID is the job carrying the skip and statusContext the required check the
// workflow reports. Three things must hold: the gate job carries exactly
// ghworkflow.HostedGateSkipCondition; one job other than the gate job reports statusContext, so
// the gate job's skipped draft does not satisfy it; and that job is a proven aggregate that fails
// on a draft (draftSkipAggregateFault). Each refusal names the missing condition.
func DraftSkipFault(data []byte, gateJobID, statusContext string) error {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return err
	}
	gate, found := spec.Jobs[gateJobID]
	if !found {
		return fmt.Errorf("the workflow has no gate job %q", gateJobID)
	}
	if !carriesDraftSkip(&gate) {
		return fmt.Errorf("gate job %s does not carry the draft skip condition %q", gateJobID, ghworkflow.HostedGateSkipCondition)
	}
	reporter, err := contextReporter(&spec, statusContext)
	if err != nil {
		return err
	}
	if reporter == gateJobID {
		return fmt.Errorf("the required check %q is the gate job %s itself, whose draft skip reports success, which a "+
			"required check accepts: no aggregate job reports it", statusContext, gateJobID)
	}
	if reason := draftSkipAggregateFault(&spec, reporter, gateJobID); reason != "" {
		return fmt.Errorf("the required check %q is reported by job %s: %s", statusContext, reporter, reason)
	}
	return nil
}

// contextReporter returns the id of the one job of spec that reports the check context.
func contextReporter(spec *workflowSpec, context string) (string, error) {
	ids := sortedJobIDs(spec.Jobs)
	reporters := make([]string, 0, 1)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		contexts, err := jobCheckContexts(ids[i], spec.Jobs[ids[i]])
		if err == nil && slices.Contains(contexts, context) {
			reporters = append(reporters, ids[i])
		}
	}
	switch len(reporters) {
	case 0:
		return "", fmt.Errorf("no job of the workflow reports the required check %q", context)
	case 1:
		return reporters[0], nil
	}
	return "", fmt.Errorf("jobs %s all report the required check %q", strings.Join(reporters, ", "), context)
}

// provenDraftSkips returns the ids of the jobs of spec whose draft skip is proven: a job that
// carries exactly the draft skip condition and is covered by an aggregate that fails on it
// (draftSkipAggregateFault), and each such aggregate. Such a gate job stops a draft by way of the
// aggregate, which the trigger audit counts as stopping it.
func provenDraftSkips(spec *workflowSpec) map[string]bool {
	ids := sortedJobIDs(spec.Jobs)
	proven := make(map[string]bool)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		gate := spec.Jobs[ids[i]]
		if !carriesDraftSkip(&gate) {
			continue
		}
		for j := 0; j < len(ids) && j < maxJobsPerFile; j++ {
			if ids[j] != ids[i] && draftSkipAggregateFault(spec, ids[j], ids[i]) == "" {
				proven[ids[i]], proven[ids[j]] = true, true
			}
		}
	}
	return proven
}
