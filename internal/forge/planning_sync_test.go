package forge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

const planningRepo = "acme/widgets"

// fakePlanningForge is an in-memory PlanningForge over one repository. Every write is
// logged and applied to its state, so a test reads back what the sync changed.
type fakePlanningForge struct {
	issues     map[int]IssueSpec
	milestones map[int]PlanningMilestone
	writes     []string
	editErr    error
}

func newFakePlanningForge(issues []IssueSpec, milestones []PlanningMilestone) *fakePlanningForge {
	f := &fakePlanningForge{issues: map[int]IssueSpec{}, milestones: map[int]PlanningMilestone{}}
	for _, issue := range issues {
		f.issues[issue.ID] = issue
	}
	for _, m := range milestones {
		f.milestones[m.Number] = m
	}
	return f
}

func (f *fakePlanningForge) GetIssue(_ context.Context, number int) (IssueSpec, error) {
	issue, ok := f.issues[number]
	if !ok {
		return IssueSpec{}, fmt.Errorf("issue #%d does not exist", number)
	}
	return issue, nil
}

func (f *fakePlanningForge) EditIssueBody(_ context.Context, number int, body string) error {
	if f.editErr != nil {
		return f.editErr
	}
	issue := f.issues[number]
	issue.Body = body
	f.issues[number] = issue
	f.writes = append(f.writes, fmt.Sprintf("edit #%d", number))
	return nil
}

func (f *fakePlanningForge) CloseIssue(_ context.Context, number int) error {
	issue := f.issues[number]
	issue.State = "closed"
	f.issues[number] = issue
	f.writes = append(f.writes, fmt.Sprintf("close #%d", number))
	return nil
}

func (f *fakePlanningForge) GetMilestone(_ context.Context, number int) (PlanningMilestone, error) {
	m, ok := f.milestones[number]
	if !ok {
		return PlanningMilestone{}, fmt.Errorf("milestone %d does not exist", number)
	}
	return m, nil
}

func (f *fakePlanningForge) CloseMilestone(_ context.Context, number int) error {
	m := f.milestones[number]
	m.State = "closed"
	f.milestones[number] = m
	f.writes = append(f.writes, fmt.Sprintf("close milestone %d", number))
	return nil
}

// planningEngine tracks issues and milestones of planningRepo and returns the engine with
// a fake forge holding the same state.
func planningEngine(issues []IssueSpec, milestones []PlanningMilestone) (*ReconcileEngine, *fakePlanningForge) {
	engine := NewReconcileEngine("acme")
	for _, issue := range issues {
		engine.TrackIssue(planningRepo, issue)
	}
	if milestones != nil {
		engine.TrackMilestones(planningRepo, milestones)
	}
	return engine, newFakePlanningForge(issues, milestones)
}

func forgeOf(f PlanningForge) PlanningForgeFor {
	return func(repo string) (PlanningForge, error) {
		if repo != planningRepo {
			return nil, fmt.Errorf("unexpected repository %s", repo)
		}
		return f, nil
	}
}

func epicIssue(number int, body string, labels ...string) IssueSpec {
	return IssueSpec{ID: number, Title: fmt.Sprintf("parent %d", number), Body: body, State: "open", Labels: labels}
}

func childIssue(number int, state string) IssueSpec {
	return IssueSpec{ID: number, Title: fmt.Sprintf("child %d", number), State: state}
}

func writeKinds(report *PlanningReport) []string {
	kinds := make([]string, 0, len(report.Writes))
	for _, w := range report.Writes {
		kinds = append(kinds, fmt.Sprintf("%s #%d %s", w.Kind, w.Number, w.Status))
	}
	return kinds
}

func findingKinds(report *PlanningReport) []string {
	kinds := make([]string, 0, len(report.Findings))
	for _, f := range report.Findings {
		kinds = append(kinds, fmt.Sprintf("%s #%d", f.Kind, f.Number))
	}
	return kinds
}

