package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	writeLevel          = "write"
	writeAllShorthand   = "write-all"
	allScopes           = "every scope (write-all)"
	eventNameExpression = "github.event_name"
	maxPermissionScopes = 32
	maxConditionLength  = 4096
	maxGroupNesting     = 8
)

// PullRequestPermissionFinding names one job a pull request run can reach while a write
// permission is in scope for its token. Trigger names the events that reach it, so a
// pull_request_target finding is not read as a pull_request one.
type PullRequestPermissionFinding struct {
	Workflow string
	Job      string
	Scope    string
	Trigger  string
}

func (f PullRequestPermissionFinding) String() string {
	return fmt.Sprintf("%s: job %q is reachable from %s and carries %s: write",
		f.Workflow, f.Job, f.Trigger, f.Scope)
}

// AuditPullRequestPermissions reports every job a pull request run can reach while one of
// its token's scopes is writable.
//
// A pull request is the one event a repository runs on a contributor's say-so, and a fork
// branch is contributor-controlled content. A write scope declared for the whole workflow
// therefore reaches jobs the writing step never runs in: pages.yml declared pages:write
// and id-token:write at workflow level for a deploy job that is fenced off from pull
// requests, so every documentation pull request carried credentials to create a Pages
// deployment (#292). The permission belongs on the job that uses it.
//
// pull_request_target is audited beside pull_request. It runs a contributor's branch in
// the base repository's context with the base repository's token, so it is the strictly
// worse form of the same defect and must not be the one event the guard cannot see.
//
// A job that no pull request can start may declare whatever it needs, which is why the
// audit decides reachability rather than reporting every write scope in the file. Only a
// declared write is reported: a workflow with no permissions key at all inherits the
// repository default, which the manifest's actions policy governs and
// EvaluateActionsPermissions audits against the live forge.
func AuditPullRequestPermissions(ctx context.Context, repoPath string) ([]PullRequestPermissionFinding, error) {
	if ctx == nil {
		return nil, errors.New("pull request permission audit requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	var findings []PullRequestPermissionFinding
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		found, err := auditWorkflowPullRequestPermissions(files[i].Name, files[i].Data)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}
	return findings, nil
}

// auditWorkflowPullRequestPermissions audits one already-read workflow document.
func auditWorkflowPullRequestPermissions(name string, data []byte) ([]PullRequestPermissionFinding, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("workflow %s: parse: %w", name, err)
	}
	events := pullRequestTriggers(&spec.On)
	if len(events) == 0 {
		return nil, nil
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return nil, fmt.Errorf("workflow %s exceeds %d jobs", name, maxJobsPerFile)
	}
	inherited, _ := writeScopes(&spec.Permissions)
	trigger := strings.Join(events, " and ")
	ids := sortedJobIDs(spec.Jobs)
	var findings []PullRequestPermissionFinding
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		if !reachableFromPullRequest(job.If, events) {
			continue
		}
		scopes := inherited
		// A job's own permissions key configures that job's token by itself; the
		// workflow-level value is not merged into it.
		if own, declared := writeScopes(&job.Permissions); declared {
			scopes = own
		}
		for j := 0; j < len(scopes) && j < maxPermissionScopes; j++ {
			findings = append(findings, PullRequestPermissionFinding{
				Workflow: name, Job: ids[i], Scope: scopes[j], Trigger: trigger})
		}
	}
	return findings, nil
}

// writeScopes returns the writable scopes a permissions node grants and whether the node
// declares anything at all. An absent key inherits; an empty mapping grants nothing, which
// is a declaration and not an inheritance.
func writeScopes(permissions *yaml.Node) ([]string, bool) {
	switch permissions.Kind {
	case yaml.ScalarNode:
		if permissions.Value == writeAllShorthand {
			return []string{allScopes}, true
		}
		return nil, permissions.Value != ""
	case yaml.MappingNode:
		var scopes []string
		for i := 0; i+1 < len(permissions.Content) && i < 2*maxPermissionScopes; i += 2 {
			if permissions.Content[i+1].Value == writeLevel {
				scopes = append(scopes, permissions.Content[i].Value)
			}
		}
		return scopes, true
	default:
		return nil, false
	}
}

