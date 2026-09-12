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
	for i := 0; i < len(issues) && i < MaxIssuesLimit; i++ {
		if strings.TrimSpace(issues[i].Title) == "" {
			return fmt.Errorf("issue at index %d has empty title", i)
		}
	}
	return f.Authenticate(ctx)
}

// existingIssuesByTitle indexes the issues already present on the forge by trimmed title,
// which is the identity SyncIssues upserts on.
func existingIssuesByTitle(ctx context.Context, f Forge) (map[string]IssueSpec, error) {
	current, err := f.ListIssues(ctx, "all")
	if err != nil {
		return nil, fmt.Errorf("failed listing existing issues on %s: %w", f.Name(), err)
	}
	index := make(map[string]IssueSpec, len(current))
	for i := 0; i < len(current) && i < MaxIssuesLimit; i++ {
		index[strings.TrimSpace(current[i].Title)] = current[i]
	}
	return index, nil
}

// SyncIssues converges a declarative batch of issues onto the target forge. It is an
// upsert keyed on the issue title: re-running the same batch updates the existing issues
// instead of creating duplicates.
func SyncIssues(ctx context.Context, f Forge, issues []IssueSpec) (*SyncReport, error) {
	if err := validateIssueBatch(ctx, f, issues); err != nil {
		return nil, err
	}
	existing, err := existingIssuesByTitle(ctx, f)
	if err != nil {
		return nil, err
	}

	rep := &SyncReport{}
	for i := 0; i < len(issues) && i < MaxIssuesLimit; i++ {
		if err := ctx.Err(); err != nil {
			return rep, fmt.Errorf("context cancelled during issue batch sync at index %d: %w", i, err)
		}
		spec := issues[i]
		current, found := existing[strings.TrimSpace(spec.Title)]
		if !found {
			if _, err := f.CreateIssue(ctx, spec); err != nil {
				return rep, fmt.Errorf("failed to create issue %q on %s: %w", spec.Title, f.Name(), err)
			}
			rep.Created++
			continue
		}
		if len(spec.Labels) == 0 && spec.State == "" {
			rep.Unchanged++
			continue
		}
		if err := f.UpdateIssue(ctx, current.ID, spec.Labels, spec.State); err != nil {
			return rep, fmt.Errorf("failed to update issue %q (#%d) on %s: %w", spec.Title, current.ID, f.Name(), err)
		}
		rep.Updated++
	}

	return rep, nil
}