func TestParseTaskItems_Positive_LeadingReferences(t *testing.T) {
	body := "- [ ] #12\n- [x] other/repo#13 with text\n  - [X] https://github.com/acme/widgets/issues/14\n- [ ] #15."
	items, complete := ParseTaskItems(body)
	if !complete || len(items) != 4 {
		t.Fatalf("items=%+v complete=%t", items, complete)
	}
	want := []TaskItem{
		{Line: 0, Checked: false, Ref: IssueRef{Number: 12, Raw: "#12"}},
		{Line: 1, Checked: true, Ref: IssueRef{Owner: "other", Repo: "repo", Number: 13, Raw: "other/repo#13"}},
		{Line: 2, Checked: true, Ref: IssueRef{Owner: "acme", Repo: "widgets", Number: 14, Raw: "https://github.com/acme/widgets/issues/14"}},
		{Line: 3, Checked: false, Ref: IssueRef{Number: 15, Raw: "#15"}},
	}
	if !slices.Equal(items, want) {
		t.Fatalf("got %+v\nwant %+v", items, want)
	}
}

func TestParseTaskItems_Negative_NotChildren(t *testing.T) {
	body := strings.Join([]string{
		"- [ ] fix the bug in #12", // reference later in the text
		"- [ ] #12abc",             // not a reference
		"* [ ] #13",                // not the task-list bullet the reconciler reads
		"#14",                      // not a task item
		"```", "- [ ] #15", "```",  // fenced example
		"~~~md", "- [ ] #16", "~~~", // tilde fence
		"- [ ] #0",                       // no issue zero
		"- [ ] #99999999999999999999",    // overflows
		"- [ ] **Task 1**: [TASK 1/5] x", // the pre-#837 epic checklist
	}, "\n")
	items, complete := ParseTaskItems(body)
	if !complete || len(items) != 0 {
		t.Fatalf("expected no children, got %+v (complete %t)", items, complete)
	}
}

func TestParseTaskItems_Boundary_Limits(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= MaxDependenciesLimit; i++ {
		fmt.Fprintf(&sb, "- [ ] #%d\n", i)
	}
	if items, complete := ParseTaskItems(sb.String()); !complete || len(items) != MaxDependenciesLimit {
		t.Fatalf("%d children: complete=%t len=%d", MaxDependenciesLimit, complete, len(items))
	}
	sb.WriteString("- [ ] #9999\n")
	if items, complete := ParseTaskItems(sb.String()); complete || items != nil {
		t.Fatalf("one child over the limit must make the list unreadable, got %d items", len(items))
	}
	long := strings.Repeat("line\n", MaxLinesLimit) + "- [ ] #1"
	if _, complete := ParseTaskItems(long); complete {
		t.Fatal("a body over the line limit must be unreadable")
	}
	if items, complete := ParseTaskItems(""); !complete || len(items) != 0 {
		t.Fatalf("empty body: %+v %t", items, complete)
	}
}

func TestTickTaskLines_3D(t *testing.T) {
	body := "intro\r\n- [ ] #2\r\n- [ ] #3\r\n  - [ ] #4 nested"
	got := TickTaskLines(body, map[int]bool{1: true, 3: true, 0: true, 99: true})
	want := "intro\r\n- [x] #2\r\n- [ ] #3\r\n  - [x] #4 nested"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if TickTaskLines(body, nil) != body {
		t.Fatal("no lines must leave the body unchanged")
	}
}

// Positive: a parent with mixed children ticks exactly the closed ones and stays open.
func TestPlanningSync_Positive_MixedChildrenTickExactlyClosed(t *testing.T) {
	parent := epicIssue(1, "## Children\n- [ ] #2\n- [ ] #3\n- [x] #4\n- [ ] #5\ntrailing", "epic")
	engine, fake := planningEngine([]IssueSpec{parent, childIssue(2, "closed"), childIssue(3, "open"),
		childIssue(4, "closed"), childIssue(5, "closed")}, nil)

	report, err := engine.PlanPlanning(DefaultPlanningWriteCap)
	if err != nil {
		t.Fatal(err)
	}
	if got := writeKinds(report); !slices.Equal(got, []string{"tick #1 planned"}) {
		t.Fatalf("writes = %v", got)
	}
	if want := []string{"acme/widgets#2", "acme/widgets#5"}; !slices.Equal(report.Writes[0].Children, want) {
		t.Fatalf("ticked children = %v, want %v", report.Writes[0].Children, want)
	}
	if failed := engine.ApplyPlanning(t.Context(), report, forgeOf(fake)); failed != 0 {
		t.Fatalf("%d writes failed: %+v", failed, report.Writes)
	}
	if got := fake.issues[1].Body; got != "## Children\n- [x] #2\n- [ ] #3\n- [x] #4\n- [x] #5\ntrailing" {
		t.Fatalf("body after the tick: %q", got)
	}
	if fake.issues[1].State != "open" || report.Writes[0].Status != PlanningApplied {
		t.Fatalf("parent with an open child must stay open: %+v", report.Writes)
	}
}

