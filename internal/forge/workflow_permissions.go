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
)

// PullRequestPermissionFinding names one job a pull request run can reach while a write
// permission is in scope for its token.
type PullRequestPermissionFinding struct {
	Workflow string
	Job      string
	Scope    string
}

func (f PullRequestPermissionFinding) String() string {
	return fmt.Sprintf("%s: job %q is reachable from pull_request and carries %s: write",
		f.Workflow, f.Job, f.Scope)
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
	if !triggersOnPullRequest(&spec.On) {
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
		if !reachableFromPullRequest(job.If) {
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
				Workflow: name, Job: ids[i], Scope: scopes[j]})
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

// reachableFromPullRequest reports whether a pull request run can start a job carrying this
// condition. An unconditional job is reachable, and so is any condition the file does not
// decide: an audit that guessed a job away would hide exactly the credential it looks for.
func reachableFromPullRequest(condition string) bool {
	condition = strings.TrimSpace(strings.ReplaceAll(condition, "\"", "'"))
	if condition == "" {
		return true
	}
	// Parenthesised groups are removed first so what remains is the condition's top-level
	// structure. A top-level || leaves the condition undecided whichever way GitHub binds
	// the two operators, so it is reported rather than resolved.
	top := stripGroups(condition)
	if strings.Contains(top, "||") {
		return true
	}
	conjuncts := strings.Split(top, "&&")
	for i := 0; i < len(conjuncts) && i < maxPermissionScopes; i++ {
		if excludesPullRequest(strings.TrimSpace(conjuncts[i])) {
			return false
		}
	}
	return true
}

// stripGroups removes every parenthesised group from a condition, leaving its top-level
// operators. An unbalanced closing parenthesis is ignored rather than treated as an error:
// the caller wants a reachability answer, and the forge rejects a malformed condition.
func stripGroups(condition string) string {
	var top strings.Builder
	depth := 0
	for i := 0; i < len(condition) && i < maxConditionLength; i++ {
		switch condition[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				top.WriteByte(condition[i])
			}
		}
	}
	return top.String()
}

// excludesPullRequest reports whether one conjunct of a job condition rules the pull
// request event out on its own: a test that the event is not a pull request, or that it is
// some other named event.
func excludesPullRequest(conjunct string) bool {
	rest, named := strings.CutPrefix(conjunct, eventNameExpression)
	if !named {
		return false
	}
	rest = strings.TrimSpace(rest)
	if value, negated := strings.CutPrefix(rest, "!="); negated {
		return namedEvent(value) == pullRequestEvent
	}
	if value, equal := strings.CutPrefix(rest, "=="); equal {
		return namedEvent(value) != pullRequestEvent
	}
	return false
}

// namedEvent returns the event name a comparison's right-hand side spells.
func namedEvent(value string) string {
	return strings.Trim(strings.TrimSpace(value), "'")
}
