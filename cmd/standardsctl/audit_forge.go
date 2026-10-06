// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// actionsForgeHost is the only forge the live Actions and branch protection checks read:
	// the origin remote must name the manifest's repository on it.
	actionsForgeHost = "github.com"
	// actionsForgeTimeout bounds every live forge read of one audit or plan run, the Actions
	// reads and the branch protection read together (HISS-02).
	actionsForgeTimeout = 3 * time.Minute
	// offlineFlagUsage is the --offline help text of audit and plan.
	offlineFlagUsage = "Read nothing from the forge: the live Actions permission, workflow run and branch protection checks report as not made"
)

var (
	// actionsForgeEndpoint is the GitHub REST API the live forge checks read. Tests point it
	// at a stand-in forge; nothing else changes it.
	actionsForgeEndpoint = util.DefaultGitHubAPIBase
	// resolveActionsToken finds the token the live forge checks read with: GITHUB_TOKEN,
	// GH_TOKEN, then the gh CLI session. TestMain replaces it, so no test reads the operator's
	// credential or asks the real forge.
	resolveActionsToken = func(ctx context.Context) string { return util.ResolveAuthTokenContext(ctx, "") }
)

// actionsForge is the live forge reader of one audit or plan run, or why there is none: the
// Actions reads and the branch protection read go through the same driver.
type actionsForge struct {
	reader     forge.ActionsReader
	protection protectionReader
	notMade    string
}

// openActionsForge decides whether the live forge checks may ask the forge: not with
// --offline, not without the manifest's repository identity, not unless the origin remote
// names that repository on github.com (so a read never describes another repository), and
// not without a token. Each refusal is the reason the report gives for the check not made.
func openActionsForge(ctx context.Context, rootDir string, repo config.RepositoryMetadata, offline bool) actionsForge {
	if offline {
		return actionsForge{notMade: "--offline"}
	}
	if repo.Owner == "" || repo.Name == "" {
		return actionsForge{notMade: "the manifest sets no repository.owner and repository.name"}
	}
	if err := verifyOriginIdentity(ctx, rootDir, actionsForgeHost, repo.Owner, repo.Name); err != nil {
		return actionsForge{notMade: err.Error()}
	}
	token := resolveActionsToken(ctx)
	if token == "" {
		return actionsForge{notMade: "no forge token: set GITHUB_TOKEN or GH_TOKEN, or sign in with gh"}
	}
	driver := forge.NewGitHubDriver(token, actionsForgeEndpoint)
	driver.SetRepository(repo.Owner, repo.Name)
	return actionsForge{reader: driver, protection: driver}
}

// readActionsPermissions compares declared with the live workflow permissions, or returns why
// the comparison was not made.
func readActionsPermissions(ctx context.Context, declared config.ActionsPolicy, live actionsForge) (forge.ActionsFinding, string) {
	if live.reader == nil {
		return forge.ActionsFinding{}, live.notMade
	}
	state, err := live.reader.WorkflowPermissions(ctx)
	if err != nil {
		return forge.ActionsFinding{}, err.Error()
	}
	return forge.EvaluateLiveActionsPermissions(declared, state), ""
}

// auditLiveForge is the audit gate that reads the forge (#611, #612, #159). The live workflow
// permissions are compared with overrides.actions and fail the audit on drift. Each
// workflow's recent runs on the default branch are reported, and never fail it: the operator
// chose read-and-report for them. The branch protection the default branch enforces is compared
// with the declared policy and fails the audit on drift (auditLiveBranchProtection). Every check
// runs whatever an earlier one found, and the failures are joined, so one drift never hides
// another. A check the forge did not answer is named as not made, never passed. sync --remote
// reconciles the branch protection and neither Actions setting.
func auditLiveForge(ctx context.Context, manifest *config.Manifest, rootDir string, policy *config.ResolvedPolicy, offline bool) error {
	ctx, cancel := context.WithTimeout(ctx, actionsForgeTimeout)
	defer cancel()
	live := openActionsForge(ctx, rootDir, manifest.Repository, offline)
	permissions := auditActionsPermissions(ctx, manifest.Overrides.Actions, live)
	auditWorkflowRuns(ctx, manifest, rootDir, live)
	return errors.Join(permissions, auditLiveBranchProtection(ctx, manifest, rootDir, policy, live))
}

