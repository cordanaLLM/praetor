// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxScaffoldedWorkflows bounds the workflows one harness reads (HISS-02).
const maxScaffoldedWorkflows = 64

// scaffoldedWorkflow is one CI workflow this adoption run leaves as its own rendering, and what
// it runs as read from the file (forge.WorkflowRuns), with the actions its steps use
// (forge.WorkflowActions), named for a workflow that runs no command of its own.
type scaffoldedWorkflow struct {
	path    string
	runs    []forge.WorkflowRun
	actions []string
}

// scaffoldedWorkflows lists the CI workflows this run leaves as adoption's rendering
// (adoptedWorkflowFiles) with what each runs. The harness step runs before the managed family
// and flavor steps, so it reads what they will write; what each workflow runs is read from its
// body, never restated, so rule 5 cannot drift from the files (BUG-804).
func (s *adoptSession) scaffoldedWorkflows(ctx context.Context, families []managedasset.Family) ([]scaffoldedWorkflow, error) {
	files, err := s.adoptedWorkflowFiles(ctx, families)
	if err != nil {
		return nil, err
	}
	return workflowRuns(files)
}

// adoptedWorkflowFiles lists the CI workflows this run leaves as adoption's rendering, with
// their bodies: the hosted workflow of every managed asset family the active facets enable
// (families), which the family's step writes whenever its facet is declared (the step cannot be
// declined; only the facet turns it off), the hosted REUSE gate of a repository that declares
// REUSE (plannedReuseWorkflow), and the workflows of the detected flavor that flavor apply
// leaves as its own (plannedFlavorWorkflows), unless the flavor step is declined. The harness
// names what they run and the actionlint-labels step declares the runner labels they need, both
// from this one list.
func (s *adoptSession) adoptedWorkflowFiles(ctx context.Context, families []managedasset.Family) ([]flavor.PlannedTemplate, error) {
	reuse, err := s.plannedReuseWorkflow(ctx)
	if err != nil {
		return nil, err
	}
	planned, err := s.plannedFlavorWorkflows(ctx)
	if err != nil {
		return nil, err
	}
	hosted, err := familyWorkflows(ctx, s.repoPath, families)
	if err != nil {
		return nil, err
	}
	return append(append(hosted, reuse...), planned...), nil
}

// familyWorkflows returns the hosted workflow of each of families that has one, in their order,
// with its locked body as the family's step writes it to its WorkflowFile: rendered for the
// default branch of the repository at repoPath (FamilyForRepository).
func familyWorkflows(ctx context.Context, repoPath string, families []managedasset.Family) ([]flavor.PlannedTemplate, error) {
	var files []flavor.PlannedTemplate
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		if families[index].WorkflowFile == "" {
			continue
		}
		rendered, err := FamilyForRepository(ctx, repoPath, families[index])
		if err != nil {
			return nil, err
		}
		files = append(files, flavor.PlannedTemplate{Path: rendered.WorkflowFile, Content: rendered.Workflow})
	}
	return files, nil
}

// plannedFlavorWorkflows lists the workflows the flavor step leaves as the own rendering of the
// flavor it applies, the one of the profile this adoption records (flavor.PlannedWorkflows under
// s.arch, as applyDetectedFlavor resolves it), or none when adoption.decline names the step. The
// harness names what they run from it, and a dry run derives the ruleset's status checks from
// it, since a dry run only plans the flavor and writes no workflow.
func (s *adoptSession) plannedFlavorWorkflows(ctx context.Context) ([]flavor.PlannedTemplate, error) {
	declined, err := ArtifactDeclined(s.declined, "working-dir-and-flavor")
	if err != nil {
		return nil, fmt.Errorf("resolve adoption.decline for the flavor workflows: %w", err)
	}
	if declined {
		return nil, nil
	}
	planned, err := flavor.PlannedWorkflows(ctx, s.repoPath, s.arch)
	if err != nil {
		return nil, fmt.Errorf("plan flavor workflows: %w", err)
	}
	return planned, nil
}

