package planning

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompileRejectsInvalidGraphs(t *testing.T) {
	tests := map[string]func(*Draft){
		"schema":                  func(d *Draft) { d.SchemaVersion = 2 },
		"duplicate source":        func(d *Draft) { d.Sources[1].ID = d.Sources[0].ID },
		"claimed verification":    func(d *Draft) { d.Sources[0].Verified = true },
		"accepted requirement":    func(d *Draft) { d.Requirements[0].Disposition = "accepted" },
		"citation digest":         func(d *Draft) { d.Requirements[0].SourceRefs[0].SourceSHA256 = strings.Repeat("c", 64) },
		"uncovered requirement":   addUncoveredRequirement,
		"milestone cycle":         func(d *Draft) { d.Milestones[0].DependsOn = []string{d.Milestones[1].ID} },
		"step cycle":              func(d *Draft) { d.Steps[1].DependsOn = []string{d.Steps[2].ID} },
		"milestone inversion":     invertMilestoneStep,
		"unknown dependency":      func(d *Draft) { d.Steps[0].DependsOn = []string{"missing"} },
		"one action":              func(d *Draft) { d.Steps[0].Actions = d.Steps[0].Actions[:1] },
		"missing detail":          func(d *Draft) { d.Steps[0].Detail = "" },
		"missing output":          func(d *Draft) { d.Steps[0].ExpectedOutputs = nil },
		"missing negative case":   func(d *Draft) { d.Steps[0].Acceptance.Negative = nil },
		"duplicate TODO":          func(d *Draft) { d.Steps[1].Link.TODOID = d.Steps[0].Link.TODOID },
		"duplicate roadmap":       func(d *Draft) { d.Steps[1].Link.RoadmapID = d.Steps[0].Link.RoadmapID },
		"wrong milestone link":    func(d *Draft) { d.Steps[0].Link.MilestoneID = d.Milestones[0].ID },
		"non-proposed step":       func(d *Draft) { d.Steps[0].Status = "complete" },
		"unsupported action kind": func(d *Draft) { d.Steps[0].Kind = "executable" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			draft := fixtureDraft(t)
			mutate(&draft)
			if _, err := compileDraft(t, draft); err == nil {
				t.Fatal("invalid graph accepted")
			}
		})
	}
}

func addUncoveredRequirement(draft *Draft) {
	requirement := draft.Requirements[0]
	requirement.ID = "requirement-uncovered"
	draft.Requirements = append(draft.Requirements, requirement)
}

func invertMilestoneStep(draft *Draft) {
	// step-research belongs to the prerequisite milestone and may not depend on
	// step-render in the dependent milestone.
	draft.Steps[1].DependsOn = []string{"step-render"}
}

func TestCollectionBoundaries(t *testing.T) {
	tests := []struct {
		name string
		max  int
		make func(*testing.T, int) Draft
	}{
		{"sources", MaxSources, sourceCountDraft},
		{"requirements", MaxRequirements, requirementCountDraft},
		{"milestones", MaxMilestones, milestoneCountDraft},
		{"steps", MaxSteps, stepCountDraft},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := compileDraft(t, test.make(t, test.max)); err != nil {
				t.Fatalf("exact boundary rejected: %v", err)
			}
			if _, err := compileDraft(t, test.make(t, test.max+1)); err == nil {
				t.Fatal("over boundary accepted")
			}
		})
	}
}

func TestNestedBoundaries(t *testing.T) {
	tests := []struct {
		name string
		max  int
		set  func(*Draft, int)
	}{
		{"citations", MaxCitations, setCitations},
		{"actions", MaxActions, func(d *Draft, n int) { d.Steps[0].Actions = textValues("action", n) }},
		{"outputs", MaxExpectedOutputs, setOutputs},
		{"acceptance", MaxAcceptanceCases, func(d *Draft, n int) { d.Steps[0].Acceptance.Positive = textValues("case", n) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := fixtureDraft(t)
			test.set(&draft, test.max)
			if _, err := compileDraft(t, draft); err != nil {
				t.Fatalf("exact boundary rejected: %v", err)
			}
			draft = fixtureDraft(t)
			test.set(&draft, test.max+1)
			if _, err := compileDraft(t, draft); err == nil {
				t.Fatal("over boundary accepted")
			}
		})
	}
}

func TestDependencyAndStringBoundaries(t *testing.T) {
	exact := dependencyCountDraft(t, MaxDependencies)
	if _, err := compileDraft(t, exact); err != nil {
		t.Fatalf("exact dependency boundary rejected: %v", err)
	}
	if _, err := compileDraft(t, dependencyCountDraft(t, MaxDependencies+1)); err == nil {
		t.Fatal("over dependency boundary accepted")
	}
	draft := fixtureDraft(t)
	draft.Steps[0].Detail = strings.Repeat("x", MaxTextBytes)
	if _, err := compileDraft(t, draft); err != nil {
		t.Fatalf("exact string boundary rejected: %v", err)
	}
	draft.Steps[0].Detail += "x"
	if _, err := compileDraft(t, draft); err == nil {
		t.Fatal("over string boundary accepted")
	}
	draft = fixtureDraft(t)
	draft.ID = strings.Repeat("p", MaxIDBytes)
	if _, err := compileDraft(t, draft); err != nil {
		t.Fatalf("exact id boundary rejected: %v", err)
	}
	draft.ID += "p"
	if _, err := compileDraft(t, draft); err == nil {
		t.Fatal("over id boundary accepted")
	}
}

