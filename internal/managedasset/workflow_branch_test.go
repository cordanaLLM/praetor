// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"gopkg.in/yaml.v3"
)

// branchFixtureFamily is fixtureFamily with a workflow that names WorkflowBranch on its push
// trigger, the shape ForBranch renders.
func branchFixtureFamily() Family {
	family := fixtureFamily()
	family.Workflow = "name: Fixture Gate\n'on':\n  push:\n    branches: ['main']\njobs: {}\n"
	return family
}

// Positive: every hosted family names its default branch once and renders another one, the
// rendering reporting its branch; a rendering renders again from itself. Negative: a branch
// config.ValidBranchName refuses is an error. Boundary: a workflow without a push branch line,
// and a family without a workflow, are returned unchanged, and a branch YAML reads as a number
// stays quoted.
func TestForBranch(t *testing.T) {
	for _, family := range hostedFamilies(t) {
		if !family.BranchDependent() || family.Branch() != WorkflowBranch {
			t.Fatalf("%s: workflow does not name %s on its push trigger once", family.Name, WorkflowBranch)
		}
		develop, err := family.ForBranch("develop")
		if err != nil || develop.Branch() != "develop" || !strings.Contains(develop.Workflow, "\n    branches: ['develop']\n") ||
			strings.Contains(develop.Workflow, "['main']") {
			t.Fatalf("%s: develop rendering = %v, %q", family.Name, err, develop.Workflow)
		}
		back, err := develop.ForBranch(WorkflowBranch)
		if err != nil || back.Workflow != family.Workflow {
			t.Fatalf("%s: rendering main from the develop rendering differs (%v)", family.Name, err)
		}
		canonical, _, err := develop.Canonical(develop.WorkflowFile)
		if err != nil || string(canonical) != develop.Workflow {
			t.Fatalf("%s: Canonical does not return the rendering (%v)", family.Name, err)
		}
	}
	for _, branch := range []string{"", "a b", "it's", "feature/..x", "-lead"} {
		if _, err := branchFixtureFamily().ForBranch(branch); err == nil {
			t.Fatalf("branch %q rendered", branch)
		}
	}
	number, err := branchFixtureFamily().ForBranch("1.0")
	if err != nil || !strings.Contains(number.Workflow, "branches: ['1.0']") {
		t.Fatalf("numeric branch rendering = %v, %q", err, number.Workflow)
	}
	unchanged, err := fixtureFamily().ForBranch("develop")
	if err != nil || unchanged.Workflow != fixtureFamily().Workflow || unchanged.BranchDependent() {
		t.Fatalf("a workflow without a push branch line changed: %v, %q", err, unchanged.Workflow)
	}
	bare := fixtureFamily()
	bare.WorkflowFile, bare.StatusContext, bare.Workflow, bare.WorkflowNoun = "", "", "", ""
	if got, err := bare.ForBranch("a b"); err != nil || got.BranchDependent() {
		t.Fatalf("a family without a workflow: %v", err)
	}
}

