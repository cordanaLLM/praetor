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
	ids := sortedJobIDs(spec.Jobs)
	var findings []PullRequestPermissionFinding
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		// The trigger is the job's own, not the workflow's declaration list: an event a
		// job condition fences the job off from does not reach it, and naming it would
		// tell an operator that a correctly fenced event is a hole.
		reaching := reachingEvents(job.If, events)
		if len(reaching) == 0 {
			continue
		}
		scopes := inherited
		// A job's own permissions key configures that job's token by itself; the
		// workflow-level value is not merged into it.
		if own, declared := writeScopes(&job.Permissions); declared {
			scopes = own
		}
		trigger := strings.Join(reaching, " and ")
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

// reachingEvents returns, in the workflow's declaration order, the pull request events that
// can start a job carrying this condition. It is the audit's reachability answer and the
// finding's trigger wording at once, so the two can never disagree: a job fenced off from
// one of two declared events is reported against the other one alone, and a job fenced off
// from both is not reported at all.
func reachingEvents(condition string, events []string) []string {
	condition = strings.TrimSpace(strings.ReplaceAll(condition, "\"", "'"))
	var reaching []string
	for i := 0; i < len(events) && i < maxPermissionScopes; i++ {
		if startsJob(condition, events[i]) {
			reaching = append(reaching, events[i])
		}
	}
	return reaching
}

// startsJob reports whether a run of one named event can start a job carrying this
// condition. An unconditional job is started, and so is any condition the file does not
// decide: an audit that guessed a job away would hide exactly the credential it looks for.
func startsJob(condition, event string) bool {
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
		if excludesEvent(conjuncts[i], event) {
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
//
// The unwrapped text may still hold an operator of its own; reading it as one comparison is
// excludesEvent's business, and it splits before it compares.
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

// excludesEvent reports whether one conjunct of a job condition rules this one event out on
// its own. A conjunct that unwraps to a disjunction rules the event out only when every one
// of its parts does, which is what keeps `(github.event_name == 'push' || github.event_name
// == 'pull_request')` reachable from pull_request while fencing pull_request_target off.
// A conjunct that unwraps to a conjunction rules it out when any one of its parts does, as
// the top-level conjuncts do, which is what keeps the same-repository guard `(github.event_name
// == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository)`
// reachable from pull_request. Read as one comparison, either group handed the comparison a
// value that named no event, and the job carrying it went unaudited.
//
// One level of grouping is resolved, which is what workflow conditions spell. A disjunction
// part that holds a conjunction binds one way or the other depending on operator precedence,
// so it reaches comparisonExcludes whole and is left undecided there; so is a conjunct nested
// deeper. An undecided condition is reported rather than guessed away.
func excludesEvent(conjunct, event string) bool {
	group := unwrapGroup(conjunct)
	if parts := topLevelParts(group, "||"); len(parts) > 1 {
		for i := 0; i < len(parts) && i < maxPermissionScopes; i++ {
			if !comparisonExcludes(parts[i], event) {
				return false
			}
		}
		return true
	}
	parts := topLevelParts(group, "&&")
	for i := 0; i < len(parts) && i < maxPermissionScopes; i++ {
		if comparisonExcludes(parts[i], event) {
			return true
		}
	}
	return false
}

// comparisonExcludes reports whether one `github.event_name` comparison rules this event
// out: a test that it is not this event, or that it is some other named one. A right-hand
// side that is not one quoted literal decides nothing: "some other event" is a guess about
// a value the file does not spell, and it is the guess that hides a reachable job.
func comparisonExcludes(comparison, event string) bool {
	rest, named := strings.CutPrefix(unwrapGroup(comparison), eventNameExpression)
	if !named {
		return false
	}
	rest = strings.TrimSpace(rest)
	if value, negated := strings.CutPrefix(rest, "!="); negated {
		name, literal := namedEvent(value)
		return literal && strings.EqualFold(event, name)
	}
	if value, equal := strings.CutPrefix(rest, "=="); equal {
		name, literal := namedEvent(value)
		return literal && !strings.EqualFold(event, name)
	}
	return false
}

// namedEvent returns the event name a comparison's right-hand side spells, and whether it
// spells exactly one quoted literal. Parentheses around the literal are trimmed: a condition
// may spell the comparison `!= ('pull_request')`, and an unbalanced one survives the split
// that produced this conjunct. Inside the literal a quote is escaped by doubling it, so a
// lone one ends the literal early and whatever follows it is more expression, not more name.
// The comparison is case-insensitive at the caller because GitHub compares strings that way.
func namedEvent(value string) (string, bool) {
	value = strings.Trim(strings.TrimSpace(value), "() \t")
	if len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
		return "", false
	}
	inner := value[1 : len(value)-1]
	if strings.Contains(strings.ReplaceAll(inner, "''", ""), "'") {
		return "", false
	}
	return strings.ReplaceAll(inner, "''", "'"), true
}