func sourceCountDraft(t *testing.T, count int) Draft {
	draft := minimalDraft(t)
	for index := 1; index < count; index++ {
		source := draft.Sources[0]
		source.ID = fmt.Sprintf("source-%03d", index)
		draft.Sources = append(draft.Sources, source)
	}
	return draft
}

func requirementCountDraft(t *testing.T, count int) Draft {
	draft := minimalDraft(t)
	draft.Requirements = nil
	draft.Steps = nil
	for index := 0; index < count; index++ {
		requirement := fixtureDraft(t).Requirements[0]
		requirement.ID = fmt.Sprintf("requirement-%03d", index)
		draft.Requirements = append(draft.Requirements, requirement)
	}
	for offset := 0; offset < count; offset += MaxDependencies {
		end := min(offset+MaxDependencies, count)
		step := baseStep(t, len(draft.Steps), "milestone-contract")
		step.RequirementIDs = nil
		for _, requirement := range draft.Requirements[offset:end] {
			step.RequirementIDs = append(step.RequirementIDs, requirement.ID)
		}
		draft.Steps = append(draft.Steps, step)
	}
	return draft
}

func milestoneCountDraft(t *testing.T, count int) Draft {
	draft := minimalDraft(t)
	draft.Milestones, draft.Steps = nil, nil
	for index := 0; index < count; index++ {
		milestone := fixtureDraft(t).Milestones[0]
		milestone.ID = fmt.Sprintf("milestone-%03d", index)
		draft.Milestones = append(draft.Milestones, milestone)
		draft.Steps = append(draft.Steps, baseStep(t, index, milestone.ID))
	}
	return draft
}

func stepCountDraft(t *testing.T, count int) Draft {
	draft := minimalDraft(t)
	draft.Steps = nil
	for index := 0; index < count; index++ {
		draft.Steps = append(draft.Steps, baseStep(t, index, draft.Milestones[0].ID))
	}
	return draft
}

func dependencyCountDraft(t *testing.T, count int) Draft {
	draft := stepCountDraft(t, count+1)
	for index := 0; index < count; index++ {
		draft.Steps[count].DependsOn = append(draft.Steps[count].DependsOn, draft.Steps[index].ID)
	}
	return draft
}

func minimalDraft(t *testing.T) Draft {
	draft := fixtureDraft(t)
	draft.Sources = draft.Sources[:1]
	draft.Requirements = draft.Requirements[:1]
	draft.Milestones = draft.Milestones[:1]
	draft.Steps = draft.Steps[1:2]
	return draft
}

func baseStep(t *testing.T, index int, milestoneID string) Step {
	step := fixtureDraft(t).Steps[1]
	step.ID = fmt.Sprintf("step-%03d", index)
	step.MilestoneID, step.Link.MilestoneID = milestoneID, milestoneID
	step.Link.TODOID = fmt.Sprintf("todo-%03d", index)
	step.Link.RoadmapID = fmt.Sprintf("roadmap-%03d", index)
	step.ExpectedOutputs[0].ID = fmt.Sprintf("output-%03d", index)
	step.RequirementIDs, step.DependsOn = []string{"requirement-contract"}, []string{}
	return step
}

func setCitations(draft *Draft, count int) {
	draft.Requirements[0].SourceRefs = nil
	for index := 0; index < count; index++ {
		ref := fixtureDraftForHelper().Requirements[0].SourceRefs[0]
		ref.Quote = fmt.Sprintf("citation %03d", index)
		draft.Requirements[0].SourceRefs = append(draft.Requirements[0].SourceRefs, ref)
	}
}

func setOutputs(draft *Draft, count int) {
	draft.Steps[0].ExpectedOutputs = nil
	for index := 0; index < count; index++ {
		draft.Steps[0].ExpectedOutputs = append(draft.Steps[0].ExpectedOutputs,
			ExpectedOutput{ID: fmt.Sprintf("output-boundary-%03d", index), Description: fmt.Sprintf("output %03d", index)})
	}
}

func textValues(prefix string, count int) []string {
	values := make([]string, 0, count)
	for index := 0; index < count; index++ {
		values = append(values, fmt.Sprintf("%s %03d", prefix, index))
	}
	return values
}

// fixtureDraftForHelper avoids passing testing state through small mutation callbacks.
func fixtureDraftForHelper() Draft {
	return Draft{Requirements: []Requirement{{SourceRefs: []Citation{{
		SourceID: "source-research", SourceSHA256: strings.Repeat("a", 64), Quote: "fixture",
	}}}}}
}
