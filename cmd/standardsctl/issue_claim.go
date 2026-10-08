// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// claimDeskFactory builds the claim desk of a run; tests replace it with a fake forge.
var claimDeskFactory = newForgeClaimDesk

// claimOutput receives the report of a claim command.
var claimOutput io.Writer = os.Stdout

func printIssueClaimUsage() {
	fmt.Println("  claim <owner/repo#n> --session <id> --lane <lane> --branch <branch>")
	fmt.Println("            Claim an issue before working on it: one comment with a machine-readable marker, the")
	fmt.Println("            status:in-progress label and the account as assignee; refused while another session holds")
	fmt.Println("            a live claim, a claim without an update for forge.claim_stale (default 6h) is taken over")
	fmt.Println("  status <owner/repo#n> --session <id> [--stage <stage>] [--note <text>]")
	fmt.Println("            Record a stage on the claim comment (claimed, implementing, review, fix-round-N, blocked,")
	fmt.Println("            queued, landing); without --stage, report the current claim and change nothing")
	fmt.Println("  release <owner/repo#n> --session <id> --outcome landed|abandoned|handed-over [--note <text>]")
	fmt.Println("            Finalise the claim comment and remove the status labels; the pull request closes the issue")
}

// newForgeClaimDesk is the production desk: the GitHub forge with the stale window of the
// operator settings.
func newForgeClaimDesk(ctx context.Context, token, endpoint string, settings *operatorSettingsFlags) (*forge.ClaimDesk, error) {
	forgeSettings, err := loadForgeSettings(ctx, settings)
	if err != nil {
		return nil, fmt.Errorf("issue claim: %w", err)
	}
	return forge.NewClaimDesk(token, endpoint, forgeSettings.ClaimStaleWindow()), nil
}

// runIssueClaim serves `issue claim|status|release`: op is the subcommand.
func runIssueClaim(ctx context.Context, op string, args []string) error {
	fs := flag.NewFlagSet("issue "+op, flag.ContinueOnError)
	cmd := forge.ClaimCommand{Op: op}
	fs.StringVar(&cmd.Session, "session", "", "Identifier of the agent session that holds the claim")
	fs.StringVar(&cmd.Lane, "lane", "", "Lane the session works in (claim)")
	fs.StringVar(&cmd.Branch, "branch", "", "Branch that carries the work (claim)")
	fs.StringVar(&cmd.Stage, "stage", "", "Stage to record (status)")
	fs.StringVar(&cmd.Note, "note", "", "Short note shown on the claim comment (status, release)")
	fs.StringVar(&cmd.Outcome, "outcome", "", "Outcome: landed, abandoned or handed-over (release)")
	tokenFlag := fs.String("token", "", "Forge API token (default: GITHUB_TOKEN or gh auth token)")
	endpoint := fs.String("endpoint", "", "Forge API endpoint (default: https://api.github.com)")
	settings := registerOperatorSettingsFlags(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("issue %s needs exactly one issue reference <owner>/<repo>#<number>, got %d arguments", op, len(positional))
	}
	cmd.Ref = positional[0]
	token := resolveForgeAuthToken(ctx, *tokenFlag)
	if token == "" {
		return errors.New("issue " + op + " needs a forge token: set GITHUB_TOKEN, sign in with gh, or pass --token")
	}
	desk, err := claimDeskFactory(ctx, token, *endpoint, settings)
	if err != nil {
		return err
	}
	result, err := desk.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("issue %s: %w", op, err)
	}
	if _, err := fmt.Fprintln(claimOutput, result.Summary()); err != nil {
		return fmt.Errorf("write claim report: %w", err)
	}
	return nil
}