// auditLiveBranchProtection compares the branch protection the default branch enforces on the
// forge, from its rulesets and its legacy protection object alike, with the declared policy and
// the status checks the default branch's workflows report (auditProtectionTarget), through the
// comparison plan --remote prints (compareLiveProtection). The committed ruleset gate reads only the file
// (adopt.AuditBranchProtectionWithPolicy), so a ruleset GitHub never applied passed it (#159).
// Drift fails the audit and names every property that differs; a setting stricter than declared
// passes. Like the committed ruleset gate, it compares nothing when adoption.decline declines
// the branch-ruleset step or the policy requires no ruleset. A read the forge did not answer is a
// check not made ([SKIP]); a comparison that cannot be evaluated, such as an invalid review
// requirement or more required checks than a ruleset holds, is a local defect and fails.
func auditLiveBranchProtection(ctx context.Context, manifest *config.Manifest, rootDir string, policy *config.ResolvedPolicy, live actionsForge) error {
	notCompared, err := liveProtectionNotCompared(manifest, policy, live)
	if err != nil {
		return err
	}
	if notCompared != "" {
		fmt.Println(notCompared)
		return nil
	}
	target, notes, err := auditProtectionTarget(ctx, rootDir, manifest, policy.BranchProtection)
	if err != nil {
		return fmt.Errorf("[FAIL] Live branch protection audit failed: %w", err)
	}
	protection, findings, err := compareLiveProtection(ctx, live.protection, target)
	var unread *forgeReadError
	switch {
	case errors.As(err, &unread):
		fmt.Printf("[SKIP] Live branch protection not compared with the forge: %v\n", err)
		return nil
	case err != nil:
		return fmt.Errorf("[FAIL] Live branch protection audit failed: %w", err)
	}
	printLines(notes)
	return liveProtectionVerdict(target, protection, findings)
}

// auditProtectionTarget is what the audit compares the live branch protection with: the
// liveProtectionTarget plan --remote compares with, except for the status checks. Those are the
// checks the workflows of origin's default branch report as this checkout last fetched it
// (forge.RequiredStatusContextsAt), not this checkout's: the branch can require a job only once
// the job is on it, and requiring it earlier blocks every open pull request that lacks it. A
// check this checkout adds is named as not compared yet, and a check the default branch gained
// since this checkout branched off is compared. Without origin's default branch in the checkout,
// or with a workflow of it the checkout does not hold (forge.ErrCommittedWorkflowAbsent), the
// checks are this checkout's, and a note names that substitution (workingTreeChecksNote). The
// notes are printed with the verdict.
func auditProtectionTarget(ctx context.Context, rootDir string, manifest *config.Manifest, policy config.BranchProtectionPolicy) (protectionTarget, []string, error) {
	target, err := liveProtectionTarget(ctx, rootDir, manifest, policy)
	if err != nil {
		return protectionTarget{}, nil, err
	}
	ref := "origin/" + target.branch
	commit, err := util.ResolveGitCommit(ctx, rootDir, "refs/remotes/"+ref)
	if err != nil {
		return protectionTarget{}, nil, err
	}
	if commit == "" {
		return target, []string{workingTreeChecksNote(target.branch, ref+" is not in this checkout")}, nil
	}
	committed, err := forge.RequiredStatusContextsAt(ctx, rootDir, commit, target.repository)
	if errors.Is(err, forge.ErrCommittedWorkflowAbsent) {
		return target, []string{workingTreeChecksNote(target.branch, fmt.Sprintf(
			"the workflows of %s (%s) are not all in this checkout, as in a partial clone (%v)", ref, shortHead(commit), err))}, nil
	}
	if err != nil {
		return protectionTarget{}, nil, fmt.Errorf("discover the required status checks of %s: %w", ref, err)
	}
	notes := []string{fmt.Sprintf("[INFO] Live branch protection of %s: status checks compared with the ones %s (%s) reports.",
		target.branch, ref, shortHead(commit))}
	added := slices.DeleteFunc(slices.Clone(target.contexts), func(check string) bool { return slices.Contains(committed, check) })
	if len(added) > 0 {
		notes = append(notes, fmt.Sprintf("[INFO] Live branch protection of %s: not compared yet, as %s does not report them: %s. "+
			"Once they are on %s, 'praetorctl sync --remote' requires them.", target.branch, ref, strings.Join(added, ", "), target.branch))
	}
	target.contexts = committed
	return target, notes, nil
}

// workingTreeChecksNote is the line that names the substitution auditProtectionTarget makes when
// it cannot read the status checks of the default branch, and why.
func workingTreeChecksNote(branch, why string) string {
	return fmt.Sprintf("[INFO] Live branch protection of %s: %s, so the status checks are compared with the ones "+
		"this checkout's workflows report.", branch, why)
}

// liveProtectionNotCompared returns the line of a live branch protection check that is not
// made, and why: the branch-ruleset step declined, a policy that requires no ruleset, or a forge
// the audit may not ask (openActionsForge). It returns "" when the comparison runs.
func liveProtectionNotCompared(manifest *config.Manifest, policy *config.ResolvedPolicy, live actionsForge) (string, error) {
	decline, err := adopt.AuditDecline(manifest, "branch-ruleset")
	switch {
	case err != nil:
		return "", fmt.Errorf("[FAIL] Live branch protection audit failed: %w", err)
	case decline.Declined:
		return "[INFO] Live branch protection not compared with the forge: branch-ruleset declined by adoption.decline.", nil
	case policy == nil:
		return "", errors.New("[FAIL] Live branch protection audit failed: policy is required")
	case !adopt.RulesetRequired(policy.BranchProtection):
		return "[INFO] Live branch protection not compared with the forge: policy requires neither linear history nor signed commits, so it declares no branch protection ruleset.", nil
	case live.protection == nil:
		return "[SKIP] Live branch protection not compared with the forge: " + live.notMade, nil
	}
	return "", nil
}