// reachableFromPullRequest reports whether a run of one of the workflow's pull request
// events can start a job carrying this condition. An unconditional job is reachable, and so
// is any condition the file does not decide: an audit that guessed a job away would hide
// exactly the credential it looks for.
func reachableFromPullRequest(condition string, events []string) bool {
	condition = strings.TrimSpace(strings.ReplaceAll(condition, "\"", "'"))
	if condition == "" {
		return true
	}
	// A top-level || leaves the condition undecided whichever way GitHub binds the two
	// operators, so it is reported rather than resolved. An || inside a parenthesised group
	// is not a top-level one: `github.repository == (vars.X || 'a/b')` decides nothing
	// about the event.
	if len(topLevelParts(condition, "||")) > 1 {
		return true
	}
	conjuncts := topLevelParts(condition, "&&")
	for i := 0; i < len(conjuncts) && i < maxPermissionScopes; i++ {
		if excludesPullRequest(conjuncts[i], events) {
			return false
		}
	}
	return true
}

// topLevelParts splits a condition on one operator, ignoring the occurrences inside
// parenthesised groups, and returns the parts with their group text intact.
//
// The groups are kept rather than deleted because a whole conjunct may be one. Deleting
// them took the event test in `(github.event_name != 'pull_request') && ...` out of the
// condition altogether and reported a correctly fenced job, which the repository-wide
// guard test turns into a build failure. An unbalanced closing parenthesis counts as depth
// zero rather than as an error: the caller wants a reachability answer, and the forge
// rejects a malformed condition before it ever runs.
func topLevelParts(condition, operator string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(condition) && i < maxConditionLength; i++ {
		if condition[i] == '(' {
			depth++
			continue
		}
		if condition[i] == ')' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && strings.HasPrefix(condition[i:], operator) {
			parts = append(parts, condition[start:i])
			start = i + len(operator)
		}
	}
	return append(parts, condition[start:])
}

// unwrapGroup removes the parentheses enclosing a whole conjunct, so a fully parenthesised
// event test decides reachability exactly as the bare spelling does. A conjunct that merely
// starts and ends with a parenthesis without being one group, such as `(a) || (b)`, is left
// as it is: its inner text is unbalanced, and it decides nothing this function may act on.
func unwrapGroup(conjunct string) string {
	conjunct = strings.TrimSpace(conjunct)
	for i := 0; i < maxGroupNesting; i++ {
		if !strings.HasPrefix(conjunct, "(") || !strings.HasSuffix(conjunct, ")") {
			return conjunct
		}
		inner := conjunct[1 : len(conjunct)-1]
		if !balancedGroups(inner) {
			return conjunct
		}
		conjunct = strings.TrimSpace(inner)
	}
	return conjunct
}

// balancedGroups reports whether every parenthesis in the text opens before it closes and
// closes before the text ends.
func balancedGroups(text string) bool {
	depth := 0
	for i := 0; i < len(text) && i < maxConditionLength; i++ {
		if text[i] == '(' {
			depth++
		}
		if text[i] == ')' {
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// excludesPullRequest reports whether one conjunct of a job condition rules every pull
// request event the workflow declares out on its own: a test that the event is not the only
// such event the workflow has, or that it is some other named event.
func excludesPullRequest(conjunct string, events []string) bool {
	rest, named := strings.CutPrefix(unwrapGroup(conjunct), eventNameExpression)
	if !named {
		return false
	}
	rest = strings.TrimSpace(rest)
	if value, negated := strings.CutPrefix(rest, "!="); negated {
		// Excluding pull_request leaves a workflow that also triggers on
		// pull_request_target reachable by that second event.
		return len(events) == 1 && events[0] == namedEvent(value)
	}
	if value, equal := strings.CutPrefix(rest, "=="); equal {
		return !declaresEvent(events, namedEvent(value))
	}
	return false
}

// declaresEvent reports whether a workflow's pull request events include the named one.
func declaresEvent(events []string, event string) bool {
	for i := 0; i < len(events) && i < maxPermissionScopes; i++ {
		if events[i] == event {
			return true
		}
	}
	return false
}

// namedEvent returns the event name a comparison's right-hand side spells. Parentheses are
// trimmed with the quotes: a condition may spell the comparison `!= ('pull_request')`, and
// an unbalanced one survives the split that produced this conjunct.
func namedEvent(value string) string {
	return strings.Trim(strings.TrimSpace(value), "'()")
}