// Positive: a parent whose children are all closed is ticked, then closed.
func TestPlanningSync_Positive_AllChildrenClosedClosesParent(t *testing.T) {
	parent := epicIssue(1, "- [ ] #2\n- [x] #3", "tracking")
	engine, fake := planningEngine([]IssueSpec{parent, childIssue(2, "closed"), childIssue(3, "closed")}, nil)

	report, err := engine.PlanPlanning(DefaultPlanningWriteCap)
	if err != nil {
		t.Fatal(err)
	}
	if got := writeKinds(report); !slices.Equal(got, []string{"tick #1 planned", "close-parent #1 planned"}) {
		t.Fatalf("writes = %v", got)
	}
	if failed := engine.ApplyPlanning(t.Context(), report, forgeOf(fake)); failed != 0 {
		t.Fatalf("writes failed: %+v", report.Writes)
	}
	if !slices.Equal(fake.writes, []string{"edit #1", "close #1"}) || fake.issues[1].State != "closed" {
		t.Fatalf("forge writes %v, state %s", fake.writes, fake.issues[1].State)
	}
}

// Positive: an open milestone with closed issues and none open closes; one with an open
// issue stays, and so does one holding no issue at all.
func TestPlanningSync_Positive_MilestonesCloseOnlyWhenDone(t *testing.T) {
	milestones := []PlanningMilestone{
		{Number: 3, Title: "busy", State: "open", OpenIssues: 1, ClosedIssues: 4},
		{Number: 1, Title: "done", State: "open", OpenIssues: 0, ClosedIssues: 5},
		{Number: 2, Title: "fresh", State: "open"},
		{Number: 4, Title: "old", State: "closed", ClosedIssues: 2},
	}
	engine, fake := planningEngine(nil, milestones)

	report, err := engine.PlanPlanning(DefaultPlanningWriteCap)
	if err != nil {
		t.Fatal(err)
	}
	if got := writeKinds(report); !slices.Equal(got, []string{"close-milestone #1 planned"}) {
		t.Fatalf("writes = %v", got)
	}
	if got := findingKinds(report); !slices.Equal(got, []string{FindingEmptyMilestone + " #2"}) {
		t.Fatalf("findings = %v", got)
	}
	if failed := engine.ApplyPlanning(t.Context(), report, forgeOf(fake)); failed != 0 {
		t.Fatalf("writes failed: %+v", report.Writes)
	}
	if fake.milestones[1].State != "closed" || fake.milestones[3].State != "open" || fake.milestones[2].State != "open" {
		t.Fatalf("milestones after the sync: %+v", fake.milestones)
	}
}

