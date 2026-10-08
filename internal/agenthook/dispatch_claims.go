// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

// IssueClaim is the live claim on a forge issue as the dispatch gate sees it.
type IssueClaim struct {
	Session string
	Lane    string
	Branch  string
	Stage   string
	Updated time.Time
}

// IssueClaimLookup answers the live claim on ref (<owner>/<repo>#<number>), or nil when no
// session holds the issue. Any failure to read the forge is an error, never a nil claim: the
// gate refuses a dispatch whose claim it cannot verify.
type IssueClaimLookup func(ctx context.Context, ref string) (*IssueClaim, error)

const claimDenyPrefix = "[BLOCKED BY HISS] issue claim: "

func claimDenied(format string, args ...any) Verdict {
	return Verdict{Outcome: Deny, Reason: claimDenyPrefix + fmt.Sprintf(format, args...)}
}

// evaluateBriefClaims enforces issue claims at dispatch (#937). A brief that names issues
// (`issue:` lines) must also name the session that works them (`session:`); each named issue
// must be claimed by exactly that session. An issue another live session holds is refused,
// naming that claim; an unclaimed issue is refused until the dispatcher claims it. Briefs that
// name no issue are not touched. With no lookup wired, or a lookup that fails, a brief that
// names an issue is refused: an unverifiable claim never counts as held or free.
func evaluateBriefClaims(ctx context.Context, briefs []string, lookup IssueClaimLookup) Verdict {
	seen := make(map[string]*IssueClaim)
	for index := 0; index < len(briefs) && index < MaxDispatchBriefs; index++ {
		claim, err := caveman.ExtractBriefClaim(briefs[index])
		if err != nil {
			return claimDenied("brief %d: %v", index, err)
		}
		if len(claim.Issues) == 0 {
			continue
		}
		if verdict := checkBriefIssues(ctx, index, claim, lookup, seen); verdict.Outcome != Allow {
			return verdict
		}
	}
	return Verdict{Outcome: Allow}
}

func checkBriefIssues(ctx context.Context, index int, claim caveman.BriefClaim, lookup IssueClaimLookup, seen map[string]*IssueClaim) Verdict {
	if claim.Session == "" {
		return claimDenied("brief %d names issue %s but no session: line says which session works it", index, claim.Issues[0])
	}
	if lookup == nil {
		return claimDenied("brief %d names issue %s but no claim lookup is wired, so the claim cannot be verified", index, claim.Issues[0])
	}
	for _, ref := range claim.Issues {
		held, known := seen[ref]
		if !known {
			var err error
			if held, err = lookup(ctx, ref); err != nil {
				return claimDenied("brief %d: claim on %s cannot be verified: %v", index, ref, err)
			}
			seen[ref] = held
		}
		if verdict := judgeIssueClaim(index, ref, claim.Session, held); verdict.Outcome != Allow {
			return verdict
		}
	}
	return Verdict{Outcome: Allow}
}

func judgeIssueClaim(index int, ref, session string, held *IssueClaim) Verdict {
	switch {
	case held == nil:
		return claimDenied("brief %d: %s is unclaimed; claim it first: praetorctl issue claim %s --session %s --lane <lane> --branch <branch>",
			index, ref, ref, session)
	case held.Session != session:
		return claimDenied("brief %d: %s is claimed by session %s (lane %s, branch %s, stage %s, updated %s), not by session %s",
			index, ref, held.Session, held.Lane, held.Branch, held.Stage, held.Updated.UTC().Format(time.RFC3339), session)
	}
	return Verdict{Outcome: Allow}
}
