package forge

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
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
func (e *ReconcileEngine) TrackIssue(repo string, issue IssueSpec) {
	if _, ok := e.issues[repo]; !ok {
		e.issues[repo] = make(map[int]IssueSpec)
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
			isChecked := strings.ToLower(m[1]) == "x"
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

// Reconcile executes a sweep over all tracked issues, unblocking tasks whose dependencies are met.
func (e *ReconcileEngine) Reconcile(ctx context.Context) (*ReconciliationReport, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	report := &ReconciliationReport{
		Timestamp:       time.Now().UTC(),
		UnblockedIssues: make([]UnblockAction, 0),
		StillBlocked:    make([]BlockedSummary, 0),
	}

	for repo, repoIssues := range e.issues {
		for num, issue := range repoIssues {
			report.EvaluatedCount++
			e.evaluateSingleIssue(repo, num, issue, report)
		}
	}

	return report, nil
}

func (e *ReconcileEngine) evaluateSingleIssue(repo string, num int, issue IssueSpec, report *ReconciliationReport) {
	if issue.State == "closed" {
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

	for _, depStr := range issue.DependsOn {
		ref := parseSingleDepStr(depStr, repo)
		if ref.Number == 0 {
			continue
		}
		targetRepo := ref.Repo
		if targetRepo == "" {
			targetRepo = repo
		}

		state := e.resolveTargetState(targetRepo, ref.Number)
		depIdentifier := fmt.Sprintf("%s#%d", targetRepo, ref.Number)
		if state == "closed" {
			resolved = append(resolved, depIdentifier)
		} else {
			pending = append(pending, depIdentifier)
		}
	}
	return pending, resolved
}

func parseSingleDepStr(depStr, currentRepo string) IssueRef {
	clean := strings.TrimSpace(strings.TrimPrefix(depStr, "Depends-On:"))
	clean = strings.TrimSpace(clean)
	parts := strings.Split(clean, "#")
	if len(parts) != 2 {
		return IssueRef{}
	}

	num, err := strconv.Atoi(parts[1])
	if err != nil {
		return IssueRef{}
	}

	target := strings.TrimSpace(parts[0])
	if target == "" {
		return IssueRef{Repo: currentRepo, Number: num}
	}

	if strings.Contains(target, "/") {
		slashParts := strings.Split(target, "/")
		return IssueRef{Owner: slashParts[0], Repo: slashParts[1], Number: num}
	}

	return IssueRef{Repo: target, Number: num}
}

func (e *ReconcileEngine) checkCheckboxes(issue IssueSpec) []string {
	pending := make([]string, 0)
	boxes := ParseCheckboxDependencies(issue.Body)
	for _, box := range boxes {
		if !box.IsChecked {
			pending = append(pending, box.Text)
		}
	}
	return pending
}

func (e *ReconcileEngine) resolveTargetState(repo string, number int) string {
	if repoStates, ok := e.states[repo]; ok {
		if state, sOk := repoStates[number]; sOk {
			return state
		}
	}
	if !strings.Contains(repo, "/") && e.defaultOwner != "" {
		if repoStates, ok := e.states[e.defaultOwner+"/"+repo]; ok {
			if state, sOk := repoStates[number]; sOk {
				return state
			}
		}
	}
	for r, repoStates := range e.states {
		if strings.HasSuffix(r, "/"+repo) {
			if state, sOk := repoStates[number]; sOk {
				return state
			}
		}
	}
	return "open"
}

func hasLabel(labels []string, target string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, target) {
			return true
		}
	}
	return false
}
