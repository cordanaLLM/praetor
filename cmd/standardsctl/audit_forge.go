// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// actionsForgeHost is the only forge the live Actions checks read: the origin remote must
	// name the manifest's repository on it.
	actionsForgeHost = "github.com"
	// actionsForgeTimeout bounds every live Actions read of one audit or plan run (HISS-02).
	actionsForgeTimeout = 3 * time.Minute
	// offlineFlagUsage is the --offline help text of audit and plan.
	offlineFlagUsage = "Read nothing from the forge: the live Actions permission and workflow run checks report as not made"
)

var (
	// actionsForgeEndpoint is the GitHub REST API the live Actions checks read. Tests point it
	// at a stand-in forge; nothing else changes it.
	actionsForgeEndpoint = util.DefaultGitHubAPIBase
	// resolveActionsToken finds the token the live Actions checks read with: GITHUB_TOKEN,
	// GH_TOKEN, then the gh CLI session. TestMain replaces it, so no test reads the operator's
	// credential or asks the real forge.
	resolveActionsToken = func(ctx context.Context) string { return util.ResolveAuthTokenContext(ctx, "") }
)

// actionsForge is the live Actions reader of one audit or plan run, or why there is none.
type actionsForge struct {
	reader  forge.ActionsReader
	notMade string
}

// openActionsForge decides whether the live Actions checks may ask the forge: not with
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
	return actionsForge{reader: driver}
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

// auditLiveActions is the audit gate that reads the forge (#611, #612). The live workflow
// permissions are compared with overrides.actions and fail the audit on drift. Each
// workflow's recent runs on the default branch are reported, and never fail it: the operator
// chose read-and-report for them. A check the forge did not answer is named as not made, never
// passed; sync --remote does not reconcile either setting.
func auditLiveActions(ctx context.Context, manifest *config.Manifest, rootDir string, offline bool) error {
	ctx, cancel := context.WithTimeout(ctx, actionsForgeTimeout)
	defer cancel()
	live := openActionsForge(ctx, rootDir, manifest.Repository, offline)
	if err := auditActionsPermissions(ctx, manifest.Overrides.Actions, live); err != nil {
		return err
	}
	auditWorkflowRuns(ctx, manifest, rootDir, live)
	return nil
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
