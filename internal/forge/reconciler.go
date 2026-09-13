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
type ReconcileEngine struct {
	issues       map[string]map[int]IssueSpec // repo -> number -> spec
	states       map[string]map[int]string    // repo -> number -> state ("open"/"closed")
	defaultOwner string
}

// NewReconcileEngine initializes an empty reconciliation engine.
func NewReconcileEngine(defaultOwner string) *ReconcileEngine {
	return &ReconcileEngine{
		issues:       make(map[string]map[int]IssueSpec),
		states:       make(map[string]map[int]string),
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

var checkboxRegex = regexp.MustCompile(`(?i)^\s*-\s*\[([ xX])\]\s*(.*)$`)

// ParseCheckboxDependencies extracts tasklist dependencies from an issue body.
func ParseCheckboxDependencies(body string) []CheckboxDep {
	deps := make([]CheckboxDep, 0)
	if strings.TrimSpace(body) == "" {
		return deps
	}

	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines) && i < MaxLinesLimit; i++ {
		line := lines[i]
		m := checkboxRegex.FindStringSubmatch(line)
		if len(m) >= 3 {
			isChecked := strings.EqualFold(m[1], "x")
			text := strings.TrimSpace(m[2])
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