// workflowRuns reads what each workflow body runs.
func workflowRuns(files []flavor.PlannedTemplate) ([]scaffoldedWorkflow, error) {
	workflows := make([]scaffoldedWorkflow, 0, len(files))
	for i := 0; i < len(files) && i < maxScaffoldedWorkflows; i++ {
		runs, err := forge.WorkflowRuns([]byte(files[i].Content))
		if err != nil {
			return nil, fmt.Errorf("read scaffolded workflow %s: %w", files[i].Path, err)
		}
		actions, err := forge.WorkflowActions([]byte(files[i].Content))
		if err != nil {
			return nil, fmt.Errorf("read the actions of scaffolded workflow %s: %w", files[i].Path, err)
		}
		workflows = append(workflows, scaffoldedWorkflow{path: files[i].Path, runs: runs, actions: actions})
	}
	return workflows, nil
}

// ciClaim completes rule 5 with the server side. Adoption installs no CI job that runs a
// `praetorctl` gate, so those gates run only in the local hooks and targets; each workflow it
// does scaffold is named with what it runs. Should a scaffolded workflow ever run praetorctl,
// the claim of no server-side gate run is dropped instead of stated falsely.
func ciClaim(workflows []scaffoldedWorkflow) string {
	if len(workflows) == 0 {
		return "Adoption scaffolds no CI workflow: `praetorctl` gates run local only.\n\n"
	}
	parts := make([]string, 0, len(workflows))
	praetorInCI := false
	for _, workflow := range workflows {
		parts = append(parts, "`"+workflow.path+"` runs "+workflowClaim(workflow))
		praetorInCI = praetorInCI || runsPraetor(workflow.runs)
	}
	lead := "Adoption adds no server-side `praetorctl` gate run. Scaffolded CI: "
	if praetorInCI {
		lead = "Scaffolded CI: "
	}
	return lead + strings.Join(parts, "; ") + ".\n\n"
}

// workflowClaim renders what one workflow runs: its commands (codeList), or, for a workflow
// whose steps run none of their own, such as the hosted REUSE gate, the actions they use.
func workflowClaim(workflow scaffoldedWorkflow) string {
	if len(workflow.runs) > 0 || len(workflow.actions) == 0 {
		return codeList(workflow.runs)
	}
	spans := make([]string, 0, len(workflow.actions))
	for _, action := range workflow.actions {
		spans = append(spans, "`"+strings.ReplaceAll(action, "`", "'")+"`")
	}
	return "no command of its own, only the actions " + strings.Join(spans, ", ")
}

// codeList renders what the steps run as a comma-separated list of code spans, a multi-line
// script's name labelled as a step so it never reads as a command, or "no command".
func codeList(runs []forge.WorkflowRun) string {
	if len(runs) == 0 {
		return "no command"
	}
	spans := make([]string, 0, len(runs))
	for _, run := range runs {
		span := "`" + strings.ReplaceAll(run.Label, "`", "'") + "`"
		if run.Step {
			span = "step " + span
		}
		spans = append(spans, span)
	}
	return strings.Join(spans, ", ")
}

// runsPraetor reports whether any line of any step's script invokes the praetor CLI under either
// of its names, a multi-line script's later lines included.
func runsPraetor(runs []forge.WorkflowRun) bool {
	for _, run := range runs {
		if slices.ContainsFunc(strings.Fields(run.Script), isPraetorCommand) {
			return true
		}
	}
	return false
}

// isPraetorCommand reports whether token names the praetor CLI, by path or bare, .exe or not.
func isPraetorCommand(token string) bool {
	name := strings.TrimSuffix(path.Base(strings.ReplaceAll(token, `\`, "/")), ".exe")
	return name == util.PraetorCLI || name == util.LegacyCLI
}