// Negative: a ticked box whose child is open is reported and never unticked; a closed
// parent with an open child is reported and never reopened; an unlabelled parent whose
// children are all closed is ticked but left open.
func TestPlanningSync_Negative_DriftIsReportedNotChanged(t *testing.T) {
	ticked := epicIssue(1, "- [x] #2", "epic")
	closedParent := IssueSpec{ID: 3, Title: "closed epic", Body: "- [ ] #2\n- [ ] #5", State: "closed", Labels: []string{"epic"}}
	unlabelled := epicIssue(4, "- [ ] #5")
	engine, fake := planningEngine([]IssueSpec{ticked, childIssue(2, "open"), closedParent, unlabelled, childIssue(5, "closed")}, nil)

	report, err := engine.PlanPlanning(DefaultPlanningWriteCap)
	if err != nil {
		t.Fatal(err)
	}
	wantFindings := []string{FindingTickedOpenChild + " #1", FindingClosedParentOpenChild + " #3", FindingCompleteUnlabelledParent + " #4"}
	if got := findingKinds(report); !slices.Equal(got, wantFindings) {
		t.Fatalf("findings = %v, want %v", got, wantFindings)
	}
	// The closed parent's closed child is still ticked; nothing touches #1 or reopens #3.
	if got := writeKinds(report); !slices.Equal(got, []string{"tick #3 planned", "tick #4 planned"}) {
		t.Fatalf("writes = %v", got)
	}
	if failed := engine.ApplyPlanning(t.Context(), report, forgeOf(fake)); failed != 0 {
		t.Fatalf("writes failed: %+v", report.Writes)
	}
	if fake.issues[1].Body != "- [x] #2" || fake.issues[3].State != "closed" || fake.issues[4].State != "open" {
		t.Fatalf("drift was changed: %+v", fake.issues)
	}
	if fake.issues[3].Body != "- [ ] #2\n- [x] #5" {
		t.Fatalf("closed parent body: %q", fake.issues[3].Body)
	}
}

// Negative: a child outside the reconciled repositories keeps its parent open, an epic
// naming no child is reported, a self reference is no child, and a parent label without
// any child never closes.
func TestPlanningSync_Negative_UnknownChildrenKeepParentOpen(t *testing.T) {
	foreign := epicIssue(1, "- [x] elsewhere/repo#9\n- [x] #1", "epic")
	empty := epicIssue(2, "no task list", "epic")
	engine, _ := planningEngine([]IssueSpec{foreign, empty}, nil)

	report, err := engine.PlanPlanning(DefaultPlanningWriteCap)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Writes) != 0 {
		t.Fatalf("expected no write, got %v", writeKinds(report))
	}
	want := []string{FindingUntrackedChild + " #1", FindingParentWithoutChildren + " #2"}
	if got := findingKinds(report); !slices.Equal(got, want) {
		t.Fatalf("findings = %v, want %v", got, want)
	}
}

