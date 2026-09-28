// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// workflowDir holds the CI workflows a flavor scaffolds.
	workflowDir = ".github/workflows/"
	// maxPlannedTemplates bounds the templates one plan reads (HISS-02).
	maxPlannedTemplates = 64
)

// PlannedTemplate is one template of a flavor and the body an apply leaves at its path.
type PlannedTemplate struct {
	Path    string
	Content string
}

// PlannedWorkflows returns the CI workflows of the flavor of profile that matches repoPath
// (ResolveForProfile, the flavor adoption applies for the profile it records) that ApplyFlavor
// without --force leaves as the flavor's own rendering: an absent workflow it writes, and a
// present one canonically equal to that rendering (an earlier apply's). A present workflow that
// differs is the repository's own, and one a requirement or an alternative withholds is never
// written, so neither is listed; a profile with no flavor, or none that matches, has none.
// Adoption names what scaffolded CI runs from this list, so it names only workflows the flavor
// owns, and a dry run derives the ruleset's status checks from it, since a dry run applies no
// flavor. The identity lookup that renders the bodies runs under ctx.
func PlannedWorkflows(ctx context.Context, repoPath, profile string) ([]PlannedTemplate, error) {
	if ctx == nil {
		return nil, errors.New("planned workflows require a context")
	}
	name, err := plannedFlavorName(repoPath, profile)
	if name == "" {
		return nil, err
	}
	flv, err := Get(name)
	if err != nil {
		return nil, fmt.Errorf("planned workflows: %w", err)
	}
	owner, repoName, err := flavorIdentity(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	return plannedWorkflowBodies(ctx, repoPath, flv.RequiredTemplates(), repoName, owner)
}

// plannedWorkflowBodies lists, of items, the CI workflows without a producer whose rendering an
// apply without --force leaves at their path (plannedBody), with that rendering.
func plannedWorkflowBodies(ctx context.Context, repoPath string, items []TemplateItem, repoName, owner string) ([]PlannedTemplate, error) {
	var planned []PlannedTemplate
	for i := 0; i < len(items) && i < maxPlannedTemplates; i++ {
		if !strings.HasPrefix(items[i].Path, workflowDir) || items[i].Producer != "" {
			continue
		}
		body, owned, err := plannedBody(ctx, repoPath, items[i], repoName, owner)
		if err != nil {
			return nil, err
		}
		if owned {
			planned = append(planned, PlannedTemplate{Path: items[i].Path, Content: body})
		}
	}
	return planned, nil
}

// plannedFlavorName resolves the flavor of profile ApplyFlavor scaffolds for repoPath, as
// adoption's flavor step does (ResolveForProfile). A profile with no flavor, or none that
// matches, yields an empty name and no error, like the skipped flavor step in adoption; any
// other failure is returned.
func plannedFlavorName(repoPath, profile string) (string, error) {
	name, err := ResolveForProfile(repoPath, profile)
	switch {
	case errors.Is(err, ErrNoFlavorMatched), errors.Is(err, ErrFlavorNotApplicable):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("planned workflows: %w", err)
	}
	return name, nil
}

// plannedBody renders one template and reports whether an apply without --force leaves that
// rendering at its path. It asks the questions scaffoldTemplate asks, through the same helpers,
// in the same order: path containment and cover (templateDisposition), a withheld body and the
// repository facts the body renders against (templateWithheld), and the file already there
// (readTemplateTarget). A present file canonically equal to the rendering is an earlier apply's,
// and one holding an earlier text of the template (TemplateItem.Prior) is refreshed to it
// (planTargetWrite).
func plannedBody(ctx context.Context, repoPath string, tmpl TemplateItem, repoName, owner string) (string, bool, error) {
	disposition, _, err := templateDisposition(repoPath, tmpl, false)
	if err != nil {
		return "", false, err
	}
	outcome, _, vars := templateWithheld(ctx, repoPath, tmpl)
	vars.RepoName, vars.Owner = repoName, owner
	body, err := templateContent(tmpl, vars)
	if err != nil {
		return "", false, err
	}
	target, err := readTemplateTarget(ctx, filepath.Join(repoPath, tmpl.Path), tmpl.Path, false)
	if err != nil {
		return "", false, err
	}
	if target.exists {
		_, write := planTargetWrite(target, []byte(body), tmpl.Prior)
		return body, write == targetUnchanged || write == targetRefreshed, nil
	}
	if disposition == templateCovered || outcome != templateCreated {
		return "", false, nil
	}
	return body, true, nil
}
