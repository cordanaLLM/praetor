package planning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

var unsupportedStages = []string{
	"existing-ledger compare-and-swap apply",
	"source-adapter byte verification",
	"step execution and completion evidence",
}

type digestInput struct {
	Draft          Draft    `json:"draft"`
	MilestoneOrder []string `json:"milestone_order"`
	StepOrder      []string `json:"step_order"`
	Unsupported    []string `json:"unsupported"`
}

// Compile strictly decodes, validates, normalizes, and renders a planning
// proposal. It performs no filesystem, network, model, tool, or shell action.
func Compile(ctx context.Context, raw []byte) (*Result, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	draft, err := decodeDraft(raw)
	if err != nil {
		return nil, err
	}
	milestoneOrder, stepOrder, err := validateDraft(ctx, &draft)
	if err != nil {
		return nil, err
	}
	normalizeDraft(&draft, milestoneOrder, stepOrder)
	core := digestInput{Draft: draft, MilestoneOrder: milestoneOrder,
		StepOrder: stepOrder, Unsupported: append([]string(nil), unsupportedStages...)}
	canonical, err := json.Marshal(core)
	if err != nil {
		return nil, fmt.Errorf("marshal planning digest input: %w", err)
	}
	sum := sha256.Sum256(canonical)
	result := &Result{Status: StatusStructurallyValid, ReviewRequired: true,
		ProvenanceStatus: ProvenanceUnverified, Digest: hex.EncodeToString(sum[:]),
		MilestoneOrder: append([]string(nil), milestoneOrder...), StepOrder: append([]string(nil), stepOrder...),
		Unsupported: append([]string(nil), unsupportedStages...), Draft: draft}
	if err := renderArtifacts(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func normalizeDraft(draft *Draft, milestoneOrder, stepOrder []string) {
	sort.Slice(draft.Sources, func(i, j int) bool { return draft.Sources[i].ID < draft.Sources[j].ID })
	sort.Slice(draft.Requirements, func(i, j int) bool { return draft.Requirements[i].ID < draft.Requirements[j].ID })
	for index := range draft.Requirements {
		sort.Slice(draft.Requirements[index].SourceRefs, func(i, j int) bool {
			left, right := draft.Requirements[index].SourceRefs[i], draft.Requirements[index].SourceRefs[j]
			if left.SourceID != right.SourceID {
				return left.SourceID < right.SourceID
			}
			if left.SourceSHA256 != right.SourceSHA256 {
				return left.SourceSHA256 < right.SourceSHA256
			}
			return left.Quote < right.Quote
		})
	}
	normalizeMilestones(draft, milestoneOrder)
	normalizeSteps(draft, stepOrder)
}

func normalizeMilestones(draft *Draft, order []string) {
	byID := make(map[string]Milestone, len(draft.Milestones))
	for _, milestone := range draft.Milestones {
		sort.Strings(milestone.DependsOn)
		normalizeAcceptance(&milestone.Acceptance)
		byID[milestone.ID] = milestone
	}
	ordered := make([]Milestone, 0, len(order))
	for _, id := range order {
		ordered = append(ordered, byID[id])
	}
	draft.Milestones = ordered
}

func normalizeSteps(draft *Draft, order []string) {
	byID := make(map[string]Step, len(draft.Steps))
	for _, step := range draft.Steps {
		sort.Strings(step.RequirementIDs)
		sort.Strings(step.DependsOn)
		sort.Slice(step.ExpectedOutputs, func(i, j int) bool { return step.ExpectedOutputs[i].ID < step.ExpectedOutputs[j].ID })
		normalizeAcceptance(&step.Acceptance)
		byID[step.ID] = step
	}
	ordered := make([]Step, 0, len(order))
	for _, id := range order {
		ordered = append(ordered, byID[id])
	}
	draft.Steps = ordered
}

func normalizeAcceptance(acceptance *Acceptance) {
	sort.Strings(acceptance.Positive)
	sort.Strings(acceptance.Negative)
	sort.Strings(acceptance.Boundary)
}