// Negative: a fresh read that shows the work already done, or the parent changed, skips
// the write; a refused edit fails it and is counted.
func TestPlanningSync_Negative_ApplyRereadsAndReportsFailures(t *testing.T) {
	parent := epicIssue(1, "- [ ] #2", "epic")
	engine, fake := planningEngine([]IssueSpec{parent, childIssue(2, "closed")},
		[]PlanningMilestone{{Number: 1, Title: "m", State: "open", ClosedIssues: 1}})
	report, err := engine.PlanPlanning(DefaultPlanningWriteCap)
	if err != nil {
		t.Fatal(err)
	}
	// Between the listing and the apply someone ticked the box, removed the label and
	// added an open issue to the milestone.
	fake.issues[1] = IssueSpec{ID: 1, Body: "- [x] #2", State: "open"}
	fake.milestones[1] = PlanningMilestone{Number: 1, State: "open", OpenIssues: 1, ClosedIssues: 1}
	if failed := engine.ApplyPlanning(t.Context(), report, forgeOf(fake)); failed != 0 {
		t.Fatalf("writes failed: %+v", report.Writes)
	}
	for _, w := range report.Writes {
		if w.Status != PlanningSkipped || w.Detail == "" {
			t.Errorf("%s #%d: status %s detail %q, want skipped with a reason", w.Kind, w.Number, w.Status, w.Detail)
		}
	}
	if len(fake.writes) != 0 {
		t.Fatalf("skipped writes reached the forge: %v", fake.writes)
	}

	engine, fake = planningEngine([]IssueSpec{epicIssue(1, "- [ ] #2"), childIssue(2, "closed")}, nil)
	fake.editErr = errors.New("forbidden")
	report, err = engine.PlanPlanning(DefaultPlanningWriteCap)
	if err != nil {
		t.Fatal(err)
	}
	if failed := engine.ApplyPlanning(t.Context(), report, forgeOf(fake)); failed != 1 || report.Writes[0].Status != PlanningFailed ||
		!strings.Contains(report.Writes[0].Detail, "forbidden") {
		t.Fatalf("refused edit: failed=%d writes=%+v", failed, report.Writes)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	report, err = engine.PlanPlanning(DefaultPlanningWriteCap)
	if err != nil {
		t.Fatal(err)
	}
	if failed := engine.ApplyPlanning(cancelled, report, forgeOf(fake)); failed != 1 {
		t.Fatalf("a cancelled run must fail its writes, failed=%d", failed)
	}
}

// Boundary: the write cap plans exactly cap writes and defers the rest, a parent's close
// is deferred with its tick, and the cap itself is bounded.
func TestPlanningSync_Boundary_WriteCap(t *testing.T) {
	issues := []IssueSpec{
		epicIssue(1, "- [ ] #9", "epic"), epicIssue(2, "- [ ] #9", "epic"), childIssue(9, "closed"),
	}
	engine, fake := planningEngine(issues, []PlanningMilestone{{Number: 1, State: "open", ClosedIssues: 1}})

	report, err := engine.PlanPlanning(1)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tick #1 planned", "close-parent #1 deferred", "tick #2 deferred", "close-parent #2 deferred", "close-milestone #1 deferred"}
	if got := writeKinds(report); !slices.Equal(got, want) {
		t.Fatalf("writes = %v, want %v", got, want)
	}
	if !strings.Contains(report.Writes[4].Detail, "write cap 1 reached") {
		t.Fatalf("deferred write must say why: %q", report.Writes[4].Detail)
	}
	if failed := engine.ApplyPlanning(t.Context(), report, forgeOf(fake)); failed != 0 || len(fake.writes) != 1 {
		t.Fatalf("cap 1 made %v (failed %d)", fake.writes, failed)
	}
	if report.Count(PlanningApplied) != 1 || report.Count(PlanningDeferred) != 4 {
		t.Fatalf("applied %d deferred %d", report.Count(PlanningApplied), report.Count(PlanningDeferred))
	}
	for _, bad := range []int{0, -1, MaxPlanningWriteCap + 1} {
		if _, err := engine.PlanPlanning(bad); err == nil {
			t.Errorf("write cap %d accepted", bad)
		}
	}
	if _, err := engine.PlanPlanning(MaxPlanningWriteCap); err != nil {
		t.Fatalf("the largest cap must be accepted: %v", err)
	}
}

// Boundary: sub-issue progress counts as children: all completed closes a labelled
// parent, one open keeps it open, and a closed parent with an open sub-issue is reported.
func TestPlanningSync_Boundary_SubIssues(t *testing.T) {
	done := IssueSpec{ID: 1, Title: "done", State: "open", Labels: []string{"epic"}, SubIssues: &SubIssueSummary{Total: 2, Completed: 2}}
	busy := IssueSpec{ID: 2, Title: "busy", State: "open", Labels: []string{"epic"}, SubIssues: &SubIssueSummary{Total: 2, Completed: 1}}
	closed := IssueSpec{ID: 3, Title: "closed", State: "closed", SubIssues: &SubIssueSummary{Total: 1}}
	engine, _ := planningEngine([]IssueSpec{done, busy, closed}, nil)

	report, err := engine.PlanPlanning(DefaultPlanningWriteCap)
	if err != nil {
		t.Fatal(err)
	}
	if got := writeKinds(report); !slices.Equal(got, []string{"close-parent #1 planned"}) {
		t.Fatalf("writes = %v", got)
	}
	if got := findingKinds(report); !slices.Equal(got, []string{FindingClosedParentOpenChild + " #3"}) {
		t.Fatalf("findings = %v", got)
	}
}

// Boundary: a body over the line limit is reported and not reconciled at all.
func TestPlanningSync_Boundary_UnreadableTaskList(t *testing.T) {
	body := strings.Repeat("x\n", MaxLinesLimit) + "- [ ] #2"
	engine, _ := planningEngine([]IssueSpec{epicIssue(1, body, "epic"), childIssue(2, "closed")}, nil)
	report, err := engine.PlanPlanning(DefaultPlanningWriteCap)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Writes) != 0 || !slices.Equal(findingKinds(report), []string{FindingUnreadableTaskList + " #1"}) {
		t.Fatalf("writes %v findings %v", writeKinds(report), findingKinds(report))
	}
}
