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

func (s *Server) createIssueClaimTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"ref", "session", "lane", "branch"}, Properties: map[string]mcp.PropertySchema{
		"ref":     {Type: "string", Description: "Issue reference <owner>/<repo>#<number>, any repository token reaches"},
		"session": {Type: "string", Description: "Identifier of agent session holding claim"},
		"lane":    {Type: "string", Description: "Lane session works in"},
		"branch":  {Type: "string", Description: "Branch carrying work"},
	}}
	return mcp.NewOpenWorldTool("standards_issue_claim",
		"Claim forge issue before work, as praetorctl issue claim. Posts one claim comment with machine-readable marker, adds status:in-progress label, assigns account. Refused while another session holds live claim; claim idle past stale window gets taken over. Writes to forge.",
		schema, s.runIssueClaim(forge.ClaimOpClaim), false, true)
}

func (s *Server) createIssueStatusTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"ref", "session"}, Properties: map[string]mcp.PropertySchema{
		"ref":     {Type: "string", Description: "Issue reference <owner>/<repo>#<number>, any repository token reaches"},
		"session": {Type: "string", Description: "Identifier of agent session holding claim"},
		"stage":   {Type: "string", Description: "claimed, implementing, review, fix-round-N, blocked, queued, landing; omit to read claim"},
		"note":    {Type: "string", Description: "Short note shown on claim comment"},
	}}
	return mcp.NewOpenWorldTool("standards_issue_status",
		"Record stage on session issue claim by editing single claim comment, as praetorctl issue status. Stage blocked adds status:blocked label. Without stage, reads claim only. Writes to forge.",
		schema, s.runIssueClaim(forge.ClaimOpStatus), false, true)
}

func (s *Server) createIssueReleaseTool() (mcp.Tool, error) {
	schema := mcp.ToolInputSchema{Type: "object", Required: []string{"ref", "session", "outcome"}, Properties: map[string]mcp.PropertySchema{
		"ref":     {Type: "string", Description: "Issue reference <owner>/<repo>#<number>, any repository token reaches"},
		"session": {Type: "string", Description: "Identifier of agent session holding claim"},
		"outcome": {Type: "string", Description: "landed, abandoned or handed-over"},
		"note":    {Type: "string", Description: "Short note shown on claim comment"},
	}}
	return mcp.NewOpenWorldTool("standards_issue_release",
		"Release session issue claim, as praetorctl issue release. Finalises claim comment with outcome, removes status labels; pull request closes issue. Writes to forge.",
		schema, s.runIssueClaim(forge.ClaimOpRelease), false, true)
}

// runIssueClaim is the handler of one claim tool: it decodes the arguments into the same
// forge.ClaimCommand the CLI builds and runs it on the same desk.
func (s *Server) runIssueClaim(op string) mcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.ToolResult, error) {
		result, err := executeIssueClaim(ctx, op, args)
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

func executeIssueClaim(ctx context.Context, op string, args map[string]any) (forge.ClaimResult, error) {
	if err := requireStringArguments(args); err != nil {
		return forge.ClaimResult{}, err
	}
	cmd := forge.ClaimCommand{Op: op}
	for key, target := range map[string]*string{"ref": &cmd.Ref, "session": &cmd.Session, "lane": &cmd.Lane,
		"branch": &cmd.Branch, "stage": &cmd.Stage, "note": &cmd.Note, "outcome": &cmd.Outcome} {
		value, err := argString(args, key)
		if err != nil {
			return forge.ClaimResult{}, err
		}
		*target = value
	}
	desk, err := newClaimDesk(ctx)
	if err != nil {
		return forge.ClaimResult{}, err
	}
	return desk.Run(ctx, cmd)
}
