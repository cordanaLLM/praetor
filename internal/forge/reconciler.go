package forge

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxTrackedReposLimit bounds every iteration over the engine's tracked repositories
	// (HISS-02).
	MaxTrackedReposLimit = 1000
	// depStateOpen marks a dependency whose target issue is tracked and still open.
	depStateOpen = "open"
	// depStateClosed marks a dependency whose target issue is tracked and closed.
	depStateClosed = "closed"
	// depStateUnknown marks a dependency whose target repository is not tracked, or whose
	// bare repository name matches more than one tracked repository. An unknown
	// dependency keeps the issue blocked: it is never treated as satisfied.
	depStateUnknown = "unknown"
)

// CheckboxDep captures a markdown task list checkbox dependency.
type CheckboxDep struct {
	Text      string   `json:"text"`
	IsChecked bool     `json:"is_checked"`
	TargetRef IssueRef `json:"target_ref"`
}

// UnblockAction describes an issue transitioned from blocked to ready.
type UnblockAction struct {
	Repo            string   `json:"repo"`
	IssueNumber     int      `json:"issue_number"`
	Title           string   `json:"title"`
	ResolvedPrereqs []string `json:"resolved_prereqs"`
	ActionTaken     string   `json:"action_taken"`
}

// BlockedSummary describes an issue that remains blocked by open dependencies.
type BlockedSummary struct {
	Repo           string   `json:"repo"`
	IssueNumber    int      `json:"issue_number"`
	PendingPrereqs []string `json:"pending_prereqs"`
	PendingBoxes   []string `json:"pending_boxes"`
}

// ReconciliationReport records the result of multi-repo dependency reconciliation.
type ReconciliationReport struct {
	Timestamp       time.Time        `json:"timestamp"`
	EvaluatedCount  int              `json:"evaluated_count"`
	UnblockedIssues []UnblockAction  `json:"unblocked_issues"`
	StillBlocked    []BlockedSummary `json:"still_blocked"`
}

// ReconcileEngine maintains the in-memory dependency DAG and reconciles issue statuses.
// The same engine plans the planning sync (PlanPlanning) over the issues and milestones
// it tracks.
type ReconcileEngine struct {
	issues       map[string]map[int]IssueSpec   // repo -> number -> spec
	states       map[string]map[int]string      // repo -> number -> state ("open"/"closed")
	milestones   map[string][]PlanningMilestone // repo -> milestones as the forge lists them
	defaultOwner string
}

// NewReconcileEngine initializes an empty reconciliation engine.
func NewReconcileEngine(defaultOwner string) *ReconcileEngine {
	return &ReconcileEngine{
		issues:       make(map[string]map[int]IssueSpec),
		states:       make(map[string]map[int]string),
		milestones:   make(map[string][]PlanningMilestone),
		defaultOwner: defaultOwner,
	}
}

// TrackIssue registers an issue into the engine's tracking state.
//
// The two maps are created independently: a repository whose state was seeded by
// SetIssueState before any issue of that repository was tracked must not have its
// recorded states discarded by the first TrackIssue call.
func (e *ReconcileEngine) TrackIssue(repo string, issue IssueSpec) {
	if _, ok := e.issues[repo]; !ok {
		e.issues[repo] = make(map[int]IssueSpec)
	}
	if _, ok := e.states[repo]; !ok {
		e.states[repo] = make(map[int]string)
	}
	e.issues[repo][issue.ID] = issue
	e.states[repo][issue.ID] = issue.State
}

// SetIssueState updates an issue's open/closed state.
func (e *ReconcileEngine) SetIssueState(repo string, number int, state string) {
	if _, ok := e.states[repo]; !ok {
		e.states[repo] = make(map[int]string)
	}
	e.states[repo][number] = state
}

// taskLineRegex matches one task-list line: the text before the box, the box character
// and the rest of the line from the closing bracket on. ParseCheckboxDependencies, the
// planning sync and the box ticking all read lines through it, so they agree on which
// lines are task items.
var taskLineRegex = regexp.MustCompile(`^(\s*-\s*\[)([ xX])(\].*)$`)

// ParseCheckboxDependencies extracts tasklist dependencies from an issue body.
func ParseCheckboxDependencies(body string) []CheckboxDep {
	deps := make([]CheckboxDep, 0)
	if strings.TrimSpace(body) == "" {
		return deps
	}

	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines) && i < MaxLinesLimit; i++ {
		line := lines[i]
		m := taskLineRegex.FindStringSubmatch(line)
		if len(m) >= 4 {
			isChecked := strings.EqualFold(m[2], "x")
			text := strings.TrimSpace(strings.TrimPrefix(m[3], "]"))
			ref := parseIssueRefFromText(text)
			deps = append(deps, CheckboxDep{
				Text:      text,
				IsChecked: isChecked,
				TargetRef: ref,
			})
		}
	}
	return deps
}

