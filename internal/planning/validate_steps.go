package planning

import (
	"context"
	"fmt"
)

func validateSteps(ctx context.Context, list []Step, requirements, milestones map[string]bool,
	milestoneOrder []string, prerequisites map[string]map[string]bool) ([]graphNode, error) {
	stepIDs := make(map[string]bool, len(list))
	stepMilestones := make(map[string]string, len(list))
	milestoneRanks := rankIDs(milestoneOrder)
	coveredRequirements := make(map[string]bool, len(requirements))
	coveredMilestones := make(map[string]bool, len(milestones))
	links, outputs := make(map[string]bool, len(list)*2), make(map[string]bool)
	nodes := make([]graphNode, 0, len(list))
	for _, step := range list {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := validateStep(step, stepIDs, requirements, milestones, links, outputs); err != nil {
			return nil, err
		}
		stepIDs[step.ID], stepMilestones[step.ID], coveredMilestones[step.MilestoneID] = true, step.MilestoneID, true
		for _, requirementID := range step.RequirementIDs {
			coveredRequirements[requirementID] = true
		}
		nodes = append(nodes, graphNode{id: step.ID, dependencies: step.DependsOn, rank: milestoneRanks[step.MilestoneID]})
	}
	if err := validateReferences("step dependency", nodes, stepIDs); err != nil {
		return nil, err
	}
	if err := validateMilestoneDirection(list, stepMilestones, prerequisites); err != nil {
		return nil, err
	}
	addMilestoneDependencies(nodes, stepMilestones, prerequisites)
	if len(coveredRequirements) != len(requirements) || len(coveredMilestones) != len(milestones) {
		return nil, fmt.Errorf("every requirement and milestone must be covered by a step")
	}
	return nodes, nil
}

func rankIDs(order []string) map[string]int {
	ranks := make(map[string]int, len(order))
	for index, id := range order {
		ranks[id] = index
	}
	return ranks
}

func validateMilestoneDirection(steps []Step, stepMilestones map[string]string, prerequisites map[string]map[string]bool) error {
	for _, step := range steps {
		for _, dependency := range step.DependsOn {
			dependencyMilestone := stepMilestones[dependency]
			if prerequisites[dependencyMilestone][step.MilestoneID] {
				return fmt.Errorf("step %s dependency %s inverts milestone order", step.ID, dependency)
			}
		}
	}
	return nil
}

func addMilestoneDependencies(nodes []graphNode, stepMilestones map[string]string, prerequisites map[string]map[string]bool) {
	for index := range nodes {
		seen := make(map[string]bool, len(nodes[index].dependencies))
		for _, dependency := range nodes[index].dependencies {
			seen[dependency] = true
		}
		for stepID, milestoneID := range stepMilestones {
			if prerequisites[stepMilestones[nodes[index].id]][milestoneID] && !seen[stepID] {
				nodes[index].dependencies = append(nodes[index].dependencies, stepID)
				seen[stepID] = true
			}
		}
	}
}

func milestonePrerequisites(milestones []Milestone) map[string]map[string]bool {
	result := make(map[string]map[string]bool, len(milestones))
	for _, milestone := range milestones {
		result[milestone.ID] = make(map[string]bool, len(milestone.DependsOn))
		for _, dependency := range milestone.DependsOn {
			result[milestone.ID][dependency] = true
		}
	}
	for iteration := 0; iteration < len(milestones); iteration++ {
		for _, milestone := range milestones {
			for dependency := range result[milestone.ID] {
				for inherited := range result[dependency] {
					result[milestone.ID][inherited] = true
				}
			}
		}
	}
	return result
}

func validateStep(step Step, stepIDs, requirements, milestones, links, outputs map[string]bool) error {
	if err := validateStepIdentity(step, stepIDs); err != nil {
		return err
	}
	if err := validateStepLink(step, milestones, links); err != nil {
		return err
	}
	links["todo:"+step.Link.TODOID], links["roadmap:"+step.Link.RoadmapID] = true, true
	if err := validateStepRequirements(step, requirements); err != nil {
		return err
	}
	if err := validateIDs("step dependency", step.ID, step.DependsOn, MaxDependencies); err != nil {
		return err
	}
	if err := validateTextList("step action", step.Actions, 2, MaxActions); err != nil {
		return fmt.Errorf("step %s: %w", step.ID, err)
	}
	if err := validateOutputs(step.ID, step.ExpectedOutputs, outputs); err != nil {
		return err
	}
	return validateAcceptance("step "+step.ID, step.Acceptance)
}

func validateStepIdentity(step Step, seen map[string]bool) error {
	allowedKind := map[string]bool{"manual": true, "research": true, "implementation": true}
	if !validID(step.ID) || !validText(step.Title) || !validText(step.Detail) || seen[step.ID] ||
		!allowedKind[step.Kind] || step.Status != "proposed" {
		return fmt.Errorf("invalid or duplicate proposed step %q", step.ID)
	}
	return nil
}

func validateStepLink(step Step, milestones, links map[string]bool) error {
	if !milestones[step.MilestoneID] || step.Link.MilestoneID != step.MilestoneID ||
		!validID(step.Link.TODOID) || !validID(step.Link.RoadmapID) ||
		links["todo:"+step.Link.TODOID] || links["roadmap:"+step.Link.RoadmapID] {
		return fmt.Errorf("step %s requires unique TODO/roadmap links and a matching milestone", step.ID)
	}
	return nil
}

func validateStepRequirements(step Step, requirements map[string]bool) error {
	if !validCount(len(step.RequirementIDs), MaxDependencies) {
		return fmt.Errorf("step %s requires 1..%d requirement references", step.ID, MaxDependencies)
	}
	if err := validateIDs("step requirement", step.ID, step.RequirementIDs, MaxDependencies); err != nil {
		return err
	}
	for _, requirementID := range step.RequirementIDs {
		if !requirements[requirementID] {
			return fmt.Errorf("step %s references unknown requirement %s", step.ID, requirementID)
		}
	}
	return nil
}

func validateOutputs(stepID string, list []ExpectedOutput, globallySeen map[string]bool) error {
	if !validCount(len(list), MaxExpectedOutputs) {
		return fmt.Errorf("step %s requires 1..%d expected outputs", stepID, MaxExpectedOutputs)
	}
	for _, output := range list {
		if !validID(output.ID) || !validText(output.Description) || globallySeen[output.ID] {
			return fmt.Errorf("step %s has invalid or duplicate expected output %q", stepID, output.ID)
		}
		globallySeen[output.ID] = true
	}
	return nil
}

func validateAcceptance(owner string, acceptance Acceptance) error {
	ordered := []struct {
		kind  string
		cases []string
	}{
		{"positive", acceptance.Positive}, {"negative", acceptance.Negative}, {"boundary", acceptance.Boundary},
	}
	for _, item := range ordered {
		if err := validateTextList(owner+" "+item.kind+" acceptance", item.cases, 1, MaxAcceptanceCases); err != nil {
			return err
		}
	}
	return nil
}

func validateTextList(name string, values []string, minimum, maximum int) error {
	if len(values) < minimum || len(values) > maximum {
		return fmt.Errorf("%s requires %d..%d entries", name, minimum, maximum)
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !validText(value) || seen[value] {
			return fmt.Errorf("%s contains invalid or duplicate text", name)
		}
		seen[value] = true
	}
	return nil
}
