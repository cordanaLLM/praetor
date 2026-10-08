// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/forge/forgetest"
)

var mcpClaimEpoch = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func fakeClaimDesk(fake *forgetest.ClaimFake) *forge.ClaimDesk {
	return &forge.ClaimDesk{Open: fake.Opener(), Stale: 6 * time.Hour, Now: func() time.Time { return mcpClaimEpoch }}
}

func useFakeMCPDesk(t *testing.T, fake *forgetest.ClaimFake) {
	t.Helper()
	previous := newClaimDesk
	newClaimDesk = func(context.Context) (*forge.ClaimDesk, error) { return fakeClaimDesk(fake), nil }
	t.Cleanup(func() { newClaimDesk = previous })
}

func decodeClaimResult(t *testing.T, text string) forge.ClaimResult {
	t.Helper()
	var res forge.ClaimResult
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("tool output is not a claim result: %v\n%s", err, text)
	}
	return res
}

// TestIssueClaimMCP_Positive_MirrorsTheCLIDesk runs one claim, status and release sequence
// through the tools and the same commands through the desk the CLI uses, on separate fakes,
// and requires equal results and equal forge state.
func TestIssueClaimMCP_Positive_MirrorsTheCLIDesk(t *testing.T) {
	srv, _ := newFixtureServer(t)
	toolFake, cliFake := forgetest.NewClaimFake(), forgetest.NewClaimFake()
	useFakeMCPDesk(t, toolFake)
	cliDesk := fakeClaimDesk(cliFake)
	steps := []struct {
		tool string
		args map[string]any
		cmd  forge.ClaimCommand
	}{
		{"standards_issue_claim", map[string]any{"ref": "acme/widgets#7", "session": "s1", "lane": "agy", "branch": "feat/x"},
			forge.ClaimCommand{Op: forge.ClaimOpClaim, Ref: "acme/widgets#7", Session: "s1", Lane: "agy", Branch: "feat/x"}},
		{"standards_issue_status", map[string]any{"ref": "acme/widgets#7", "session": "s1", "stage": "blocked", "note": "two findings"},
			forge.ClaimCommand{Op: forge.ClaimOpStatus, Ref: "acme/widgets#7", Session: "s1", Stage: "blocked", Note: "two findings"}},
		{"standards_issue_release", map[string]any{"ref": "acme/widgets#7", "session": "s1", "outcome": "handed-over"},
			forge.ClaimCommand{Op: forge.ClaimOpRelease, Ref: "acme/widgets#7", Session: "s1", Outcome: "handed-over"}},
	}
	for _, step := range steps {
		result := callTool(t, srv, step.tool, step.args)
		if result.IsError {
			t.Fatalf("%s: %s", step.tool, result.Content[0].Text)
		}
		want, err := cliDesk.Run(context.Background(), step.cmd)
		if err != nil {
			t.Fatal(err)
		}
		if got := decodeClaimResult(t, result.Content[0].Text); got.Summary() != want.Summary() || got.Claim.CommentID != want.Claim.CommentID {
			t.Fatalf("%s differs from the CLI desk:\n tool %+v\n cli  %+v", step.tool, got, want)
		}
	}
	if len(toolFake.Comments) != 1 || len(cliFake.Comments) != 1 || toolFake.Comments[0].Body != cliFake.Comments[0].Body {
		t.Fatalf("forge state differs:\n%+v\n%+v", toolFake.Comments, cliFake.Comments)
	}
	if len(toolFake.OnIssue) != 0 {
		t.Fatalf("release must clear the labels: %v", toolFake.OnIssue)
	}
}

func TestIssueClaimMCP_Negative_RefusalsAndBadArguments(t *testing.T) {
	srv, _ := newFixtureServer(t)
	fake := forgetest.NewClaimFake()
	useFakeMCPDesk(t, fake)
	claim := map[string]any{"ref": "acme/widgets#7", "session": "first", "lane": "agy", "branch": "feat/first"}
	if result := callTool(t, srv, "standards_issue_claim", claim); result.IsError {
		t.Fatal(result.Content[0].Text)
	}
	second := map[string]any{"ref": "acme/widgets#7", "session": "second", "lane": "codex", "branch": "feat/second"}
	expectError(t, "second claimer", callTool(t, srv, "standards_issue_claim", second), "session first")
	expectError(t, "bad reference", callTool(t, srv, "standards_issue_claim",
		map[string]any{"ref": "7", "session": "s", "lane": "l", "branch": "b"}), "not <owner>/<repo>#<number>")
	expectError(t, "non-string argument", callTool(t, srv, "standards_issue_claim",
		map[string]any{"ref": "acme/widgets#7", "session": 7, "lane": "l", "branch": "b"}), "must be a string")
	expectError(t, "bad stage", callTool(t, srv, "standards_issue_status",
		map[string]any{"ref": "acme/widgets#7", "session": "first", "stage": "released"}), "stage")
	expectError(t, "foreign release", callTool(t, srv, "standards_issue_release",
		map[string]any{"ref": "acme/widgets#7", "session": "second", "outcome": "landed"}), "session first")
	if len(fake.Comments) != 1 {
		t.Fatalf("refusals must add no comment: %d", len(fake.Comments))
	}
}

func TestIssueClaimMCP_Negative_ForgeErrorAndNoToken(t *testing.T) {
	srv, _ := newFixtureServer(t)
	fake := forgetest.NewClaimFake()
	fake.FailOn = "ListIssueComments"
	useFakeMCPDesk(t, fake)
	res := callTool(t, srv, "standards_issue_claim", map[string]any{"ref": "acme/widgets#7", "session": "s", "lane": "l", "branch": "b"})
	expectError(t, "forge error", res, "could not be verified")

	newClaimDesk = productionClaimDesk
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("PATH", t.TempDir()) // no gh to sign in with
	expectError(t, "no token", callTool(t, srv, "standards_issue_status",
		map[string]any{"ref": "acme/widgets#7", "session": "s"}), "forge token")
	if strings.Contains(res.Content[0].Text, "held") {
		t.Fatal("a forge error must not read as a held claim")
	}
}
