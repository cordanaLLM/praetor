// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var claimTestEpoch = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func claimBrief(issue, session string) string {
	brief := validBrief
	if issue != "" {
		brief += "issue: " + issue + "\n"
	}
	if session != "" {
		brief += "session: " + session + "\n"
	}
	return brief
}

// lookupOf serves a fixed claim table and counts the calls it answers.
func lookupOf(table map[string]*IssueClaim, calls *int) IssueClaimLookup {
	return func(_ context.Context, ref string) (*IssueClaim, error) {
		*calls++
		if claim, ok := table[ref]; ok {
			return claim, nil
		}
		return nil, nil
	}
}

func runClaimDispatch(t *testing.T, brief string, lookup IssueClaimLookup) Response {
	t.Helper()
	root := repository(t, true)
	payload := nativePayload(t, "PreToolUse", "claim-session", "Agent", "claim-tool",
		map[string]any{"prompt": brief, "run_in_background": true}, nil)
	return Run(context.Background(), Invocation{Client: "claude", Event: string(EventPreDispatch), Stdin: bytes.NewReader(payload),
		Getenv: noEnvironment, WorkDir: root, Policy: policy(t), CorrelationDir: t.TempDir(), IssueClaims: lookup})
}

func deniedText(response Response) string { return string(response.Stdout) + string(response.Stderr) }

func TestBriefClaims_Negative_ForeignLiveClaimRefusesDispatchNamingIt(t *testing.T) {
	calls := 0
	table := map[string]*IssueClaim{"acme/widgets#7": {Session: "other", Lane: "agy", Branch: "feat/other", Stage: "review", Updated: claimTestEpoch}}
	response := runClaimDispatch(t, claimBrief("acme/widgets#7", "unit7"), lookupOf(table, &calls))
	if response.ExitCode == 0 {
		t.Fatalf("a foreign live claim must refuse the dispatch: %+v", response)
	}
	text := deniedText(response)
	for _, want := range []string{"acme/widgets#7", "claimed by session other", "feat/other", "stage review", "not by session unit7"} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal lacks %q:\n%s", want, text)
		}
	}
}

func TestBriefClaims_Positive_OwnClaimAndNoIssueAllowed(t *testing.T) {
	calls := 0
	table := map[string]*IssueClaim{"acme/widgets#7": {Session: "unit7", Updated: claimTestEpoch}, "other/lib#9": {Session: "unit7", Updated: claimTestEpoch}}
	if response := runClaimDispatch(t, claimBrief("acme/widgets#7, other/lib#9", "unit7"), lookupOf(table, &calls)); response.ExitCode != 0 {
		t.Fatalf("own claims must pass: %+v", response)
	}
	if calls != 2 {
		t.Fatalf("one lookup per issue, got %d", calls)
	}
	// A brief naming no issue is untouched, even with no lookup wired.
	if response := runClaimDispatch(t, validBrief, nil); response.ExitCode != 0 {
		t.Fatalf("a brief without issues must pass: %+v", response)
	}
}

func TestBriefClaims_Negative_UnclaimedUnverifiableAndMalformedRefuse(t *testing.T) {
	calls := 0
	cases := map[string]struct {
		brief  string
		lookup IssueClaimLookup
		want   string
	}{
		"unclaimed":      {claimBrief("acme/widgets#7", "unit7"), lookupOf(nil, &calls), "unclaimed; claim it first: praetorctl issue claim acme/widgets#7 --session unit7"},
		"no session":     {claimBrief("acme/widgets#7", ""), lookupOf(nil, &calls), "no session: line"},
		"no lookup":      {claimBrief("acme/widgets#7", "unit7"), nil, "no claim lookup is wired"},
		"forge failure":  {claimBrief("acme/widgets#7", "unit7"), func(context.Context, string) (*IssueClaim, error) { return nil, errors.New("forge down") }, "cannot be verified: forge down"},
		"malformed ref":  {claimBrief("widgets#7", "unit7"), lookupOf(nil, &calls), "not <owner>/<repo>#<number>"},
		"second foreign": {claimBrief("acme/widgets#7 acme/widgets#8", "unit7"), lookupOf(map[string]*IssueClaim{"acme/widgets#7": {Session: "unit7"}, "acme/widgets#8": {Session: "other", Updated: claimTestEpoch}}, &calls), "acme/widgets#8 is claimed by session other"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			response := runClaimDispatch(t, tc.brief, tc.lookup)
			if response.ExitCode == 0 || !strings.Contains(deniedText(response), tc.want) {
				t.Fatalf("want a refusal containing %q, got %+v\n%s", tc.want, response, deniedText(response))
			}
		})
	}
}

func TestBriefClaims_Boundary_SharedIssueIsLookedUpOnce(t *testing.T) {
	calls := 0
	table := map[string]*IssueClaim{"acme/widgets#7": {Session: "unit7", Updated: claimTestEpoch}}
	briefs := []string{claimBrief("acme/widgets#7", "unit7"), claimBrief("acme/widgets#7", "unit7")}
	if verdict := evaluateBriefClaims(context.Background(), briefs, lookupOf(table, &calls)); verdict.Outcome != Allow || calls != 1 {
		t.Fatalf("verdict %+v after %d lookups, want allow after 1", verdict, calls)
	}
	if verdict := evaluateBriefClaims(context.Background(), nil, nil); verdict.Outcome != Allow {
		t.Fatalf("no briefs, no claims: %+v", verdict)
	}
}

func TestBriefClaims_Positive_ProseResemblingFieldsIsNotTouched(t *testing.T) {
	for name, prose := range map[string]string{
		"bulleted issue":       "- Issue: parser drops trailing field\n",
		"issue without owner":  "  issue: #937\n",
		"session prose alone":  "session: pairing slot\n",
		"bulleted bad session": "- session: unit7 unit8\n",
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			response := runClaimDispatch(t, validBrief+prose, lookupOf(nil, &calls))
			if response.ExitCode != 0 || calls != 0 {
				t.Fatalf("prose must not reach the claim gate: exit %d, %d lookups, %s", response.ExitCode, calls, deniedText(response))
			}
		})
	}
}