// liveProtectionReconcileHint is the remedy a drift failure names. plan --remote and sync --remote
// require the checks of the workflows of the checkout they run in, not the default branch's, and a
// sync keeps every check it finds required, so only a sync from an up-to-date default branch
// requires what that branch runs. sync --remote also writes the labels and repository metadata,
// which plan --remote does not preview.
const liveProtectionReconcileHint = "To reconcile it, run 'praetorctl plan --remote' and then 'praetorctl sync --remote' " +
	"from an up-to-date checkout of %[1]s, not from another branch:\n" +
	"  both require the status checks of the workflows of the checkout they run in, and sync keeps every check it finds " +
	"required, so a sync from another branch requires its jobs that %[1]s does not run, and they block every pull request until removed by hand.\n" +
	"  sync --remote also writes the labels in .config/labels.yaml and the repository description, homepage and topics; " +
	"plan --remote previews only the branch protection, so review those first.\n" +
	"  Both read the token from --token, GITHUB_TOKEN or GH_TOKEN, never from the gh session."

// liveProtectionVerdict passes a branch that enforces every declared property and fails one that
// does not, naming each differing property with its declared and live value and the remedy
// (liveProtectionReconcileHint). A default branch GitHub does not have yet enforces nothing, so
// it is reported as not compared, in one line, until it is pushed.
func liveProtectionVerdict(target protectionTarget, live *forge.LiveBranchProtection, findings []forge.ProtectionFinding) error {
	if live.Missing {
		fmt.Printf("[SKIP] Live branch protection of %[1]s not compared with the forge: %[1]s does not exist on GitHub yet, "+
			"so nothing is enforced on it. The audit compares it once it is pushed; 'praetorctl plan --remote' shows what "+
			"the rulesets that target it require.\n", target.branch)
		return nil
	}
	var drift []string
	for _, finding := range findings {
		if finding.Verdict == forge.ProtectionDrift {
			drift = append(drift, "  "+describeProtectionFinding(finding))
		}
	}
	if len(drift) == 0 {
		fmt.Printf("[PASS] Live branch protection of %s compared with the forge: GitHub enforces every declared property (%s).\n",
			target.branch, describeMechanisms(live))
		return nil
	}
	return fmt.Errorf("[FAIL] Live branch protection of %s on GitHub does not match the declared policy (%s):\n%s\n%s",
		target.branch, describeMechanisms(live), strings.Join(drift, "\n"), fmt.Sprintf(liveProtectionReconcileHint, target.branch))
}

// auditActionsPermissions prints the permission check and returns the failure of a drifted one.
func auditActionsPermissions(ctx context.Context, declared *config.ActionsPolicy, live actionsForge) error {
	if declared == nil {
		fmt.Println("[INFO] Actions workflow permissions: overrides.actions declares none; nothing compared with the forge.")
		return nil
	}
	finding, notMade := readActionsPermissions(ctx, *declared, live)
	switch {
	case notMade != "":
		fmt.Printf("[SKIP] Actions workflow permissions not compared with the forge: %s\n", notMade)
	case finding.Verdict.Failed():
		return fmt.Errorf("[FAIL] Actions workflow permissions %s: %s", finding.Verdict, finding.Detail)
	default:
		fmt.Printf("[PASS] Actions workflow permissions compared with the forge: %s.\n", finding.Detail)
	}
	return nil
}

// auditWorkflowRuns prints the workflow run report, or why the runs were not read.
func auditWorkflowRuns(ctx context.Context, manifest *config.Manifest, rootDir string, live actionsForge) {
	if live.reader == nil {
		fmt.Printf("[SKIP] Workflow runs not read from the forge: %s\n", live.notMade)
		return
	}
	branch, err := forge.RepositoryDefaultBranch(ctx, rootDir, manifest)
	if err != nil {
		fmt.Printf("[SKIP] Workflow runs not read from the forge: %v\n", err)
		return
	}
	report, err := forge.AuditWorkflowRunHealth(ctx, rootDir, branch, live.reader, manifest.WorkflowRuns)
	if err != nil {
		fmt.Printf("[SKIP] Workflow runs on %s not read from the forge: %v\n", branch, err)
		return
	}
	printLines(report.Lines())
}

// printPlanActionsPermissions previews the live workflow permission comparison. plan reports
// the verdict and never fails on it, as it reports file drift; the audit fails.
func printPlanActionsPermissions(ctx context.Context, manifest *config.Manifest, rootDir string, offline bool) {
	fmt.Println("\nLive Actions workflow permissions (read-only; sync does not change them):")
	declared := manifest.Overrides.Actions
	if declared == nil {
		fmt.Println("  - overrides.actions declares none; nothing compared")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, actionsForgeTimeout)
	defer cancel()
	finding, notMade := readActionsPermissions(ctx, *declared, openActionsForge(ctx, rootDir, manifest.Repository, offline))
	if notMade != "" {
		fmt.Printf("  - not compared: %s\n", notMade)
		return
	}
	fmt.Printf("  - %s: %s\n", finding.Verdict, finding.Detail)
}
