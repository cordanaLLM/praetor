// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/templates"
)

// renderedTemplate renders one embedded template as flavor apply does for acme/widget.
func renderedTemplate(t *testing.T, source string) string {
	t.Helper()
	body, err := templates.RenderFile(source, templates.Context{RepoName: "widget", Owner: "acme"})
	if err != nil {
		t.Fatalf("render %s: %v", source, err)
	}
	return body
}

// workflowPaths returns the paths of the scaffolded workflows.
func workflowPaths(workflows []scaffoldedWorkflow) []string {
	paths := make([]string, 0, len(workflows))
	for _, workflow := range workflows {
		paths = append(paths, workflow.path)
	}
	return paths
}

// ciSession is a session over a Go library checkout, its go.mod tracked, with the documentation
// facet declared.
func ciSession(t *testing.T, declined ...string) *adoptSession {
	t.Helper()
	repo := newTestRepo(t, "widget")
	trackGoModule(t, repo, "go.mod")
	mustWrite(t, filepath.Join(repo, "internal", "widget.go"), "package widget\n")
	s := identitySession(t, repo)
	s.declined = declined
	return s
}

// TestScaffoldedWorkflowsReadWhatAdoptionWrites: rule 5 names the workflows the managed family
// and flavor steps write, with the commands their bodies run. Positive: a documented Go library
// with a public API contract gets the documentation gate, the API compatibility gate and the
// flavor's ci.yml. Negative: a declined flavor step and a
// repository-owned ci.yml are not adoption's, so neither is named. Boundary: with no facet and
// no flavor nothing is scaffolded and rule 5 says the gates run local only.
func TestScaffoldedWorkflowsReadWhatAdoptionWrites(t *testing.T) {
	s := ciSession(t)
	families, err := EnabledManagedFamilies(t.Context(), s.repoPath, []string{"docs:seo-portal", "api:public-contract"})
	if err != nil {
		t.Fatal(err)
	}
	workflows, err := s.scaffoldedWorkflows(t.Context(), families)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{DocumentationWorkflowFile, APICompatibilityWorkflowFile, ".github/workflows/ci.yml"}
	if got := workflowPaths(workflows); !slices.Equal(got, want) {
		t.Fatalf("scaffolded workflows = %q, want %q", got, want)
	}
	if !slices.Equal(runLabels(workflows[1].runs), []string{`go run tools/apicompat/gate/main.go -base="$BASE"`}) {
		t.Errorf("%s runs = %+v", APICompatibilityWorkflowFile, workflows[1].runs)
	}
	if !slices.Equal(runLabels(workflows[2].runs), []string{"go vet ./...", "go test -race ./..."}) {
		t.Errorf("ci.yml runs = %+v", workflows[2].runs)
	}

	declined, err := ciSession(t, "working-dir-and-flavor").scaffoldedWorkflows(t.Context(), nil)
	if err != nil || len(declined) != 0 {
		t.Errorf("declined flavor step still scaffolds %q (%v)", workflowPaths(declined), err)
	}
	owned := ciSession(t)
	mustWrite(t, filepath.Join(owned.repoPath, ".github", "workflows", "ci.yml"), "name: Own\non: push\njobs:\n  own:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make ci\n")
	if got, err := owned.scaffoldedWorkflows(t.Context(), nil); err != nil || len(got) != 0 {
		t.Errorf("repository-owned ci.yml named as scaffolded: %q (%v)", workflowPaths(got), err)
	}

	bare := identitySession(t, newTestRepo(t, "bare"))
	none, err := bare.scaffoldedWorkflows(t.Context(), nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("bare repository scaffolds %q (%v)", workflowPaths(none), err)
	}
	if claim := ciClaim(none); claim != "Adoption scaffolds no CI workflow: `praetorctl` gates run local only.\n\n" {
		t.Errorf("claim without CI = %q", claim)
	}
}

// runLabels returns the label of each run, in order.
func runLabels(runs []forge.WorkflowRun) []string {
	labels := make([]string, 0, len(runs))
	for _, run := range runs {
		labels = append(labels, run.Label)
	}
	return labels
}

// command is a one-line `run:` step.
func command(script string) forge.WorkflowRun {
	return forge.WorkflowRun{Label: script, Script: script}
}

// TestCIClaimFollowsWhatWorkflowsRun: the no-server-side-gate sentence holds only while no
// scaffolded workflow runs praetorctl, under either CLI name and on any line of a multi-line
// script; a multi-line step reads as a named step, never as a command; a workflow of actions only
// runs no command.
func TestCIClaimFollowsWhatWorkflowsRun(t *testing.T) {
	toolchain := forge.WorkflowRun{Label: "Install toolchain", Step: true, Script: "rustup toolchain install stable\nrustup default stable"}
	plain := ciClaim([]scaffoldedWorkflow{{path: "ci.yml", runs: []forge.WorkflowRun{toolchain, command("cargo test")}}})
	if plain != "Adoption adds no server-side `praetorctl` gate run. Scaffolded CI: `ci.yml` runs step `Install toolchain`, `cargo test`.\n\n" {
		t.Errorf("plain claim = %q", plain)
	}
	hidden := forge.WorkflowRun{Label: "Gate", Step: true, Script: "set -eu\npraetorctl audit"}
	for _, run := range []forge.WorkflowRun{command("praetorctl audit"), command("./bin/standardsctl audit"), command(`C:\tools\praetorctl.exe audit`), hidden} {
		claim := ciClaim([]scaffoldedWorkflow{{path: "ci.yml", runs: []forge.WorkflowRun{run}}})
		if strings.Contains(claim, "no server-side") || !strings.HasPrefix(claim, "Scaffolded CI: ") {
			t.Errorf("%q: claim keeps the no-gate sentence: %q", run.Script, claim)
		}
	}
	if claim := ciClaim([]scaffoldedWorkflow{{path: "pin.yml"}}); !strings.Contains(claim, "`pin.yml` runs no command") {
		t.Errorf("actions-only workflow claim = %q", claim)
	}
}

// TestNoScaffoldedWorkflowRunsPraetor: no workflow any flavor or the documentation gate scaffolds
// runs praetorctl, so rule 5's "no server-side `praetorctl` gate run" holds for every adoption.
// A template that starts running a gate fails here and makes ciClaim drop the sentence.
func TestNoScaffoldedWorkflowRunsPraetor(t *testing.T) {
	bodies := map[string]string{DocumentationWorkflowFile: DocumentationWorkflow()}
	for _, flv := range flavor.List() {
		for _, item := range flv.RequiredTemplates() {
			if !strings.HasPrefix(item.Path, ".github/workflows/") || item.Producer != "" {
				continue
			}
			body := ""
			switch {
			case item.ContentFunc != nil:
				body = item.ContentFunc("widget", "acme")
			case item.Source != "":
				body = renderedTemplate(t, item.Source)
			}
			bodies[flv.Name()+":"+item.Path] = body
		}
	}
	if len(bodies) < 2 {
		t.Fatalf("fixture precondition: only %d workflow bodies found", len(bodies))
	}
	for name, body := range bodies {
		runs, err := forge.WorkflowRuns([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if runsPraetor(runs) {
			t.Errorf("%s runs praetorctl: %+v", name, runs)
		}
	}
}
