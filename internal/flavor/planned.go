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

	"github.com/cordanaLLM/praetor/internal/util"
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

// PlannedWorkflows returns the CI workflows of the flavor Resolve names under repoPath that
// ApplyFlavor without --force leaves as the flavor's own rendering: an absent workflow it
// writes, and a present one canonically equal to that rendering (an earlier apply's). A present
// workflow that differs is the repository's own, and one a requirement or an alternative
// withholds is never written, so neither is listed; a repository no flavor matches has none.
// Adoption names what scaffolded CI runs from this list, so it names only workflows the flavor
// owns. The identity lookup that renders the bodies runs under ctx.
func PlannedWorkflows(ctx context.Context, repoPath string) ([]PlannedTemplate, error) {
	if ctx == nil {
		return nil, errors.New("planned workflows require a context")
	}
	name, err := plannedFlavorName(repoPath)
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
	items := flv.RequiredTemplates()
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

// plannedFlavorName resolves the flavor ApplyFlavor scaffolds for repoPath, as Resolve does for
// apply and audit. A repository no flavor matches, or whose profile has none, yields an empty
// name and no error, like the skipped flavor step in adoption; any other failure is returned.
func plannedFlavorName(repoPath string) (string, error) {
	name, err := Resolve(repoPath)
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
// in the same order: path containment and cover (templateDisposition), the file already there
// (readTemplateTarget), and a withheld body (templateWithheld).
func plannedBody(ctx context.Context, repoPath string, tmpl TemplateItem, repoName, owner string) (string, bool, error) {
	disposition, _, err := templateDisposition(repoPath, tmpl, false)
	if err != nil {
		return "", false, err
	}
	covered := disposition == templateCovered
	body, err := templateContent(tmpl, repoName, owner)
	if err != nil {
		return "", false, err
	}
	target, err := readTemplateTarget(ctx, filepath.Join(repoPath, tmpl.Path), tmpl.Path, false)
	if err != nil {
		return "", false, err
	}
	if target.exists {
		same, err := util.CanonicalTextEquivalent(target.before, []byte(body))
		return body, err == nil && same, nil
	}
	if covered {
		return "", false, nil
	}
	if outcome, _ := templateWithheld(ctx, repoPath, tmpl); outcome != templateCreated {
		return "", false, nil
	}
	return body, true, nil
}
