// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The three claim commands, as the CLI and the MCP tools name them.
const (
	ClaimOpClaim   = "claim"
	ClaimOpStatus  = "status"
	ClaimOpRelease = "release"
)

// ClaimCommand is one decoded claim command. `praetorctl issue claim|status|release` and the
// standards_issue_* MCP tools both build one and call ClaimDesk.Run, so the two surfaces share
// a single implementation (HISS-19) and cannot drift.
type ClaimCommand struct {
	Op      string `json:"op"`
	Ref     string `json:"ref"`
	Session string `json:"session"`
	Lane    string `json:"lane,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Note    string `json:"note,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

// NewClaimDesk is the desk over the GitHub forge: every issue reference opens a driver for
// its own repository, so owner/repo#n works across repositories. token and endpoint are the
// driver's; stale is the window after which a claim may be taken over.
func NewClaimDesk(token, endpoint string, stale time.Duration) *ClaimDesk {
	return &ClaimDesk{
		Open: func(owner, repo string) (ClaimForge, error) {
			driver := NewGitHubDriver(token, endpoint)
			driver.SetRepository(owner, repo)
			if _, err := driver.TargetRepository(); err != nil {
				return nil, err
			}
			return driver, nil
		},
		Stale: stale,
		Now:   time.Now,
	}
}

// Run executes one claim command.
func (d *ClaimDesk) Run(ctx context.Context, cmd ClaimCommand) (ClaimResult, error) {
	ref, err := ParseClaimRef(cmd.Ref)
	if err != nil {
		return ClaimResult{}, err
	}
	switch cmd.Op {
	case ClaimOpClaim:
		return d.Claim(ctx, ref, ClaimRequest{Session: cmd.Session, Lane: cmd.Lane, Branch: cmd.Branch})
	case ClaimOpStatus:
		return d.Status(ctx, ref, StatusRequest{Session: cmd.Session, Stage: cmd.Stage, Note: cmd.Note})
	case ClaimOpRelease:
		return d.Release(ctx, ref, ReleaseRequest{Session: cmd.Session, Outcome: cmd.Outcome, Note: cmd.Note})
	}
	return ClaimResult{}, fmt.Errorf("unknown claim command %q", cmd.Op)
}

// Summary is the one-line report of a result.
func (r ClaimResult) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: session %s, lane %s, branch %s, stage %s", r.Action, r.Ref,
		r.Claim.Session, r.Claim.Lane, r.Claim.Branch, r.Claim.Stage)
	if r.Claim.Outcome != "" {
		fmt.Fprintf(&b, ", outcome %s", r.Claim.Outcome)
	}
	if r.TookOver != nil {
		fmt.Fprintf(&b, "\nreplaced the stale claim: %s", DescribeClaim(*r.TookOver))
	}
	return b.String()
}
