package adopt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/paperclip"
	"gopkg.in/yaml.v3"
)

const (
	rulesetFile       = ".github/rulesets/main.json"
	labelsFile        = ".config/labels.yaml"
	paperclipFile     = ".paperclip/harness.json"
	auditorAgentFile  = ".agents/agents/repo-auditor.md"
	gatekeeperFile    = ".agents/agents/repo-gatekeeper.md"
	workflowsDir      = ".github/workflows"
	maxWorkflowFiles  = 64
	maxJobsPerFile    = 64
	pullRequestEvent  = "pull_request"
	workflowPathsKey  = "paths"
	workflowIgnoreKey = "paths-ignore"
)

// rulesetRule is one rule of a GitHub repository ruleset.
type rulesetRule struct {
	Type       string         `json:"type"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// rulesetDocument is the declarative branch protection ruleset praetor scaffolds.
type rulesetDocument struct {
	Name        string         `json:"name"`
	Target      string         `json:"target"`
	Enforcement string         `json:"enforcement"`
	Conditions  map[string]any `json:"conditions"`
	Rules       []rulesetRule  `json:"rules"`
}

// buildRulesetJSON renders the branch protection ruleset. Required status checks are
// limited to contexts the repository's own workflows report on pull requests; with none
// the status-check rule is omitted so the ruleset can never block every pull request.
func buildRulesetJSON(contexts []string) (string, error) {
	doc := rulesetDocument{
		Name:        "praetor-main-protection",
		Target:      "branch",
		Enforcement: "active",
		Conditions: map[string]any{
			"ref_name": map[string]any{
				"include": []string{"refs/heads/main", "refs/heads/lts-*"},
				"exclude": []string{},
			},
		},
		Rules: []rulesetRule{
			{Type: "deletion"},
			{Type: "non_fast_forward"},
			{Type: "required_linear_history"},
			{Type: "pull_request", Parameters: map[string]any{
				"required_approving_review_count":   0,
				"dismiss_stale_reviews_on_push":     true,
				"require_code_owner_review":         false,
				"require_last_push_approval":        false,
				"required_review_thread_resolution": true,
			}},
		},
	}
	if len(contexts) > 0 {
		checks := make([]map[string]string, 0, len(contexts))
		for i := 0; i < len(contexts) && i < maxWorkflowFiles*maxJobsPerFile; i++ {
			checks = append(checks, map[string]string{"context": contexts[i]})
		}
		doc.Rules = append(doc.Rules, rulesetRule{Type: "required_status_checks", Parameters: map[string]any{
			"strict_required_status_checks_policy": true,
			"required_status_checks":               checks,
		}})
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal ruleset: %w", err)
	}
	return string(data) + "\n", nil
}

// workflowSpec is the subset of a GitHub Actions workflow needed to derive contexts.
type workflowSpec struct {
	On   yaml.Node              `yaml:"on"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

// workflowJob is the subset of a workflow job needed to derive its check context.
type workflowJob struct {
	Name string `yaml:"name"`
	If   string `yaml:"if"`
}

// requiredStatusContexts lists the status-check contexts the repository's workflows
// report on every pull request: unconditional jobs of workflows whose pull_request
// trigger carries no path filter. Contexts are ordered by workflow file name.
func requiredStatusContexts(repoPath string) ([]string, error) {
	dir, err := repoFile(repoPath, workflowsDir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", workflowsDir, err)
	}
	var contexts []string
	for i := 0; i < len(entries) && i < maxWorkflowFiles; i++ {
		name := entries[i].Name()
		if entries[i].IsDir() || !isWorkflowFile(name) {
			continue
		}
		jobs, err := workflowPullRequestContexts(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", name, err)
		}
		contexts = append(contexts, jobs...)
	}
	return contexts, nil
}

func isWorkflowFile(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}

// workflowPullRequestContexts returns the check contexts of one workflow file, or nil
// when the workflow does not run unconditionally on pull requests.
func workflowPullRequestContexts(path string) ([]string, error) {
	// #nosec G304 -- path is a directory entry under the confined .github/workflows dir.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if !triggersOnEveryPullRequest(&spec.On) {
		return nil, nil
	}
	ids := make([]string, 0, len(spec.Jobs))
	for id := range spec.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var contexts []string
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		if strings.TrimSpace(job.If) != "" {
			continue
		}
		if job.Name != "" {
			contexts = append(contexts, job.Name)
			continue
		}
		contexts = append(contexts, ids[i])
	}
	return contexts, nil
}

// triggersOnEveryPullRequest reports whether an "on" node declares a pull_request
// trigger without paths filters (a filtered trigger does not report on every PR).
func triggersOnEveryPullRequest(on *yaml.Node) bool {
	switch on.Kind {
	case yaml.ScalarNode:
		return on.Value == pullRequestEvent
	case yaml.SequenceNode:
		for i := 0; i < len(on.Content) && i < maxJobsPerFile; i++ {
			if on.Content[i].Value == pullRequestEvent {
				return true
			}
		}
		return false
	case yaml.MappingNode:
		return mappingHasUnfilteredPullRequest(on)
	default:
		return false
	}
}

