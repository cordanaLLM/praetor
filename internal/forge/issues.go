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

// Compiled regex for Depends-On tags: "Depends-On: [owner/repo]#123"
var dependsOnRegex = regexp.MustCompile(`(?i)depends-on:\s*(([a-zA-Z0-9_\-\.]+)/([a-zA-Z0-9_\-\.]+))?#(\d+)`)

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
			num, err := strconv.Atoi(m[4])
			if err != nil {
				continue
			}

			refs = append(refs, IssueRef{
				Owner:  m[2],
				Repo:   m[3],
				Number: num,
				Raw:    m[0],
			})
		}
	}

	return refs
}

// SyncIssues validates and synchronizes a declarative batch of issues to the target forge.
func SyncIssues(ctx context.Context, f Forge, issues []IssueSpec) error {
	if f == nil {
		return errors.New("forge driver cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before issue sync: %w", err)
	}
	if len(issues) > MaxIssuesLimit {
		return fmt.Errorf("issue count %d exceeds maximum allowed batch limit of %d", len(issues), MaxIssuesLimit)
	}

	if err := f.Authenticate(ctx); err != nil {
		return fmt.Errorf("failed to authenticate with forge %s: %w", f.Name(), err)
	}

	for i := 0; i < len(issues) && i < MaxIssuesLimit; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during issue batch sync at index %d: %w", i, err)
		}

		spec := issues[i]
		if strings.TrimSpace(spec.Title) == "" {
			return fmt.Errorf("issue at index %d has empty title", i)
		}

		// Parse implicit body dependencies and merge into DependsOn
		parsedRefs := ParseIssueDependencies(spec.Body)
		for _, ref := range parsedRefs {
			spec.DependsOn = append(spec.DependsOn, ref.Raw)
		}

		if _, err := f.CreateIssue(ctx, spec); err != nil {
			return fmt.Errorf("failed to create issue '%s' on %s: %w", spec.Title, f.Name(), err)
		}
	}

	return nil
}