// Positive: the workflow rendered for another default branch is a prior rendering, LF or CRLF,
// so adoption refreshes it and a disabled facet removes it, and OtherBranch names that branch.
// Negative: an edited rendering, a rendering naming a branch nothing would render, and a
// rendering at another path are neither. Boundary: the family's own rendering is canonical, never
// prior and names no other branch, and Validate accepts it.
func TestOtherBranchRenderingIsPrior(t *testing.T) {
	family := branchFixtureFamily()
	develop, err := family.ForBranch("develop")
	if err != nil {
		t.Fatal(err)
	}
	if known, crlf := family.PriorRendering(family.WorkflowFile, []byte(develop.Workflow)); !known || crlf {
		t.Fatalf("develop rendering: known=%v crlf=%v", known, crlf)
	}
	crlfText := strings.ReplaceAll(develop.Workflow, "\n", "\r\n")
	if known, crlf := family.PriorRendering(family.WorkflowFile, []byte(crlfText)); !known || !crlf {
		t.Fatalf("CRLF develop rendering: known=%v crlf=%v", known, crlf)
	}
	for _, text := range []string{develop.Workflow, crlfText} {
		if branch, found := family.OtherBranch(family.WorkflowFile, []byte(text)); !found || branch != "develop" {
			t.Fatalf("OtherBranch of the develop rendering = %q, %v", branch, found)
		}
	}
	for name, text := range map[string]string{
		"edited":       develop.Workflow + "# edited\n",
		"invalid name": strings.Replace(develop.Workflow, "['develop']", "['a b']", 1),
		"unclosed":     strings.Replace(develop.Workflow, "['develop']", "['develop", 1),
		"mixed ends":   strings.Replace(crlfText, "\r\n", "\n", 1),
	} {
		_, other := family.OtherBranch(family.WorkflowFile, []byte(text))
		if other || family.PriorText(family.WorkflowFile, []byte(text)) {
			t.Fatalf("%s rendering is a prior text or names another branch", name)
		}
	}
	if _, other := family.OtherBranch("tools/fixture/core.mjs", []byte(develop.Workflow)); other ||
		family.PriorText("tools/fixture/core.mjs", []byte(develop.Workflow)) {
		t.Fatal("a rendering at another path is a prior text or names another branch")
	}
	if family.PriorText(family.WorkflowFile, []byte(family.Workflow)) || develop.PriorText(develop.WorkflowFile, []byte(develop.Workflow)) {
		t.Fatal("a family's own rendering is a prior text")
	}
	if branch, other := develop.OtherBranch(develop.WorkflowFile, []byte(develop.Workflow)); other {
		t.Fatalf("a family's own rendering names another branch, %q", branch)
	}
	if !develop.PriorText(develop.WorkflowFile, []byte(family.Workflow)) {
		t.Fatal("the main rendering is not prior for a develop family")
	}
	if branch, other := develop.OtherBranch(develop.WorkflowFile, []byte(family.Workflow)); !other || branch != WorkflowBranch {
		t.Fatalf("OtherBranch of the main rendering for a develop family = %q, %v", branch, other)
	}
	if err := develop.Validate(); err != nil {
		t.Fatalf("Validate refused a rendered family: %v", err)
	}
}

// Negative: a workflow naming two push branch lines fails Validate. Positive and boundary: one
// line, and none, pass.
func TestValidateRefusesTwoPushBranchLines(t *testing.T) {
	family := branchFixtureFamily()
	if err := family.Validate(); err != nil {
		t.Fatalf("one push branch line: %v", err)
	}
	family.Workflow += "  other:\n    branches: ['main']\n"
	if err := family.Validate(); err == nil || !strings.Contains(err.Error(), "more than one push branch line") {
		t.Fatalf("two push branch lines: %v", err)
	}
	if err := fixtureFamily().Validate(); err != nil {
		t.Fatalf("no push branch line: %v", err)
	}
}

// workflowModel is the part of a workflow that decides whether a run starts and its jobs run.
type workflowModel struct {
	On   map[string]*eventFilter `yaml:"on"`
	Jobs map[string]struct {
		If string `yaml:"if"`
	} `yaml:"jobs"`
}

// eventFilter is one trigger's filters; a bare trigger has none.
type eventFilter struct {
	Types    []string `yaml:"types"`
	Branches []string `yaml:"branches"`
	Tags     []string `yaml:"tags"`
}

// hostedEvent is one event GitHub delivers: a push of a branch or tag ref, or a pull request
// activity on a draft or ready pull request.
type hostedEvent struct {
	name, action, ref string
	tag, draft        bool
}

// defaultPullRequestTypes are the activity types a pull_request trigger without types runs on.
var defaultPullRequestTypes = []string{"opened", "synchronize", "reopened"}

// starts reports whether event starts a run of the workflow: the trigger is declared, a push
// names a branch its branches filter lists (no filter selects every branch and tag, a branches
// filter alone selects no tag), and a pull request activity is one of its types.
func (m workflowModel) starts(event hostedEvent) bool {
	filter, declared := m.On[event.name]
	if !declared {
		return false
	}
	if filter == nil {
		filter = &eventFilter{}
	}
	if event.name == "pull_request" {
		types := filter.Types
		if len(types) == 0 {
			types = defaultPullRequestTypes
		}
		return slices.Contains(types, event.action)
	}
	if len(filter.Branches) == 0 && len(filter.Tags) == 0 {
		return true
	}
	if event.tag {
		return slices.Contains(filter.Tags, event.ref)
	}
	return slices.Contains(filter.Branches, event.ref)
}

