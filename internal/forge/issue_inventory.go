package forge

import "fmt"

// MaxListedIssuesLimit bounds a complete issue inventory independently of the
// smaller MaxIssuesLimit mutation batch. Drivers must report incomplete results.
const MaxListedIssuesLimit = 2000

// IssueListIncompleteError prevents a bounded or truncated inventory from being
// treated as proof that an issue does not exist. Callers must not mutate on it.
type IssueListIncompleteError struct {
	Limit int
}

func (e *IssueListIncompleteError) Error() string {
	return fmt.Sprintf("issue listing is incomplete at the supported limit of %d entries", e.Limit)
}

// IssueTitleConflictError means a trimmed title cannot identify exactly one
// planned or existing issue. Source is "planned" or "existing".
type IssueTitleConflictError struct {
	Title  string
	Source string
}

func (e *IssueTitleConflictError) Error() string {
	return fmt.Sprintf("issue title %q is ambiguous in %s issues", e.Title, e.Source)
}