func parseIssueRefFromText(text string) IssueRef {
	refs := ParseIssueDependencies(text)
	if len(refs) > 0 {
		return refs[0]
	}
	// Fallback to simple #num match
	re := regexp.MustCompile(`#(\d+)`)
	m := re.FindStringSubmatch(text)
	if len(m) >= 2 {
		num, err := strconv.Atoi(m[1])
		if err == nil {
			return IssueRef{Number: num, Raw: m[0]}
		}
	}
	return IssueRef{}
}

// Reconcile executes a sweep over all tracked issues, unblocking tasks whose dependencies
// are met. Repositories and issue numbers are visited in sorted order so that the report
// and the label transitions derived from it are byte-for-byte reproducible across runs.
func (e *ReconcileEngine) Reconcile(ctx context.Context) (*ReconciliationReport, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	report := &ReconciliationReport{
		Timestamp:       time.Now().UTC(),
		UnblockedIssues: make([]UnblockAction, 0),
		StillBlocked:    make([]BlockedSummary, 0),
	}

	repos := slices.Sorted(maps.Keys(e.issues))
	for ri := 0; ri < len(repos) && ri < MaxTrackedReposLimit; ri++ {
		repoIssues := e.issues[repos[ri]]
		numbers := slices.Sorted(maps.Keys(repoIssues))
		for ni := 0; ni < len(numbers) && ni < MaxIssuesLimit; ni++ {
			report.EvaluatedCount++
			e.evaluateSingleIssue(repos[ri], numbers[ni], repoIssues[numbers[ni]], report)
		}
	}

	return report, nil
}

func (e *ReconcileEngine) evaluateSingleIssue(repo string, num int, issue IssueSpec, report *ReconciliationReport) {
	if issue.State == depStateClosed {
		return
	}

	pendingPrereqs, resolvedPrereqs := e.checkDependsOn(repo, issue)
	pendingBoxes := e.checkCheckboxes(issue)

	isCurrentlyBlocked := hasLabel(issue.Labels, "status/blocked") || hasLabel(issue.Labels, "blocked")
	hasPending := len(pendingPrereqs) > 0 || len(pendingBoxes) > 0

	if isCurrentlyBlocked && !hasPending && (len(resolvedPrereqs) > 0 || len(issue.DependsOn) > 0) {
		report.UnblockedIssues = append(report.UnblockedIssues, UnblockAction{
			Repo:            repo,
			IssueNumber:     num,
			Title:           issue.Title,
			ResolvedPrereqs: resolvedPrereqs,
			ActionTaken:     "Removed status/blocked, added status/ready-for-work",
		})
	} else if hasPending {
		report.StillBlocked = append(report.StillBlocked, BlockedSummary{
			Repo:           repo,
			IssueNumber:    num,
			PendingPrereqs: pendingPrereqs,
			PendingBoxes:   pendingBoxes,
		})
	}
}

func (e *ReconcileEngine) checkDependsOn(repo string, issue IssueSpec) ([]string, []string) {
	pending := make([]string, 0)
	resolved := make([]string, 0)

	for i := 0; i < len(issue.DependsOn) && i < MaxDependenciesLimit; i++ {
		ref := parseSingleDepStr(issue.DependsOn[i], repo)
		if ref.Number == 0 {
			continue
		}

		state, target := e.resolveDependency(ref, repo)
		depIdentifier := fmt.Sprintf("%s#%d", target, ref.Number)
		if state == depStateClosed {
			resolved = append(resolved, depIdentifier)
		} else {
			pending = append(pending, depIdentifier)
		}
	}
	return pending, resolved
}

// parseSingleDepStr parses one recorded dependency string into an issue reference.
//
// The recorded string is whatever the producer matched, so the "Depends-On:" tag carries
// the author's original casing; it is therefore stripped by the same case-insensitive
// regex that produced it rather than by a case-sensitive prefix trim, which would leave
// "depends-on: repo" to be mistaken for a repository name.
func parseSingleDepStr(depStr, currentRepo string) IssueRef {
	if refs := ParseIssueDependencies(depStr); len(refs) > 0 {
		return refs[0]
	}

	clean := strings.TrimSpace(depStr)
	parts := strings.SplitN(clean, "#", 2)
	if len(parts) != 2 {
		return IssueRef{}
	}

	num, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return IssueRef{}
	}

	target := strings.TrimSpace(parts[0])
	if target == "" {
		return IssueRef{Repo: currentRepo, Number: num, Raw: clean}
	}
	if owner, repo, ok := splitRepoKey(target); ok {
		return IssueRef{Owner: owner, Repo: repo, Number: num, Raw: clean}
	}
	return IssueRef{Repo: target, Number: num, Raw: clean}
}

