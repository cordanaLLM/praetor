package forge

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Invariant bounds adhering to HISS-02.
const (
	MaxIssuesLimit       = 1000
	MaxLinesLimit        = 2000
	MaxDependenciesLimit = 100
)

// IssueSpec represents a declarative specification for an issue across forges.
type IssueSpec struct {
	ID        int      `json:"id,omitempty" yaml:"id,omitempty"`
	Title     string   `json:"title" yaml:"title"`
	Body      string   `json:"body" yaml:"body"`
	State     string   `json:"state" yaml:"state"` // "open", "closed"
	Labels    []string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Assignees []string `json:"assignees,omitempty" yaml:"assignees,omitempty"`
	Milestone string   `json:"milestone,omitempty" yaml:"milestone,omitempty"`
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
}

// IssueRef represents a parsed cross-repository or local issue reference.
type IssueRef struct {
	Owner  string `json:"owner,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Number int    `json:"number"`
	Raw    string `json:"raw"`
}

// IssueResponse represents the result of synchronizing or creating an issue.
type IssueResponse struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

// Compiled regex for Depends-On tags: "Depends-On: [owner/repo]#123" or "Depends-On: [repo]#123" or "Depends-On: #123"
var dependsOnRegex = regexp.MustCompile(`(?i)depends-on:\s*(?:([a-zA-Z0-9_\-\.]+)/)?([a-zA-Z0-9_\-\.]+)?#(\d+)`)

// ParseIssueDependencies extracts cross-reference dependency tags from text.
func ParseIssueDependencies(body string) []IssueRef {
	if strings.TrimSpace(body) == "" {
		return []IssueRef{}
	}

	lines := strings.Split(body, "\n")
	var refs []IssueRef

	for i := 0; i < len(lines) && i < MaxLinesLimit; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		matches := dependsOnRegex.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			if len(refs) >= MaxDependenciesLimit {
				break
			}
			num, err := strconv.Atoi(m[3])
			if err != nil {
				continue
			}

			refs = append(refs, IssueRef{
				Owner:  m[1],
				Repo:   m[2],
				Number: num,
				Raw:    m[0],
			})
		}
	}

	return refs
}

// SyncReport summarises what a declarative issue synchronization changed on the forge.
type SyncReport struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	// Unchanged counts specs that already exist and carry no labels or state to converge.
	Unchanged int `json:"unchanged"`
}

// validateIssueBatch checks the whole batch before any write happens, so that a malformed
// spec in the middle of a batch cannot leave a half-created set of issues behind.
func validateIssueBatch(ctx context.Context, f Forge, issues []IssueSpec) error {
	if f == nil {
		return errors.New("forge driver cannot be nil")
	}
	if ctx == nil {
		return errors.New("context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before issue sync: %w", err)
	}
	if len(issues) > MaxIssuesLimit {
		return fmt.Errorf("issue count %d exceeds maximum allowed batch limit of %d", len(issues), MaxIssuesLimit)
	}
	seen := make(map[string]bool, len(issues))
	for i := 0; i < len(issues) && i < MaxIssuesLimit; i++ {
		title := strings.TrimSpace(issues[i].Title)
		if title == "" {
			return fmt.Errorf("issue at index %d has empty title", i)
		}
		if seen[title] {
			return &IssueTitleConflictError{Title: title, Source: "planned"}
		}
		seen[title] = true
	}
	return f.Authenticate(ctx)
}

// plannedTitles returns the trimmed titles of planned, which are the identities an
// issue batch resolves against the forge's inventory.
func plannedTitles(planned []IssueSpec) map[string]bool {
	titles := make(map[string]bool, len(planned))
	for i := 0; i < len(planned) && i < MaxIssuesLimit; i++ {
		titles[strings.TrimSpace(planned[i].Title)] = true
	}
	return titles
}

// existingIssuesByTitle indexes the issues already present on the forge whose trimmed
// title is one of wanted, which is the identity SyncIssues upserts on.
func existingIssuesByTitle(ctx context.Context, f Forge, wanted map[string]bool) (map[string]IssueSpec, error) {
	current, err := f.ListIssues(ctx, "all")
	if err != nil {
		return nil, fmt.Errorf("failed listing existing issues on %s: %w", f.Name(), err)
	}
	if len(current) > MaxListedIssuesLimit {
		return nil, &IssueListIncompleteError{Limit: MaxListedIssuesLimit}
	}
	index := make(map[string]IssueSpec, len(wanted))
	for i := 0; i < len(current) && i < MaxListedIssuesLimit; i++ {
		title := strings.TrimSpace(current[i].Title)
		if !wanted[title] {
			continue
		}
		if _, exists := index[title]; exists {
			return nil, &IssueTitleConflictError{Title: title, Source: "existing"}
		}
		index[title] = current[i]
	}
	return index, nil
}

// IssueUpsertOutcome names what resolving one planned issue did on the forge.
type IssueUpsertOutcome string

