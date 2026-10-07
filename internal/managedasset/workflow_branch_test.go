// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
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

// futureFamily is family as a later Praetor ships it: its current workflow has become a Prior
// text, recorded as its WorkflowBranch rendering, and the workflow has changed.
func futureFamily(t *testing.T, family Family) Family {
	t.Helper()
	digest, _, err := util.CanonicalTextDigest([]byte(family.Workflow))
	if err != nil {
		t.Fatal(err)
	}
	future := family
	future.Prior = map[string]string{digest: family.WorkflowFile}
	future.Workflow = strings.Replace(family.Workflow, "\njobs:", "\n# a later text\njobs:", 1)
	if future.Workflow == family.Workflow || !future.BranchDependent() {
		t.Fatalf("%s: the simulated later workflow did not change or names no default branch", family.Name)
	}
	return future
}

// Positive (finding of #815): once a hosted workflow changes, an unedited copy of today's text
// rendered for master, LF or CRLF, is still a prior rendering, for a main family and for a master
// family, so a repository whose default branch is not main refreshes it without --force.
// Negative: an edited master copy, one naming two push branch lines, and one naming a branch
// nothing renders stay refused. Boundary: the main copy is found by its digest alone.
func TestPriorTextRenderedForAnotherBranchIsPrior(t *testing.T) {
	for _, family := range hostedFamilies(t) {
		future := futureFamily(t, family)
		master, err := family.ForBranch("master")
		if err != nil {
			t.Fatal(err)
		}
		futureMaster, err := future.ForBranch("master")
		if err != nil {
			t.Fatal(err)
		}
		crlfText := strings.ReplaceAll(master.Workflow, "\n", "\r\n")
		for _, judge := range []Family{future, futureMaster} {
			if known, crlf := judge.PriorRendering(judge.WorkflowFile, []byte(master.Workflow)); !known || crlf {
				t.Errorf("%s for %s: master copy of the prior text known=%v crlf=%v", family.Name, judge.Branch(), known, crlf)
			}
			if known, crlf := judge.PriorRendering(judge.WorkflowFile, []byte(crlfText)); !known || !crlf {
				t.Errorf("%s for %s: CRLF master copy known=%v crlf=%v", family.Name, judge.Branch(), known, crlf)
			}
			if !judge.PriorText(judge.WorkflowFile, []byte(family.Workflow)) {
				t.Errorf("%s for %s: the main copy of the prior text is not prior", family.Name, judge.Branch())
			}
		}
		for name, text := range map[string]string{
			"edited":         master.Workflow + "# edited\n",
			"two lines":      master.Workflow + "  other:\n    branches: ['master']\n",
			"invalid branch": strings.Replace(master.Workflow, "['master']", "['-lead']", 1),
			"mixed endings":  strings.Replace(crlfText, "\r\n", "\n", 1),
		} {
			if future.PriorText(future.WorkflowFile, []byte(text)) || futureMaster.PriorText(futureMaster.WorkflowFile, []byte(text)) {
				t.Errorf("%s: %s master copy is a prior text", family.Name, name)
			}
		}
		if future.PriorText("tools/other.yml", []byte(master.Workflow)) {
			t.Errorf("%s: a master copy at another path is a prior text", family.Name)
		}
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

// workflowModel is the part of a workflow that decides whether a run starts and which of its
// steps run.
type workflowModel struct {
	On   map[string]*eventFilter `yaml:"on"`
	Jobs map[string]struct {
		If    string `yaml:"if"`
		Steps []struct {
			If string `yaml:"if"`
		} `yaml:"steps"`
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

// stepRuns evaluates the conditions the hosted gates carry for event: none, the draft step's,
// which holds on a draft pull request only, and the gate steps', which hold everywhere else. On
// a push the missing pull_request property is an empty string, which GitHub's loose comparison
// reads as 0, not true (1). Any other condition fails the test rather than guess.
func stepRuns(t *testing.T, condition string, event hostedEvent) bool {
	t.Helper()
	draft := event.name == "pull_request" && event.draft
	switch strings.TrimSpace(condition) {
	case "":
		return true
	case ghworkflow.HostedGateDraft:
		return draft
	case ghworkflow.HostedGateNotDraft:
		return !draft
	}
	t.Fatalf("step condition %q has no model", condition)
	return false
}

// The outcome of one event for a hosted gate: no run starts, the gate's steps run, or the draft
// step alone runs and fails the job, so the required check is red until the draft is marked
// ready.
const (
	noRun   = "no run"
	gateRun = "gate runs"
	refused = "draft refused"
)

// Positive: a push to the default branch, a ready pull request and a draft marked ready run the
// gate. Negative: a push to another branch or a tag, and an activity outside the types, start
// nothing; a draft opened, updated or reopened runs the draft step alone, which fails the job.
// Boundary: the rendering for develop moves the push filter to develop, holds the hosted gate
// shape for develop, and every rendering still reports its required status context.
func TestHostedWorkflowsRunOnlyOnTheDefaultBranchAndReadyPullRequests(t *testing.T) {
	for _, family := range hostedFamilies(t) {
		assertHostedGateRuns(t, family)
	}
}

// hostedEventCases are the events a hosted gate rendered for develop is judged on.
var hostedEventCases = []struct {
	name    string
	event   hostedEvent
	outcome string
}{
	{"positive default branch push", hostedEvent{name: "push", ref: "develop"}, gateRun},
	{"positive ready pull request", hostedEvent{name: "pull_request", action: "opened"}, gateRun},
	{"positive ready pull request update", hostedEvent{name: "pull_request", action: "synchronize"}, gateRun},
	{"positive draft marked ready", hostedEvent{name: "pull_request", action: "ready_for_review"}, gateRun},
	{"positive reopened", hostedEvent{name: "pull_request", action: "reopened"}, gateRun},
	{"negative branch push", hostedEvent{name: "push", ref: "feature/x"}, noRun},
	{"negative main push in a develop repository", hostedEvent{name: "push", ref: "main"}, noRun},
	{"negative tag push", hostedEvent{name: "push", ref: "v1.0.0", tag: true}, noRun},
	{"negative draft opened", hostedEvent{name: "pull_request", action: "opened", draft: true}, refused},
	{"negative draft updated", hostedEvent{name: "pull_request", action: "synchronize", draft: true}, refused},
	{"negative edited", hostedEvent{name: "pull_request", action: "edited"}, noRun},
	{"boundary draft reopened", hostedEvent{name: "pull_request", action: "reopened", draft: true}, refused},
	{"boundary converted to draft", hostedEvent{name: "pull_request", action: "converted_to_draft", draft: true}, noRun},
}

// assertHostedGateRuns renders family for develop and fails the test for every event of
// hostedEventCases whose outcome differs, unless the rendering holds the hosted gate shape for
// develop (ghworkflow.HostedGateFault), and unless it reports the family's status context as a
// required check.
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
	id := jobID(t, model)
	spec, err := ghworkflow.Parse([]byte(develop.Workflow))
	if err != nil || ghworkflow.HostedGateFault(&spec, id, "develop") != nil {
		t.Fatalf("%s: the develop rendering departs from the hosted gate shape: %v, %v", family.Name, err, ghworkflow.HostedGateFault(&spec, id, "develop"))
	}
	for _, tc := range hostedEventCases {
		if got := eventOutcome(t, model, id, tc.event); got != tc.outcome {
			t.Errorf("%s: %s: %s, want %s", family.Name, tc.name, got, tc.outcome)
		}
	}
	contexts, err := forge.RequiredStatusContextsPlanned(t.Context(), t.TempDir(), map[string][]byte{develop.WorkflowFile: []byte(develop.Workflow)})
	if err != nil || !slices.Equal(contexts, []string{family.StatusContext}) {
		t.Errorf("%s: required status contexts = %v, %v; want [%s]", family.Name, contexts, err, family.StatusContext)
	}
}

// eventOutcome is what event does to job id of model: no run, the gate steps run, or the draft
// step alone runs and fails the job.
func eventOutcome(t *testing.T, model workflowModel, id string, event hostedEvent) string {
	t.Helper()
	job := model.Jobs[id]
	if !model.starts(event) || !stepRuns(t, job.If, event) {
		return noRun
	}
	if len(job.Steps) < 2 {
		t.Fatalf("job %s has %d steps, want the draft step and gate steps", id, len(job.Steps))
	}
	if stepRuns(t, job.Steps[0].If, event) {
		return refused
	}
	for i := 1; i < len(job.Steps); i++ {
		if !stepRuns(t, job.Steps[i].If, event) {
			t.Fatalf("job %s step %d skips on %+v while the gate runs", id, i+1, event)
		}
	}
	return gateRun
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
