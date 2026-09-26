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
	// authorAssociationExpression is the commenter's standing in the repository, the one
	// payload field an issue_comment job can gate on to keep strangers out.
	authorAssociationExpression = "github.event.comment.author_association"
	maxListedAssociations       = 16
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
// issue_comment is audited for the same reason. Any account that can comment on a pull
// request starts it, and it runs the default branch's workflow with the base repository's
// token: adopt.yml carried contents, pull-requests and issues: write for a job any commenter
// started by writing /adopt. A job that admits only trusted commenters, through a conjunct
// restricting github.event.comment.author_association to OWNER, MEMBER or COLLABORATOR, is
// not started on a stranger's say-so and is not reported (admitsOnlyTrustedCommenters).
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
		if excludesEvent(conjuncts[i], event) || admitsOnlyTrustedCommenters(conjuncts[i], event) {
			return false
		}
	}
	return true
}

// admitsOnlyTrustedCommenters reports whether one conjunct keeps an issue_comment run from
// starting the job unless the commenter is trusted (trustedAssociation). Two spellings are
// recognised: contains(fromJSON('[...]'), github.event.comment.author_association) over a
// list of trusted values, and an equality test of that field against a trusted value, or a
// disjunction of such tests. Every other spelling, a reversed comparison or a denylist such
// as `!= 'NONE'` included, is not recognised and leaves the job reported: an audit that
// guessed a gate into existence would hide exactly the credential it looks for.
//
// The gate decides issue_comment alone. Whether a pull_request or pull_request_target run
// reaches the job stays excludesEvent's business, whatever the conjunct says about comments.
func admitsOnlyTrustedCommenters(conjunct, event string) bool {
	if event != issueCommentEvent {
		return false
	}
	conjunct = unwrapGroup(conjunct)
	if trustedAssociationList(conjunct) {
		return true
	}
	parts := topLevelParts(conjunct, "||")
	for i := 0; i < len(parts) && i < maxPermissionScopes; i++ {
		if !trustedAssociationEquality(parts[i]) {
			return false
		}
	}
	return true
}

// trustedAssociationEquality reports whether a comparison tests the commenter's
// association for equality with a trusted value.
func trustedAssociationEquality(comparison string) bool {
	rest, named := strings.CutPrefix(unwrapGroup(comparison), authorAssociationExpression)
	if !named {
		return false
	}
	value, equal := strings.CutPrefix(strings.TrimSpace(rest), "==")
	if !equal {
		return false
	}
	name, literal := comparedLiteral(value)
	return literal && trustedAssociation(name)
}

// trustedAssociationList reports whether a conjunct is one contains() call asking whether
// the commenter's association is in a fromJSON list that holds trusted values only. The
// search subject has to be the association field itself, and the list has to be a JSON
// array: contains() over a plain string is a substring test.
func trustedAssociationList(conjunct string) bool {
	arguments, called := wholeCall(conjunct, "contains")
	if !called {
		return false
	}
	split := strings.LastIndex(arguments, ",")
	if split < 0 || strings.TrimSpace(arguments[split+1:]) != authorAssociationExpression {
		return false
	}
	list, parsed := wholeCall(strings.TrimSpace(arguments[:split]), "fromJSON")
	return parsed && trustedAssociationArray(list)
}

// wholeCall returns the argument text of a call to the named function when that call is the
// whole text. GitHub matches function names without regard to case, so fromJson and fromJSON
// are one function. The arguments have to balance on their own, which is what tells
// `f(a) || f(b)`, two calls, apart from one call whose last argument ends in a parenthesis.
func wholeCall(text, function string) (string, bool) {
	prefix := function + "("
	if len(text) <= len(prefix) || !strings.EqualFold(text[:len(prefix)], prefix) {
		return "", false
	}
	arguments := text[len(prefix):]
	closing := strings.LastIndex(arguments, ")")
	if closing != len(arguments)-1 || !balancedGroups(arguments[:closing]) {
		return "", false
	}
	return arguments[:closing], true
}

// trustedAssociationArray reports whether a quoted JSON array literal lists at least one
// value and trusted values only. reachingEvents has already turned every double quote into
// a single one, so the elements arrive as 'OWNER' rather than "OWNER".
func trustedAssociationArray(literal string) bool {
	body, quoted := strings.CutPrefix(strings.TrimSpace(literal), "'[")
	body, closed := strings.CutSuffix(body, "]'")
	if !quoted || !closed || strings.TrimSpace(body) == "" {
		return false
	}
	values := strings.Split(body, ",")
	if len(values) > maxListedAssociations {
		return false
	}
	for i := 0; i < len(values) && i < maxListedAssociations; i++ {
		if !trustedAssociation(strings.Trim(strings.TrimSpace(values[i]), "'")) {
			return false
		}
	}
	return true
}

// trustedAssociation reports whether an author_association value names someone the
// repository granted access: its owner, a member of the owning organization, or an invited
// collaborator. The other values GitHub defines (CONTRIBUTOR, FIRST_TIMER,
// FIRST_TIME_CONTRIBUTOR, MANNEQUIN and NONE, per the author-association schema in GitHub's
// REST API description) are all within a stranger's reach. GitHub compares strings without
// regard to case, so 'owner' admits exactly whom 'OWNER' does.
func trustedAssociation(value string) bool {
	switch strings.ToUpper(value) {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	default:
		return false
	}
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
		name, literal := comparedLiteral(value)
		return literal && strings.EqualFold(event, name)
	}
	if value, equal := strings.CutPrefix(rest, "=="); equal {
		name, literal := comparedLiteral(value)
		return literal && !strings.EqualFold(event, name)
	}
	return false
}

// comparedLiteral returns the literal a comparison's right-hand side spells, and whether it
// spells exactly one quoted literal: an event name for an event test, an association for a
// commenter gate. Parentheses around the literal are trimmed: a condition may spell the
// comparison `!= ('pull_request')`, and an unbalanced one survives the split that produced
// this conjunct. Inside the literal a quote is escaped by doubling it, so a lone one ends the
// literal early and whatever follows it is more expression, not more literal. Callers compare
// the result without regard to case because GitHub compares strings that way.
func comparedLiteral(value string) (string, bool) {
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
