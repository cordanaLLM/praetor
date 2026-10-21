package forge

import (
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const adoptCommentWorkflowFile = "adopt-comment.yml"

var adoptEntryPoints = [2]struct{ file, event, job string }{
	{adoptWorkflowFile, "workflow_dispatch", adoptDispatchJob},
	{adoptCommentWorkflowFile, issueCommentEvent, adoptCommentJob},
}

// The cache-poisoning query attributes every file trigger to every job, regardless of job-level if.
// Keep the comment entry point in a file whose only trigger is issue_comment.
func adoptEntryPointGap(spec workflowSpec, event, job string) string {
	triggers := workflowTriggers(&spec.On)
	if len(triggers) != 1 || triggers[0].name != event {
		return "workflow must declare only " + event
	}
	if !slices.Equal(sortedJobIDs(spec.Jobs), []string{job}) {
		return "workflow must hold only job " + job
	}
	if spec.Permissions.Kind != yaml.MappingNode || len(spec.Permissions.Content) != 0 {
		return "workflow must declare permissions: {}"
	}
	return ""
}

func TestAdoptWorkflow_Positive_EntryPointsHaveSeparateFiles(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	for _, entry := range adoptEntryPoints {
		t.Run(entry.file, func(t *testing.T) {
			var spec workflowSpec
			if err := yaml.Unmarshal(workflows[entry.file], &spec); err != nil {
				t.Fatal(err)
			}
			if gap := adoptEntryPointGap(spec, entry.event, entry.job); gap != "" {
				t.Fatal(gap)
			}
		})
	}
}

// This fixture retains the old file's two triggers and comment job condition. The condition
// protects issue_comment, but code scanning also attributes workflow_dispatch to this job.
const mixedAdoptCommentWorkflow = `
on:
  workflow_dispatch:
  issue_comment:
    types: [created]
permissions: {}
jobs:
  adopt-comment:
    if: >-
      github.event_name == 'issue_comment' &&
      github.event.issue.pull_request &&
      (contains(github.event.comment.body, '/adopt') || contains(github.event.comment.body, '/dogfood')) &&
      contains(fromJSON('["OWNER","MEMBER","COLLABORATOR"]'), github.event.comment.author_association)
    permissions:
      contents: read
      pull-requests: read
    runs-on: ubuntu-26.04
    steps:
      - uses: actions/checkout@v7
        with:
          ref: ${{ steps.pr-head.outputs.sha }}
      - uses: ./.github/actions/praetor-adopt
`

func TestAdoptWorkflow_Negative_RejectsMixedCommentTriggers(t *testing.T) {
	var spec workflowSpec
	if err := yaml.Unmarshal([]byte(mixedAdoptCommentWorkflow), &spec); err != nil {
		t.Fatal(err)
	}
	if gap := adoptEntryPointGap(spec, issueCommentEvent, adoptCommentJob); gap != "workflow must declare only issue_comment" {
		t.Fatalf("mixed-trigger fixture: gap = %q", gap)
	}
}

func TestAdoptWorkflow_Boundary_EntryPointShape(t *testing.T) {
	commentOnly := strings.Replace(mixedAdoptCommentWorkflow, "  workflow_dispatch:\n", "", 1)
	cases := []struct{ name, body, want string }{
		{"comment only", commentOnly, ""},
		{"no trigger", strings.Replace(commentOnly, "on:", "disabled:", 1), "workflow must declare only issue_comment"},
		{"extra job", commentOnly + "  adopt:\n    runs-on: ubuntu-26.04\n", "workflow must hold only job adopt-comment"},
		{"inherited write", strings.Replace(commentOnly, "permissions: {}", "permissions: write-all", 1), "workflow must declare permissions: {}"},
		{"implicit permissions", strings.Replace(commentOnly, "permissions: {}\n", "", 1), "workflow must declare permissions: {}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var spec workflowSpec
			if err := yaml.Unmarshal([]byte(tc.body), &spec); err != nil {
				t.Fatal(err)
			}
			if gap := adoptEntryPointGap(spec, issueCommentEvent, adoptCommentJob); gap != tc.want {
				t.Fatalf("gap = %q, want %q", gap, tc.want)
			}
		})
	}
}