// mappingHasUnfilteredPullRequest inspects an "on:" mapping for a pull_request entry
// whose own mapping carries neither paths nor paths-ignore.
func mappingHasUnfilteredPullRequest(on *yaml.Node) bool {
	for i := 0; i+1 < len(on.Content) && i < 2*maxJobsPerFile; i += 2 {
		if on.Content[i].Value != pullRequestEvent {
			continue
		}
		value := on.Content[i+1]
		if value.Kind != yaml.MappingNode {
			return true
		}
		for j := 0; j+1 < len(value.Content) && j < 2*maxJobsPerFile; j += 2 {
			key := value.Content[j].Value
			if key == workflowPathsKey || key == workflowIgnoreKey {
				return false
			}
		}
		return true
	}
	return false
}

func reconcileBranchRuleset(_ context.Context, s *adoptSession) error {
	contexts, err := requiredStatusContexts(s.repoPath)
	if err != nil {
		return err
	}
	content, err := buildRulesetJSON(contexts)
	if err != nil {
		return err
	}
	_, err = s.scaffoldFile(scaffold{
		rel:      rulesetFile,
		perm:     filePerm,
		content:  []byte(content),
		force:    true,
		created:  fmt.Sprintf("Scaffolded declarative branch protection ruleset (%d required status checks derived from workflows)", len(contexts)),
		verified: "Existing branch protection ruleset verified present",
	})
	return err
}

func reconcileLabels(_ context.Context, s *adoptSession) error {
	_, err := s.scaffoldFile(scaffold{
		rel:      labelsFile,
		perm:     filePerm,
		content:  []byte(buildDefaultLabelsYAML()),
		force:    true,
		created:  "Scaffolded repository label taxonomy",
		verified: "Existing repository label taxonomy verified present",
	})
	return err
}

func buildDefaultLabelsYAML() string {
	return `version: 1
labels:
  - name: "hiss-violation"
    color: "d73a4a"
    description: "Code introduces a regression against HISS-16 invariants"
  - name: "hiss-waiver"
    color: "fbca04"
    description: "Requires cryptographically signed waiver approval"
  - name: "standards-sync"
    color: "0075ca"
    description: "Automated configuration sync generated by cordana-standards[bot]"
`
}

func reconcilePaperclip(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, paperclipFile)
	if err != nil {
		return err
	}
	if fileExists(full) && !s.opts.Force {
		s.report.recordReconciled(paperclipFile, "Existing Paperclip agent runtime harness verified present")
		return nil
	}
	if !s.opts.DryRun {
		harness, err := paperclip.SynthesizeHarness(ctx, s.repoPath)
		if err != nil {
			return fmt.Errorf("synthesize paperclip harness: %w", err)
		}
		if err := paperclip.WriteHarness(harness, s.repoPath); err != nil {
			return fmt.Errorf("write paperclip harness: %w", err)
		}
	}
	s.report.recordCreated(paperclipFile, "Scaffolded Paperclip agent runtime harness and AGit rules")
	return nil
}

func reconcileAgentDefinitions(_ context.Context, s *adoptSession) error {
	if _, err := s.scaffoldFile(scaffold{
		rel:      auditorAgentFile,
		perm:     filePerm,
		content:  []byte(defaultAuditorAgentMD),
		force:    true,
		created:  "Scaffolded repository auditor agent definition",
		verified: "Existing repository auditor agent definition verified present",
	}); err != nil {
		return err
	}
	_, err := s.scaffoldFile(scaffold{
		rel:      gatekeeperFile,
		perm:     filePerm,
		content:  []byte(defaultGatekeeperAgentMD),
		force:    true,
		created:  "Scaffolded repository gatekeeper agent definition",
		verified: "Existing repository gatekeeper agent definition verified present",
	})
	return err
}

const defaultAuditorAgentMD = `---
name: repo-auditor
description: "Autonomous agent for repository HISS invariant sweeps and standardsctl compliance."
mainAgent: true
subagent: true
commandExecutionPolicy: auto
---

# Repository Governance Auditor Persona

You are the authoritative repository governance auditor. Your purpose is to run autonomous sweeps across codebases and git commits to guarantee 100% adherence to declared standards.

## Execution Command
` + "```bash\nstandardsctl audit\n```\n"

const defaultGatekeeperAgentMD = `---
name: repo-gatekeeper
description: "Autonomous subagent for dependency verification, SCA security scans, and worktree gating."
mainAgent: true
subagent: true
commandExecutionPolicy: auto
---

# Repository Gatekeeper Persona

You are the repository gatekeeper. Your mission is to strictly enforce the anti-direct-merge policy and verify all verification gates before shipping.

## Execution Command
` + "```bash\nstandardsctl gate run --target=. --dry-run\n```\n"