// splitRepoKey splits an "owner/repo" key. It reports false for a bare repository name.
func splitRepoKey(key string) (owner, repo string, ok bool) {
	owner, repo, ok = strings.Cut(key, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", false
	}
	return owner, repo, true
}

func (e *ReconcileEngine) checkCheckboxes(issue IssueSpec) []string {
	pending := make([]string, 0)
	boxes := ParseCheckboxDependencies(issue.Body)
	for i := 0; i < len(boxes) && i < MaxDependenciesLimit; i++ {
		if !boxes[i].IsChecked {
			pending = append(pending, boxes[i].Text)
		}
	}
	return pending
}

// resolveDependency resolves the tracked state of ref as referenced from currentRepo and
// returns that state together with the repository key it was resolved against.
//
// An owner-qualified reference is matched only against that exact "owner/repo" key, so
// two repositories that share a short name under different owners are never conflated. A
// bare repository name prefers the referencing issue's own owner, then the engine's
// default owner, and only then a suffix scan that must match exactly one tracked
// repository; anything else resolves to depStateUnknown and keeps the issue blocked.
func (e *ReconcileEngine) resolveDependency(ref IssueRef, currentRepo string) (state, target string) {
	if ref.Owner != "" && ref.Repo != "" {
		key := ref.Owner + "/" + ref.Repo
		return e.lookupState(key, ref.Number), key
	}
	if ref.Repo == "" || ref.Repo == currentRepo {
		return e.lookupState(currentRepo, ref.Number), currentRepo
	}
	return e.resolveBareRepo(ref.Repo, ref.Number, currentRepo)
}

// lookupState returns the normalized state tracked for an exact repository key, or
// depStateUnknown when the repository or the issue is not tracked.
func (e *ReconcileEngine) lookupState(repo string, number int) string {
	repoStates, ok := e.states[repo]
	if !ok {
		return depStateUnknown
	}
	raw, ok := repoStates[number]
	if !ok {
		return depStateUnknown
	}
	if strings.EqualFold(strings.TrimSpace(raw), depStateClosed) {
		return depStateClosed
	}
	return depStateOpen
}

// resolveBareRepo resolves a dependency that names a repository without an owner.
func (e *ReconcileEngine) resolveBareRepo(repo string, number int, currentRepo string) (state, target string) {
	candidates := make([]string, 0, 3)
	candidates = append(candidates, repo)
	if owner, _, ok := splitRepoKey(currentRepo); ok {
		candidates = append(candidates, owner+"/"+repo)
	}
	if e.defaultOwner != "" {
		candidates = append(candidates, e.defaultOwner+"/"+repo)
	}

	for i := 0; i < len(candidates); i++ {
		if st := e.lookupState(candidates[i], number); st != depStateUnknown {
			return st, candidates[i]
		}
	}
	return e.resolveBySuffix(repo, number)
}

// resolveBySuffix scans the tracked repositories for a unique "<anyOwner>/<repo>" match.
// An ambiguous name (two owners tracking the same repository name) resolves to
// depStateUnknown instead of whichever entry the map iteration happened to yield.
func (e *ReconcileEngine) resolveBySuffix(repo string, number int) (state, target string) {
	keys := slices.Sorted(maps.Keys(e.states))
	matches := 0
	state, target = depStateUnknown, repo
	for i := 0; i < len(keys) && i < MaxTrackedReposLimit; i++ {
		if !strings.HasSuffix(keys[i], "/"+repo) {
			continue
		}
		if st := e.lookupState(keys[i], number); st != depStateUnknown {
			matches++
			state, target = st, keys[i]
		}
	}
	if matches != 1 {
		return depStateUnknown, repo
	}
	return state, target
}