const (
	// IssueCreated means no existing issue carried the title, so one was created.
	IssueCreated IssueUpsertOutcome = "created"
	// IssueUpdated means an existing issue's labels or state were converged.
	IssueUpdated IssueUpsertOutcome = "updated"
	// IssueUnchanged means an existing issue carried the title and was left as it was.
	IssueUnchanged IssueUpsertOutcome = "unchanged"
)

// IssueUpsertResult is the issue a planned spec resolved to. Number is always the
// real issue number, so a later spec can reference it. URL is set only for a
// created issue: an issue inventory carries no URL.
type IssueUpsertResult struct {
	IssueResponse
	Outcome IssueUpsertOutcome `json:"outcome"`
}

// IssueBatch is a validated batch of planned issues bound to one forge, together
// with the existing issues their titles select. Every check SyncIssues makes before
// its first write happens when the batch is prepared, so a caller that must write
// issues one at a time, because a later body cites an earlier issue's number, gets
// the same all-or-nothing validation and the same title-keyed identity.
type IssueBatch struct {
	forge    Forge
	planned  map[string]bool
	existing map[string]IssueSpec
}

// PrepareIssueBatch validates planned and indexes the existing issues by trimmed
// title without mutating anything. Duplicate planned titles, an incomplete
// inventory and a title shared by two existing issues all fail here.
func PrepareIssueBatch(ctx context.Context, f Forge, planned []IssueSpec) (*IssueBatch, error) {
	if err := validateIssueBatch(ctx, f, planned); err != nil {
		return nil, err
	}
	titles := plannedTitles(planned)
	existing, err := existingIssuesByTitle(ctx, f, titles)
	if err != nil {
		return nil, err
	}
	return &IssueBatch{forge: f, planned: titles, existing: existing}, nil
}

// Ensure resolves spec to the existing issue carrying its title, which it leaves
// untouched, or creates the issue when none does. A created issue joins the index,
// so ensuring the same title again resolves to it instead of creating a duplicate.
// A spec whose title was not part of the prepared batch is refused: its title was
// never checked against the existing issues.
func (b *IssueBatch) Ensure(ctx context.Context, spec IssueSpec) (*IssueUpsertResult, error) {
	current, found, err := b.lookup(spec)
	if err != nil {
		return nil, err
	}
	if found {
		return &IssueUpsertResult{IssueResponse: IssueResponse{Number: current.ID, State: current.State}, Outcome: IssueUnchanged}, nil
	}
	created, err := b.forge.CreateIssue(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("failed to create issue %q on %s: %w", spec.Title, b.forge.Name(), err)
	}
	if created == nil {
		return nil, fmt.Errorf("%s created issue %q but returned no issue", b.forge.Name(), spec.Title)
	}
	b.existing[strings.TrimSpace(spec.Title)] = IssueSpec{ID: created.Number, Title: spec.Title, State: created.State}
	return &IssueUpsertResult{IssueResponse: *created, Outcome: IssueCreated}, nil
}

// Upsert is Ensure followed, for an issue that already existed, by converging its
// labels and state onto the spec when the spec sets any.
func (b *IssueBatch) Upsert(ctx context.Context, spec IssueSpec) (*IssueUpsertResult, error) {
	result, err := b.Ensure(ctx, spec)
	if err != nil || result.Outcome == IssueCreated || (len(spec.Labels) == 0 && spec.State == "") {
		return result, err
	}
	if err := b.forge.UpdateIssue(ctx, result.Number, spec.Labels, spec.State); err != nil {
		return nil, fmt.Errorf("failed to update issue %q (#%d) on %s: %w", spec.Title, result.Number, b.forge.Name(), err)
	}
	if spec.State != "" {
		result.State = spec.State
	}
	result.Outcome = IssueUpdated
	return result, nil
}

// lookup finds the existing issue carrying spec's title, refusing a spec outside
// the prepared batch.
func (b *IssueBatch) lookup(spec IssueSpec) (IssueSpec, bool, error) {
	title := strings.TrimSpace(spec.Title)
	if b == nil || b.forge == nil || !b.planned[title] {
		return IssueSpec{}, false, fmt.Errorf("issue %q is not part of the prepared batch", spec.Title)
	}
	current, found := b.existing[title]
	return current, found, nil
}

// SyncIssues converges a declarative batch of issues onto the target forge. It is an
// upsert keyed on the issue title: re-running the same batch updates the existing issues
// instead of creating duplicates. Planned titles must be unique after trimming;
// incomplete inventories or ambiguous selected titles fail before any mutation.
func SyncIssues(ctx context.Context, f Forge, issues []IssueSpec) (*SyncReport, error) {
	batch, err := PrepareIssueBatch(ctx, f, issues)
	if err != nil {
		return nil, err
	}

	rep := &SyncReport{}
	for i := 0; i < len(issues) && i < MaxIssuesLimit; i++ {
		if err := ctx.Err(); err != nil {
			return rep, fmt.Errorf("context cancelled during issue batch sync at index %d: %w", i, err)
		}
		result, err := batch.Upsert(ctx, issues[i])
		if err != nil {
			return rep, err
		}
		switch result.Outcome {
		case IssueCreated:
			rep.Created++
		case IssueUpdated:
			rep.Updated++
		default:
			rep.Unchanged++
		}
	}

	return rep, nil
}
