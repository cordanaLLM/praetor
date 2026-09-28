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
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxScaffoldedWorkflows bounds the workflows one harness reads (HISS-02).
const maxScaffoldedWorkflows = 64

// scaffoldedWorkflow is one CI workflow this adoption run leaves as its own rendering, and what
// it runs as read from the file (forge.WorkflowRuns).
type scaffoldedWorkflow struct {
	path string
	runs []forge.WorkflowRun
}

// scaffoldedWorkflows lists the CI workflows this run leaves as adoption's rendering: the
// documentation gate's workflow, which the documentation step writes whenever the facet is
// declared (the step cannot be declined; only the facet turns it off), and the workflows of the
// detected flavor that flavor apply leaves as its own (flavor.PlannedWorkflows), unless the
// flavor step is declined. The harness step runs before
// both steps, so it reads what they will write; what each workflow runs is read from its body,
// never restated, so rule 5 cannot drift from the files (BUG-804).
func (s *adoptSession) scaffoldedWorkflows(ctx context.Context, docsGate bool) ([]scaffoldedWorkflow, error) {
	var files []flavor.PlannedTemplate
	if docsGate {
		files = append(files, flavor.PlannedTemplate{Path: DocumentationWorkflowFile, Content: DocumentationWorkflow()})
	}
	flavorDeclined, err := ArtifactDeclined(s.declined, "working-dir-and-flavor")
	if err != nil {
		return nil, fmt.Errorf("resolve adoption.decline for the harness: %w", err)
	}
	if !flavorDeclined {
		planned, err := flavor.PlannedWorkflows(ctx, s.repoPath)
		if err != nil {
			return nil, fmt.Errorf("plan flavor workflows for the harness: %w", err)
		}
		files = append(files, planned...)
	}
	return workflowRuns(files)
}

// workflowRuns reads what each workflow body runs.
func workflowRuns(files []flavor.PlannedTemplate) ([]scaffoldedWorkflow, error) {
	workflows := make([]scaffoldedWorkflow, 0, len(files))
	for i := 0; i < len(files) && i < maxScaffoldedWorkflows; i++ {
		runs, err := forge.WorkflowRuns([]byte(files[i].Content))
		if err != nil {
			return nil, fmt.Errorf("read scaffolded workflow %s: %w", files[i].Path, err)
		}
		workflows = append(workflows, scaffoldedWorkflow{path: files[i].Path, runs: runs})
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
		parts = append(parts, "`"+workflow.path+"` runs "+codeList(workflow.runs))
		praetorInCI = praetorInCI || runsPraetor(workflow.runs)
	}
	lead := "Adoption adds no server-side `praetorctl` gate run. Scaffolded CI: "
	if praetorInCI {
		lead = "Scaffolded CI: "
	}
	return lead + strings.Join(parts, "; ") + ".\n\n"
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
