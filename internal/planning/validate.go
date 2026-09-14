package planning

import (
	"context"
	"fmt"
)

func validateDraft(ctx context.Context, draft *Draft) ([]string, []string, error) {
	if err := validateDraftShape(draft); err != nil {
		return nil, nil, err
	}
	sources, err := validateSources(ctx, draft.Sources)
	if err != nil {
		return nil, nil, err
	}
	requirements, err := validateRequirements(ctx, draft.Requirements, sources)
	if err != nil {
		return nil, nil, err
	}
	milestones, milestoneNodes, err := validateMilestones(ctx, draft.Milestones)
	if err != nil {
		return nil, nil, err
	}
	milestoneOrder, err := graphOrder(ctx, "milestone", milestoneNodes, MaxMilestones)
	if err != nil {
		return nil, nil, err
	}
	prerequisites := milestonePrerequisites(draft.Milestones)
	stepNodes, err := validateSteps(ctx, draft.Steps, requirements, milestones, milestoneOrder, prerequisites)
	if err != nil {
		return nil, nil, err
	}
	stepOrder, err := graphOrder(ctx, "step", stepNodes, MaxSteps)
	if err != nil {
		return nil, nil, err
	}
	return milestoneOrder, stepOrder, nil
}

func validateDraftShape(draft *Draft) error {
	if draft.SchemaVersion != SchemaVersion || !validID(draft.ID) {
		return fmt.Errorf("schema_version 1 and stable draft id required")
	}
	if err := validateProject(draft.Project); err != nil {
		return err
	}
	counts := []struct{ count, maximum int }{
		{len(draft.Sources), MaxSources}, {len(draft.Requirements), MaxRequirements},
		{len(draft.Milestones), MaxMilestones}, {len(draft.Steps), MaxSteps},
	}
	for _, item := range counts {
		if !validCount(item.count, item.maximum) {
			return fmt.Errorf("planning graph count limit exceeded or required collection empty")
		}
	}
	return nil
}

func validateProject(project Project) error {
	if !validID(project.ID) || !validText(project.Title) || !validText(project.Repository) || !validText(project.Revision) {
		return fmt.Errorf("project requires stable id, title, repository and revision")
	}
	return nil
}

func validateSources(ctx context.Context, list []Source) (map[string]Source, error) {
	allowed := map[string]bool{"research": true, "wish": true, "planning": true,
		"backfeed": true, "finding": true, "manager-demand": true}
	seen := make(map[string]Source, len(list))
	for _, source := range list {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validID(source.ID) || !allowed[source.Kind] || !validText(source.Locator) || !validText(source.Revision) ||
			!validSHA256(source.SHA256) || source.Provenance != "caller_asserted" || source.Verified {
			return nil, fmt.Errorf("source %q requires bounded caller-asserted unverified provenance", source.ID)
		}
		if _, exists := seen[source.ID]; exists {
			return nil, fmt.Errorf("duplicate source id %s", source.ID)
		}
		seen[source.ID] = source
	}
	return seen, nil
}

func validateRequirements(ctx context.Context, list []Requirement, sources map[string]Source) (map[string]bool, error) {
	seen := make(map[string]bool, len(list))
	for _, requirement := range list {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validID(requirement.ID) || !validText(requirement.Detail) || requirement.Disposition != "proposed" ||
			!validCount(len(requirement.SourceRefs), MaxCitations) || seen[requirement.ID] {
			return nil, fmt.Errorf("invalid or duplicate proposed requirement %q", requirement.ID)
		}
		if err := validateCitations(requirement.ID, requirement.SourceRefs, sources); err != nil {
			return nil, err
		}
		seen[requirement.ID] = true
	}
	return seen, nil
}

func validateCitations(requirementID string, refs []Citation, sources map[string]Source) error {
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		source, exists := sources[ref.SourceID]
		key := ref.SourceID + "\x00" + ref.SourceSHA256 + "\x00" + ref.Quote
		if !exists || source.SHA256 != ref.SourceSHA256 || !validText(ref.Quote) || seen[key] {
			return fmt.Errorf("requirement %s has invalid, duplicate, or unknown source reference", requirementID)
		}
		seen[key] = true
	}
	return nil
}

func validateMilestones(ctx context.Context, list []Milestone) (map[string]bool, []graphNode, error) {
	seen := make(map[string]bool, len(list))
	nodes := make([]graphNode, 0, len(list))
	for _, milestone := range list {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !validID(milestone.ID) || !validText(milestone.Title) || !validText(milestone.Outcome) || seen[milestone.ID] {
			return nil, nil, fmt.Errorf("invalid or duplicate milestone %q", milestone.ID)
		}
		if err := validateAcceptance("milestone "+milestone.ID, milestone.Acceptance); err != nil {
			return nil, nil, err
		}
		if err := validateIDs("milestone dependency", milestone.ID, milestone.DependsOn, MaxDependencies); err != nil {
			return nil, nil, err
		}
		seen[milestone.ID] = true
		nodes = append(nodes, graphNode{id: milestone.ID, dependencies: milestone.DependsOn})
	}
	if err := validateReferences("milestone dependency", nodes, seen); err != nil {
		return nil, nil, err
	}
	return seen, nodes, nil
}
