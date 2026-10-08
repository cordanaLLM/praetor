// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/util"
)

// newClaimDesk builds the claim desk of one tool call: the GitHub forge with the stale window
// of the operator settings, selected at call time the way the CLI does. Tests replace it with
// a desk over an in-memory forge.
var newClaimDesk = productionClaimDesk

func productionClaimDesk(ctx context.Context) (*forge.ClaimDesk, error) {
	token := util.ResolveAuthTokenContext(ctx, "")
	if token == "" {
		return nil, errors.New("issue claims need a forge token: set GITHUB_TOKEN or sign in with gh")
	}
	manifest, err := installManifestPath()
	if err != nil {
		manifest = ""
	}
	policy, err := config.SelectOperatorPolicy(ctx, config.SettingsRequest{Getenv: os.Getenv, ManifestPath: manifest})
	if err != nil {
		return nil, fmt.Errorf("load operator settings: %w", err)
	}
	return forge.NewClaimDesk(token, "", policy.OperatorSettings().Forge.ClaimStaleWindow()), nil
}

var claimRefProperty = mcp.PropertySchema{Type: "string", Description: "Issue reference <owner>/<repo>#<number>; any repository the token can reach"}
var claimSessionProperty = mcp.PropertySchema{Type: "string", Description: "Identifier of the agent session that holds the claim"}
var claimNoteProperty = mcp.PropertySchema{Type: "string", Description: "Short note shown on the claim comment"}

func (s *Server) createIssueClaimTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"ref", "session", "lane", "branch"}, Properties: map[string]mcp.PropertySchema{
		"ref": claimRefProperty, "session": claimSessionProperty,
		"lane":   {Type: "string", Description: "Lane the session works in"},
		"branch": {Type: "string", Description: "Branch that carries the work"},
	}}
	return mcp.NewOpenWorldTool("standards_issue_claim",
		"Claim a forge issue before working on it, as `praetorctl issue claim`: one claim comment with a machine-readable marker, the status:in-progress label and the account as assignee. Refused while another session holds a live claim; a claim without an update for the stale window is taken over. Writes to the forge.",
		schema, s.runIssueClaim(forge.ClaimOpClaim), false, true)
}

func (s *Server) createIssueStatusTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"ref", "session"}, Properties: map[string]mcp.PropertySchema{
		"ref": claimRefProperty, "session": claimSessionProperty,
		"stage": {Type: "string", Description: "claimed, implementing, review, fix-round-N, blocked, queued or landing; omit to read the current claim"},
		"note":  claimNoteProperty,
	}}
	return mcp.NewOpenWorldTool("standards_issue_status",
		"Record a stage on the session's issue claim by editing its one claim comment, as `praetorctl issue status`; blocked adds status:blocked. Without a stage it only reads the claim. Writes to the forge.",
		schema, s.runIssueClaim(forge.ClaimOpStatus), false, true)
}

func (s *Server) createIssueReleaseTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"ref", "session", "outcome"}, Properties: map[string]mcp.PropertySchema{
		"ref": claimRefProperty, "session": claimSessionProperty,
		"outcome": {Type: "string", Description: "landed, abandoned or handed-over"},
		"note":    claimNoteProperty,
	}}
	return mcp.NewOpenWorldTool("standards_issue_release",
		"Release the session's issue claim, as `praetorctl issue release`: finalise the claim comment with the outcome and remove the status labels; the pull request closes the issue. Writes to the forge.",
		schema, s.runIssueClaim(forge.ClaimOpRelease), false, true)
}

// runIssueClaim is the handler of one claim tool: it decodes the arguments into the same
// forge.ClaimCommand the CLI builds and runs it on the same desk.
func (s *Server) runIssueClaim(op string) mcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		if err := requireStringArguments(args); err != nil {
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		cmd := forge.ClaimCommand{Op: op}
		for key, target := range map[string]*string{"ref": &cmd.Ref, "session": &cmd.Session, "lane": &cmd.Lane,
			"branch": &cmd.Branch, "stage": &cmd.Stage, "note": &cmd.Note, "outcome": &cmd.Outcome} {
			value, err := argString(args, key)
			if err != nil {
				return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
			}
			*target = value
		}
		desk, err := newClaimDesk(ctx)
		if err != nil {
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		result, err := desk.Run(ctx, cmd)
		if err != nil {
			return mcpErrorResult(err.Error(), mcpTextUntrusted), nil
		}
		data, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("encode claim result: %w", err)
		}
		return mcpTextResult(string(data), mcpTextStructuredJSON), nil
	}
}