func hasLabel(labels []string, target string) bool {
	for i := 0; i < len(labels) && i < MaxDependenciesLimit; i++ {
		if strings.EqualFold(labels[i], target) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------------------
// Planning sync (#837): parents tick the boxes of closed children, an epic or tracking
// parent closes once every child is closed, and a milestone closes once its issues are.
// Nothing is ever unticked or reopened; drift the sync cannot repair is reported.
// ---------------------------------------------------------------------------------------

const (
	// DefaultPlanningWriteCap is the number of forge writes one planning sync makes unless
	// the caller sets another cap. Writes beyond the cap are reported as deferred.
	DefaultPlanningWriteCap = 50
	// MaxPlanningWriteCap is the largest write cap a caller may set (HISS-02).
	MaxPlanningWriteCap = 500
	// maxPlanningMilestones bounds the milestones of one repository the sync visits, the
	// bound the milestone listing itself applies (HISS-02).
	maxPlanningMilestones = 5000
	// maxPlanningWrites bounds every walk over a report's writes (HISS-02): at most a tick
	// and a close per listed issue, plus one close per milestone.
	maxPlanningWrites = 2*MaxListedIssuesLimit + maxPlanningMilestones
	// planningWriteTimeout bounds the read and the write one planned write makes (HISS-02).
	planningWriteTimeout = 30 * time.Second
)

// PlanningWriteKind names one kind of forge write the planning sync makes.
type PlanningWriteKind string

const (
	// PlanningTick ticks the boxes of closed children in a parent's body.
	PlanningTick PlanningWriteKind = "tick"
	// PlanningCloseParent closes an epic or tracking parent whose children are all closed.
	PlanningCloseParent PlanningWriteKind = "close-parent"
	// PlanningCloseMilestone closes an open milestone that holds no open issue.
	PlanningCloseMilestone PlanningWriteKind = "close-milestone"
)

// Statuses of a PlanningWrite.
const (
	// PlanningPlanned is a write within the cap that has not been applied: every write of
	// a dry run keeps it.
	PlanningPlanned = "planned"
	// PlanningDeferred is a write beyond the cap; a later run makes it.
	PlanningDeferred = "deferred"
	// PlanningApplied is a write the forge accepted.
	PlanningApplied = "applied"
	// PlanningSkipped is a write a fresh read showed to be unnecessary or no longer safe.
	PlanningSkipped = "skipped"
	// PlanningFailed is a write the forge refused or that could not be attempted.
	PlanningFailed = "failed"
)

// Kinds of PlanningFinding: drift the sync reports and leaves as it is.
const (
	// FindingTickedOpenChild is a ticked box whose child issue is open.
	FindingTickedOpenChild = "ticked-open-child"
	// FindingClosedParentOpenChild is a closed parent with an open child.
	FindingClosedParentOpenChild = "closed-parent-open-child"
	// FindingUntrackedChild is a child outside the reconciled repositories, whose state is
	// unknown; its parent is never closed.
	FindingUntrackedChild = "untracked-child"
	// FindingParentWithoutChildren is an epic or tracking issue that names no child.
	FindingParentWithoutChildren = "parent-without-children"
	// FindingCompleteUnlabelledParent is an open parent whose children are all closed but
	// which carries neither parent label, so it is not closed.
	FindingCompleteUnlabelledParent = "complete-parent-without-label"
	// FindingEmptyMilestone is an open milestone that holds no issue at all.
	FindingEmptyMilestone = "empty-milestone"
	// FindingUnreadableTaskList is a body longer than MaxLinesLimit lines or naming more
	// than MaxDependenciesLimit children; the parent is not reconciled.
	FindingUnreadableTaskList = "unreadable-task-list"
)

// planningParentLabels are the labels that make an issue a parent the sync may close.
var planningParentLabels = []string{"epic", "tracking"}

// PlanningMilestone is the forge's view of one milestone.
type PlanningMilestone struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	State        string `json:"state"`
	OpenIssues   int    `json:"open_issues"`
	ClosedIssues int    `json:"closed_issues"`
}

// PlanningWrite is one forge write the planning sync intends, made or left for later.
// Children names the child issues a tick write ticks, as "<owner>/<repo>#<n>".
type PlanningWrite struct {
	Repo     string            `json:"repo"`
	Kind     PlanningWriteKind `json:"kind"`
	Number   int               `json:"number"`
	Title    string            `json:"title"`
	Children []string          `json:"children,omitempty"`
	Status   string            `json:"status"`
	Detail   string            `json:"detail,omitempty"`
}

// PlanningFinding is planning drift the sync reports without changing it.
type PlanningFinding struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// PlanningReport is the write log and the findings of one planning sync. Writes are in
// the order they are applied: repositories and numbers ascending, a parent's tick before
// its close, milestones after issues.
type PlanningReport struct {
	WriteCap int               `json:"write_cap"`
	Writes   []PlanningWrite   `json:"writes"`
	Findings []PlanningFinding `json:"findings"`
}

// TaskItem is one task-list line of an issue body whose text starts with an issue
// reference: the child it names and whether its box is ticked. Line is the zero-based
// line index in the body.
type TaskItem struct {
	Line    int      `json:"line"`
	Checked bool     `json:"checked"`
	Ref     IssueRef `json:"ref"`
}

var (
	// leadingIssueRef is "#<n>" or "<owner>/<repo>#<n>" at the start of a task item.
	leadingIssueRef = regexp.MustCompile(`^(?:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+))?#(\d+)\b`)
	// leadingIssueURL is a GitHub issue URL at the start of a task item.
	leadingIssueURL = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/issues/(\d+)\b`)
)

// TrackMilestones records the milestones the forge lists for repo, replacing any recorded
// before.
func (e *ReconcileEngine) TrackMilestones(repo string, milestones []PlanningMilestone) {
	e.milestones[repo] = slices.Clone(milestones)
}

// ParseTaskItems returns the task-list lines of body that name a child issue: a line
// "- [ ] #12", "- [x] owner/repo#12" or "- [ ] https://github.com/owner/repo/issues/12",
// where the reference opens the item's text. A reference later in the text does not make
// the issue a child. Lines inside fenced code blocks are not task items. complete is false
// when the body exceeds MaxLinesLimit lines or names more than MaxDependenciesLimit
// children: such a task list is not reconciled at all, never in part.
func ParseTaskItems(body string) (items []TaskItem, complete bool) {
	lines := strings.Split(body, "\n")
	if len(lines) > MaxLinesLimit {
		return nil, false
	}
	items = make([]TaskItem, 0)
	fence := ""
	for i := 0; i < len(lines) && i < MaxLinesLimit; i++ {
		if marker := fenceMarker(lines[i]); marker != "" {
			fence = toggleFence(fence, marker)
			continue
		}
		item, ok := parseTaskItem(i, lines[i])
		if fence != "" || !ok {
			continue
		}
		if len(items) >= MaxDependenciesLimit {
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

// fenceMarker returns the fence marker when line opens or closes a fenced code block.
func fenceMarker(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	for _, marker := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, marker) {
			return marker
		}
	}
	return ""
}

// toggleFence opens a fence when none is open and closes the open one on its own marker.
func toggleFence(open, marker string) string {
	switch open {
	case "":
		return marker
	case marker:
		return ""
	}
	return open
}

// parseTaskItem reads line i as a task item naming a child issue.
func parseTaskItem(i int, line string) (TaskItem, bool) {
	m := taskLineRegex.FindStringSubmatch(line)
	if m == nil {
		return TaskItem{}, false
	}
	ref, ok := leadingReference(strings.TrimSpace(strings.TrimPrefix(m[3], "]")))
	if !ok {
		return TaskItem{}, false
	}
	return TaskItem{Line: i, Checked: m[2] != " ", Ref: ref}, true
}

// leadingReference parses the issue reference that opens text.
func leadingReference(text string) (IssueRef, bool) {
	m := leadingIssueURL.FindStringSubmatch(text)
	if m == nil {
		m = leadingIssueRef.FindStringSubmatch(text)
	}
	if m == nil {
		return IssueRef{}, false
	}
	number, err := strconv.Atoi(m[3])
	if err != nil || number <= 0 {
		return IssueRef{}, false
	}
	return IssueRef{Owner: m[1], Repo: m[2], Number: number, Raw: m[0]}, true
}

// TickTaskLines ticks the task-list boxes on the given zero-based lines of body and
// leaves every other byte as it was. A listed line that is not a task item is left alone.
func TickTaskLines(body string, lines map[int]bool) string {
	parts := strings.Split(body, "\n")
	for i := 0; i < len(parts) && i < MaxLinesLimit; i++ {
		if !lines[i] {
			continue
		}
		loc := taskLineRegex.FindStringSubmatchIndex(parts[i])
		if loc == nil {
			continue
		}
		parts[i] = parts[i][:loc[4]] + "x" + parts[i][loc[5]:]
	}
	return strings.Join(parts, "\n")
}

// parentEvaluation is what one parent's task list and sub-issue progress say against the
// tracked states.
type parentEvaluation struct {
	children   int
	tickLines  map[int]string // line -> child key of an unticked box whose child is closed
	tickKeys   []string
	openKeys   []string
	tickedOpen []string
	unknown    []string
	subTotal   int
	subOpen    int
}

// allClosed reports whether the parent names at least one child and every child, task
// item and sub-issue alike, is known to be closed.
func (ev parentEvaluation) allClosed() bool {
	return ev.children+ev.subTotal > 0 && len(ev.openKeys) == 0 && len(ev.unknown) == 0 && ev.subOpen == 0
}

// evaluateParent resolves every child of issue in repo against the tracked states. A task
// item naming the parent itself is not a child.
func (e *ReconcileEngine) evaluateParent(repo string, issue IssueSpec, items []TaskItem) parentEvaluation {
	ev := parentEvaluation{tickLines: make(map[int]string)}
	if issue.SubIssues != nil {
		ev.subTotal = max(issue.SubIssues.Total, 0)
		ev.subOpen = max(issue.SubIssues.Total-issue.SubIssues.Completed, 0)
	}
	self := fmt.Sprintf("%s#%d", repo, issue.ID)
	for i := 0; i < len(items) && i < MaxDependenciesLimit; i++ {
		state, target := e.resolveDependency(items[i].Ref, repo)
		key := fmt.Sprintf("%s#%d", target, items[i].Ref.Number)
		if key == self {
			continue
		}
		ev.children++
		ev.classifyChild(items[i], state, key)
	}
	return ev
}

// classifyChild files one child under what its state asks of the parent.
func (ev *parentEvaluation) classifyChild(item TaskItem, state, key string) {
	switch state {
	case depStateClosed:
		if !item.Checked {
			ev.tickLines[item.Line] = key
			ev.tickKeys = append(ev.tickKeys, key)
		}
	case depStateOpen:
		ev.openKeys = append(ev.openKeys, key)
		if item.Checked {
			ev.tickedOpen = append(ev.tickedOpen, key)
		}
	default:
		ev.unknown = append(ev.unknown, key)
	}
}

// PlanPlanning plans the planning sync over every tracked repository without writing
// anything: the ticks and closes it would make, at most writeCap of them planned and the
// rest deferred, and the drift it reports instead of changing. writeCap must lie in
// 1..MaxPlanningWriteCap.
func (e *ReconcileEngine) PlanPlanning(writeCap int) (*PlanningReport, error) {
	if writeCap < 1 || writeCap > MaxPlanningWriteCap {
		return nil, fmt.Errorf("planning write cap %d is outside 1..%d", writeCap, MaxPlanningWriteCap)
	}
	report := &PlanningReport{WriteCap: writeCap, Writes: make([]PlanningWrite, 0), Findings: make([]PlanningFinding, 0)}
	repos := e.planningRepos()
	for ri := 0; ri < len(repos) && ri < MaxTrackedReposLimit; ri++ {
		repoIssues := e.issues[repos[ri]]
		numbers := slices.Sorted(maps.Keys(repoIssues))
		for ni := 0; ni < len(numbers) && ni < MaxListedIssuesLimit; ni++ {
			e.planParent(repos[ri], repoIssues[numbers[ni]], report)
		}
		e.planMilestones(repos[ri], report)
	}
	return report, nil
}

// planningRepos returns every repository with tracked issues or milestones, sorted.
func (e *ReconcileEngine) planningRepos() []string {
	set := make(map[string]bool, len(e.issues)+len(e.milestones))
	for repo := range e.issues {
		set[repo] = true
	}
	for repo := range e.milestones {
		set[repo] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// planParent plans the writes and reports the drift of one issue when it is a parent: it
// carries a parent label, names a child in its task list, or has sub-issues.
func (e *ReconcileEngine) planParent(repo string, issue IssueSpec, report *PlanningReport) {
	items, complete := ParseTaskItems(issue.Body)
	if !complete {
		report.finding(repo, issue.ID, FindingUnreadableTaskList, fmt.Sprintf(
			"the body exceeds %d lines or names more than %d children; not reconciled", MaxLinesLimit, MaxDependenciesLimit))
		return
	}
	labelled := hasParentLabel(issue.Labels)
	if !labelled && len(items) == 0 && (issue.SubIssues == nil || issue.SubIssues.Total <= 0) {
		return
	}
	ev := e.evaluateParent(repo, issue, items)
	open := !isClosedState(issue.State)
	reportParentDrift(repo, issue.ID, ev, open, labelled, report)
	if len(ev.tickKeys) > 0 {
		report.add(PlanningWrite{Repo: repo, Kind: PlanningTick, Number: issue.ID, Title: issue.Title, Children: ev.tickKeys})
	}
	if open && labelled && ev.allClosed() {
		report.add(PlanningWrite{Repo: repo, Kind: PlanningCloseParent, Number: issue.ID, Title: issue.Title,
			Detail: fmt.Sprintf("every child is closed (%d task-list, %d sub-issue)", ev.children, ev.subTotal)})
	}
}

// reportParentDrift reports what the sync leaves as it is about one parent: each child it
// cannot reconcile, then the parent's own state (reportParentState).
func reportParentDrift(repo string, number int, ev parentEvaluation, open, labelled bool, report *PlanningReport) {
	for _, key := range ev.tickedOpen {
		report.finding(repo, number, FindingTickedOpenChild, "box ticked for "+key+", which is open; left ticked")
	}
	for _, key := range ev.unknown {
		report.finding(repo, number, FindingUntrackedChild, key+" is not in the reconciled repositories; the parent is not closed")
	}
	reportParentState(repo, number, ev, open, labelled, report)
}

// reportParentState reports at most one finding about a parent's own state: closed with
// open children, labelled without children, or complete but not labelled for a close.
func reportParentState(repo string, number int, ev parentEvaluation, open, labelled bool, report *PlanningReport) {
	switch {
	case !open && (len(ev.openKeys) > 0 || ev.subOpen > 0):
		report.finding(repo, number, FindingClosedParentOpenChild, fmt.Sprintf(
			"closed with open children (%s; %d open sub-issues); not reopened", strings.Join(ev.openKeys, ", "), ev.subOpen))
	case labelled && ev.children+ev.subTotal == 0:
		report.finding(repo, number, FindingParentWithoutChildren,
			"carries the epic or tracking label but names no child issue in a task list and has no sub-issues")
	case open && !labelled && ev.allClosed():
		report.finding(repo, number, FindingCompleteUnlabelledParent,
			"every child is closed; left open because it carries neither the epic nor the tracking label")
	}
}

// planMilestones plans the close of every open milestone of repo that holds closed issues
// and no open one. An open milestone holding no issue at all is reported, not closed: it
// may be waiting for its first issue.
func (e *ReconcileEngine) planMilestones(repo string, report *PlanningReport) {
	milestones := slices.Clone(e.milestones[repo])
	slices.SortFunc(milestones, func(a, b PlanningMilestone) int { return a.Number - b.Number })
	for i := 0; i < len(milestones) && i < maxPlanningMilestones; i++ {
		m := milestones[i]
		if isClosedState(m.State) || m.OpenIssues > 0 {
			continue
		}
		if m.ClosedIssues <= 0 {
			report.finding(repo, m.Number, FindingEmptyMilestone, fmt.Sprintf("milestone %q holds no issue; left open", m.Title))
			continue
		}
		report.add(PlanningWrite{Repo: repo, Kind: PlanningCloseMilestone, Number: m.Number, Title: m.Title,
			Detail: fmt.Sprintf("0 open, %d closed issues", m.ClosedIssues)})
	}
}

// add appends w as planned while the cap allows, otherwise as deferred.
func (r *PlanningReport) add(w PlanningWrite) {
	w.Status = PlanningPlanned
	if r.Count(PlanningPlanned) >= r.WriteCap {
		w.Status = PlanningDeferred
		w.Detail = strings.TrimSpace(w.Detail + fmt.Sprintf(" (write cap %d reached)", r.WriteCap))
	}
	r.Writes = append(r.Writes, w)
}

// finding appends one finding.
func (r *PlanningReport) finding(repo string, number int, kind, detail string) {
	r.Findings = append(r.Findings, PlanningFinding{Repo: repo, Number: number, Kind: kind, Detail: detail})
}

// Count returns how many writes carry status.
func (r *PlanningReport) Count(status string) int {
	n := 0
	for i := 0; i < len(r.Writes) && i < maxPlanningWrites; i++ {
		if r.Writes[i].Status == status {
			n++
		}
	}
	return n
}

// hasParentLabel reports whether labels carry one of planningParentLabels.
func hasParentLabel(labels []string) bool {
	return slices.ContainsFunc(planningParentLabels, func(label string) bool { return hasLabel(labels, label) })
}

// isClosedState reports whether a forge state string means closed.
func isClosedState(state string) bool {
	return strings.EqualFold(strings.TrimSpace(state), depStateClosed)
}

// PlanningForge is the forge surface the planning sync reads and writes, bound to one
// repository. CloseIssue and CloseMilestone set the state to closed and change nothing
// else; no method reopens or unticks anything.
type PlanningForge interface {
	GetIssue(ctx context.Context, number int) (IssueSpec, error)
	EditIssueBody(ctx context.Context, number int, body string) error
	CloseIssue(ctx context.Context, number int) error
	GetMilestone(ctx context.Context, number int) (PlanningMilestone, error)
	CloseMilestone(ctx context.Context, number int) error
}

// PlanningForgeFor returns the PlanningForge bound to one "<owner>/<repo>".
type PlanningForgeFor func(repo string) (PlanningForge, error)

// ApplyPlanning makes every planned write of report through forgeFor, in order, and
// records each outcome in the write itself: applied, skipped with the reason a fresh read
// gave, or failed with the error. Each write first re-reads its issue or milestone, so a
// box ticked or a milestone that gained an open issue since the listing is left alone.
// Each write runs under its own planningWriteTimeout. It returns how many writes failed.
func (e *ReconcileEngine) ApplyPlanning(ctx context.Context, report *PlanningReport, forgeFor PlanningForgeFor) int {
	failed := 0
	for i := 0; i < len(report.Writes) && i < maxPlanningWrites; i++ {
		w := &report.Writes[i]
		if w.Status != PlanningPlanned {
			continue
		}
		status, detail := e.applyPlanningWrite(ctx, forgeFor, w)
		w.Status = status
		if detail != "" {
			w.Detail = detail
		}
		if status == PlanningFailed {
			failed++
		}
	}
	return failed
}

// applyPlanningWrite makes one write and returns its status and detail.
func (e *ReconcileEngine) applyPlanningWrite(ctx context.Context, forgeFor PlanningForgeFor, w *PlanningWrite) (string, string) {
	if err := ctx.Err(); err != nil {
		return PlanningFailed, err.Error()
	}
	f, err := forgeFor(w.Repo)
	if err != nil {
		return PlanningFailed, err.Error()
	}
	wctx, cancel := context.WithTimeout(ctx, planningWriteTimeout)
	defer cancel()
	var status, detail string
	switch w.Kind {
	case PlanningTick:
		status, detail, err = e.applyTick(wctx, f, w)
	case PlanningCloseParent:
		status, detail, err = e.applyCloseParent(wctx, f, w)
	case PlanningCloseMilestone:
		status, detail, err = applyCloseMilestone(wctx, f, w)
	default:
		err = fmt.Errorf("unknown planning write kind %q", w.Kind)
	}
	if err != nil {
		return PlanningFailed, err.Error()
	}
	return status, detail
}

// applyTick re-reads the parent and ticks the boxes of the planned children that are
// still unticked.
func (e *ReconcileEngine) applyTick(ctx context.Context, f PlanningForge, w *PlanningWrite) (string, string, error) {
	fresh, err := f.GetIssue(ctx, w.Number)
	if err != nil {
		return "", "", err
	}
	items, complete := ParseTaskItems(fresh.Body)
	if !complete {
		return PlanningSkipped, "the task list is no longer readable", nil
	}
	lines := make(map[int]bool)
	for line, key := range e.evaluateParent(w.Repo, fresh, items).tickLines {
		if slices.Contains(w.Children, key) {
			lines[line] = true
		}
	}
	if len(lines) == 0 {
		return PlanningSkipped, "already ticked since the listing", nil
	}
	if err := f.EditIssueBody(ctx, w.Number, TickTaskLines(fresh.Body, lines)); err != nil {
		return "", "", err
	}
	return PlanningApplied, "", nil
}

// applyCloseParent re-reads the parent and closes it when it is open, still carries a
// parent label and every child it names is closed.
func (e *ReconcileEngine) applyCloseParent(ctx context.Context, f PlanningForge, w *PlanningWrite) (string, string, error) {
	fresh, err := f.GetIssue(ctx, w.Number)
	if err != nil {
		return "", "", err
	}
	if isClosedState(fresh.State) {
		return PlanningSkipped, "already closed", nil
	}
	items, complete := ParseTaskItems(fresh.Body)
	if !complete || !hasParentLabel(fresh.Labels) || !e.evaluateParent(w.Repo, fresh, items).allClosed() {
		return PlanningSkipped, "the parent changed since the listing; left open", nil
	}
	if err := f.CloseIssue(ctx, w.Number); err != nil {
		return "", "", err
	}
	return PlanningApplied, "", nil
}

// applyCloseMilestone re-reads the milestone and closes it when it is still open and
// holds no open issue.
func applyCloseMilestone(ctx context.Context, f PlanningForge, w *PlanningWrite) (string, string, error) {
	fresh, err := f.GetMilestone(ctx, w.Number)
	if err != nil {
		return "", "", err
	}
	switch {
	case isClosedState(fresh.State):
		return PlanningSkipped, "already closed", nil
	case fresh.OpenIssues > 0:
		return PlanningSkipped, fmt.Sprintf("%d open issues since the listing; left open", fresh.OpenIssues), nil
	}
	if err := f.CloseMilestone(ctx, w.Number); err != nil {
		return "", "", err
	}
	return PlanningApplied, "", nil
}