// jobRuns evaluates the job conditions the hosted gates carry for event: none, or the draft
// skip, which holds unless the event is a pull request on a draft. On a push the missing
// pull_request property is an empty string, which GitHub's loose comparison reads as 0, not
// true (1). Any other condition fails the test rather than guess.
func jobRuns(t *testing.T, condition string, event hostedEvent) bool {
	t.Helper()
	switch strings.TrimSpace(condition) {
	case "":
		return true
	case "github.event.pull_request.draft != true":
		return event.name != "pull_request" || !event.draft
	}
	t.Fatalf("job condition %q has no model", condition)
	return false
}

// Positive: a push to the default branch, a ready pull request and a draft marked ready run the
// gate. Negative: a push to another branch or a tag, a draft opened or updated, and an activity
// outside the types start nothing or skip the job. Boundary: the rendering for develop moves the
// push filter to develop, and every rendering still reports its required status context.
func TestHostedWorkflowsRunOnlyOnTheDefaultBranchAndReadyPullRequests(t *testing.T) {
	for _, family := range hostedFamilies(t) {
		assertHostedGateRuns(t, family)
	}
}

// hostedEventCases are the events a hosted gate rendered for develop is judged on.
var hostedEventCases = []struct {
	name  string
	event hostedEvent
	runs  bool
}{
	{"positive default branch push", hostedEvent{name: "push", ref: "develop"}, true},
	{"positive ready pull request", hostedEvent{name: "pull_request", action: "opened"}, true},
	{"positive ready pull request update", hostedEvent{name: "pull_request", action: "synchronize"}, true},
	{"positive draft marked ready", hostedEvent{name: "pull_request", action: "ready_for_review"}, true},
	{"positive reopened", hostedEvent{name: "pull_request", action: "reopened"}, true},
	{"negative branch push", hostedEvent{name: "push", ref: "feature/x"}, false},
	{"negative main push in a develop repository", hostedEvent{name: "push", ref: "main"}, false},
	{"negative tag push", hostedEvent{name: "push", ref: "v1.0.0", tag: true}, false},
	{"negative draft opened", hostedEvent{name: "pull_request", action: "opened", draft: true}, false},
	{"negative draft updated", hostedEvent{name: "pull_request", action: "synchronize", draft: true}, false},
	{"negative edited", hostedEvent{name: "pull_request", action: "edited"}, false},
	{"boundary converted to draft", hostedEvent{name: "pull_request", action: "converted_to_draft", draft: true}, false},
}

// assertHostedGateRuns renders family for develop and fails the test for every event of
// hostedEventCases whose outcome differs, and unless the rendering reports the family's status
// context as a required check.
func assertHostedGateRuns(t *testing.T, family Family) {
	t.Helper()
	develop, err := family.ForBranch("develop")
	if err != nil {
		t.Fatal(err)
	}
	var model workflowModel
	if err := yaml.Unmarshal([]byte(develop.Workflow), &model); err != nil {
		t.Fatalf("%s: %v", family.Name, err)
	}
	job := model.Jobs[jobID(t, model)]
	for _, tc := range hostedEventCases {
		if got := model.starts(tc.event) && jobRuns(t, job.If, tc.event); got != tc.runs {
			t.Errorf("%s: %s runs = %v, want %v", family.Name, tc.name, got, tc.runs)
		}
	}
	contexts, err := forge.RequiredStatusContextsPlanned(t.Context(), t.TempDir(), map[string][]byte{develop.WorkflowFile: []byte(develop.Workflow)})
	if err != nil || !slices.Equal(contexts, []string{family.StatusContext}) {
		t.Errorf("%s: required status contexts = %v, %v; want [%s]", family.Name, contexts, err, family.StatusContext)
	}
}

// jobID returns the one job of a hosted gate's workflow.
func jobID(t *testing.T, model workflowModel) string {
	t.Helper()
	if len(model.Jobs) != 1 {
		t.Fatalf("hosted gate has %d jobs, want one", len(model.Jobs))
	}
	for id := range model.Jobs {
		return id
	}
	return ""
}
