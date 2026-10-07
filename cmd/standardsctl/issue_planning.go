package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/milestone"
	"github.com/cordanaLLM/praetor/internal/util"
)

// loadFleetMilestones reads every selected repository's milestones, with their issue
// counts, into the engine. A malformed coordinate or a failed listing stops the load: a
// repository whose milestones were never read is never reported as reconciled.
func loadFleetMilestones(ctx context.Context, token, endpoint string, repos []string, engine *forge.ReconcileEngine) error {
	for i := 0; i < len(repos) && i < config.MaxReconcileRepos; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		owner, name, err := util.SplitGitHubRepository(repos[i])
		if err != nil {
			return fmt.Errorf("reconcile %s: %w", repos[i], err)
		}
		remotes, err := milestone.FetchRemoteMilestones(ctx, owner, name, token, endpoint)
		if err != nil {
			return fmt.Errorf("list milestones for %s: %w", repos[i], err)
		}
		milestones := make([]forge.PlanningMilestone, 0, len(remotes))
		for _, rm := range remotes {
			milestones = append(milestones, planningMilestone(rm))
		}
		engine.TrackMilestones(repos[i], milestones)
	}
	return nil
}

// planningMilestone is the planning sync's view of one forge milestone.
func planningMilestone(rm milestone.RemoteMilestone) forge.PlanningMilestone {
	return forge.PlanningMilestone{Number: rm.Number, Title: rm.Title, State: rm.State,
		OpenIssues: rm.OpenIssues, ClosedIssues: rm.ClosedIssues}
}

// githubPlanningForge binds the planning sync to one GitHub repository: its issues
// through the forge driver and its milestones through the milestone package, the one
// GitHub client each of them has.
type githubPlanningForge struct {
	issues                       *forge.GitHubDriver
	owner, repo, token, endpoint string
}

func (g githubPlanningForge) GetIssue(ctx context.Context, number int) (forge.IssueSpec, error) {
	return g.issues.GetIssue(ctx, number)
}

func (g githubPlanningForge) EditIssueBody(ctx context.Context, number int, body string) error {
	return g.issues.EditIssueBody(ctx, number, body)
}

func (g githubPlanningForge) CloseIssue(ctx context.Context, number int) error {
	return g.issues.UpdateIssue(ctx, number, nil, "closed")
}

func (g githubPlanningForge) GetMilestone(ctx context.Context, number int) (forge.PlanningMilestone, error) {
	rm, err := milestone.FetchRemoteMilestone(ctx, g.owner, g.repo, g.token, g.endpoint, number)
	if err != nil {
		return forge.PlanningMilestone{}, err
	}
	return planningMilestone(rm), nil
}

func (g githubPlanningForge) CloseMilestone(ctx context.Context, number int) error {
	_, err := milestone.CloseRemoteMilestone(ctx, g.owner, g.repo, g.token, g.endpoint, number)
	return err
}

// planningForgeFor binds the planning sync's writes to the GitHub repository each names.
func planningForgeFor(token, endpoint string) forge.PlanningForgeFor {
	return func(repo string) (forge.PlanningForge, error) {
		owner, name, err := util.SplitGitHubRepository(repo)
		if err != nil {
			return nil, fmt.Errorf("planning write for %s: %w", repo, err)
		}
		driver := forge.NewGitHubDriver(token, endpoint)
		driver.SetRepository(owner, name)
		return githubPlanningForge{issues: driver, owner: owner, repo: name, token: token, endpoint: endpoint}, nil
	}
}

// planningTags label each write status in the run summary. A dry run's planned write is a
// [PLAN] line: the write the same run would make with --apply.
var planningTags = map[string]string{
	forge.PlanningPlanned:  "PLAN",
	forge.PlanningDeferred: "DEFERRED",
	forge.PlanningApplied:  "APPLIED",
	forge.PlanningSkipped:  "SKIPPED",
	forge.PlanningFailed:   "FAILED",
}

// printPlanningSummary prints the planning sync's write log, one line per write with its
// outcome, then every finding it left as it was.
func printPlanningSummary(rep *forge.PlanningReport, apply bool) {
	mode := "dry run, nothing written"
	if apply {
		mode = "applied"
	}
	fmt.Printf("\n=== Planning Sync (%s): %d writes, cap %d | %d findings ===\n",
		mode, len(rep.Writes), rep.WriteCap, len(rep.Findings))
	for _, w := range rep.Writes {
		fmt.Printf("  [%s] %s\n", planningTags[w.Status], describePlanningWrite(w))
	}
	for _, f := range rep.Findings {
		fmt.Printf("  [DRIFT] %s#%d %s: %s\n", f.Repo, f.Number, f.Kind, f.Detail)
	}
	if deferred := rep.Count(forge.PlanningDeferred); deferred > 0 {
		fmt.Printf("[INFO] %d planning writes are deferred by the write cap; the next run makes them.\n", deferred)
	}
}

// describePlanningWrite names one write: its target, its action and why.
func describePlanningWrite(w forge.PlanningWrite) string {
	var line string
	switch w.Kind {
	case forge.PlanningTick:
		line = fmt.Sprintf("%s#%d tick the boxes of closed children %s", w.Repo, w.Number, strings.Join(w.Children, ", "))
	case forge.PlanningCloseParent:
		line = fmt.Sprintf("%s#%d close parent %q", w.Repo, w.Number, w.Title)
	case forge.PlanningCloseMilestone:
		line = fmt.Sprintf("%s milestone #%d close %q", w.Repo, w.Number, w.Title)
	default:
		line = fmt.Sprintf("%s #%d %s", w.Repo, w.Number, w.Kind)
	}
	if w.Detail != "" {
		line += ": " + w.Detail
	}
	return line
}
