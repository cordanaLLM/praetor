// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

const testReviewBrief = "goal: review changes\ninputs: internal/agenthook\nreturn: verdict plus citations\nevidence: focused review\ntask: review\n"

func writeTestLines(t *testing.T, root, rel string, count int, header string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if header != "" {
		b.WriteString(header)
		b.WriteByte('\n')
	}
	start := 1
	if header != "" {
		start = 2
	}
	for i := start; i <= count; i++ {
		fmt.Fprintf(&b, "# line %d\n", i)
	}
	if err := os.WriteFile(full, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReturnClaims_Refused_MissingPath(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcript := plantClaudeTranscript(t, "")
	badReturn := "verdict: pass\nchanged: internal/agenthook/nonexistent.go\nran: none\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, badReturn)
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 2 || !strings.Contains(string(resp.Stderr), "nonexistent.go: file not found") {
		t.Fatalf("expected missing path refusal: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

func TestReturnClaims_Refused_LinePastEndOfFile(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcript := plantClaudeTranscript(t, "")
	// 5 lines in file, citing line 6 (line count + 1)
	badReturn := "verdict: pass\nchanged: internal/agenthook/testfile.txt:6\nran: none\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, badReturn)
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 2 || !strings.Contains(string(resp.Stderr), "line 6 exceeds file line count 5") {
		t.Fatalf("expected line past end refusal: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

func TestReturnClaims_Refused_ClaimedRunNoBashCall(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	// Transcript with no matching Bash call
	transcriptData := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"git status"}}]}}` + "\n"
	transcript := plantClaudeTranscript(t, transcriptData)
	badReturn := "verdict: pass\nchanged: internal/agenthook/testfile.txt:5\nran: `go test -race`\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, badReturn)
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 2 || !strings.Contains(string(resp.Stderr), "go test -race: claimed run with no matching Bash tool call") {
		t.Fatalf("expected claimed run refusal: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

func TestReturnClaims_Accepted_ExistingPathAndLastValidLine(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcriptData := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test -race ./..."}}]}}` + "\n"
	transcript := plantClaudeTranscript(t, transcriptData)
	goodReturn := "verdict: pass\nchanged: internal/agenthook/testfile.txt:5\nran: `go test -race`\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, goodReturn)
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 0 {
		t.Fatalf("expected accepted return: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

func TestReturnClaims_Accepted_HonestReturnBuildCouldNotRun(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcript := plantClaudeTranscript(t, "")
	honestReturn := "verdict: fail\nchanged: internal/agenthook/testfile.txt:5\nran: build not run\nevidence: none\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, honestReturn)
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 0 {
		t.Fatalf("expected honest return accepted: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

func TestReturnClaims_Accepted_NonReviewLabelNotChecked(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	// validBrief has task: feature_implementation (non-review label)
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", validBrief)
	transcript := plantClaudeTranscript(t, "")
	// Cites missing file and claims run without transcript call; should pass because label is not review
	returnBody := "verdict: pass\nchanged: internal/agenthook/missing.go:999\nran: `go test -race`\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, returnBody)
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 0 {
		t.Fatalf("expected non-review label allowed without claim checks: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

func TestReturnClaims_Unmeasured_CodexReturnWithClaimedRun(t *testing.T) {
	root := repository(t, true)
	codexReturnWithRun := "verdict: pass\nchanged: none\nran: `go test -race`\nevidence: exit 0\nopen: none\n"
	payload := commandPayload(t, map[string]any{
		"hook_event_name":        "SubagentStop",
		"session_id":             "codex-session",
		"agent_id":               "codex-agent",
		"last_assistant_message": codexReturnWithRun,
		"stop_hook_active":       false,
	})
	resp := runAgentHook(t, root, t.TempDir(), "codex", EventPostReturn, payload)
	if resp.ExitCode != 0 {
		t.Fatalf("expected exit 0 (skip): exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
	stderr := string(resp.Stderr)
	if !strings.Contains(stderr, "claimed runs unmeasured") || !strings.Contains(stderr, "go test -race") {
		t.Fatalf("expected skip reason naming unmeasured claimed run: %q", stderr)
	}
}

func TestReturnClaims_SyntheticSurveyAllShapes(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, ".standards.yaml", 410, "version: 1")
	writeTestLines(t, root, "internal/agenthook/policy.go", 410, "")
	writeTestLines(t, root, "internal/compiler/register.go", 120, "")
	writeTestLines(t, root, "internal/caveman/contract.go", 60, "")
	writeTestLines(t, root, ".config/lefthook/praetor.yml", 30, "")

	absContract := filepath.Join(root, "internal", "caveman", "contract.go") + ":44-45"
	absLefthook := filepath.Join(root, ".config", "lefthook", "praetor.yml") + ":12-16"
	outsidePath := filepath.Join(t.TempDir(), "external", "other.go") + ":10"

	synthetic := strings.Join([]string{
		"verdict: pass",
		"changed: read, only, none",
		"ran: grep, sed, cat reads of repo; no build, no git write, no edit",
		"evidence: internal/agenthook/policy.go:205-208, .standards.yaml:393-405, internal/compiler/register.go:99. and docs_references.go:90. " +
			absContract + ", " + absLefthook + ", policy.go:46, 57-63, 383, " +
			"Policy.Command, FleetTopology.Orgs, config.LoadManifest, " +
			"policy.go, dialect.go, command_targets.go, pr.go, operator_merge.go, " +
			outsidePath + ", .config/fleet-topology.yaml, internal/compiler/register.go:150",
		"open: none",
	}, "\n") + "\n"

	citations, err := caveman.ExtractReturnCitations(synthetic)
	if err != nil {
		t.Fatalf("ExtractReturnCitations: %v", err)
	}
	var misses []string
	for _, c := range citations {
		if miss := checkCitation(t.Context(), root, c); miss != "" {
			misses = append(misses, miss)
		}
	}
	wantMisses := []string{
		".config/fleet-topology.yaml: file not found",
		"internal/compiler/register.go:150: line 150 exceeds file line count 120",
	}
	if !reflect.DeepEqual(misses, wantMisses) {
		t.Fatalf("misses = %#v, want %#v", misses, wantMisses)
	}

	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcript := plantClaudeTranscript(t, "")
	stop := claudeSubagentStopPayload(t, root, transcript, synthetic)
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 2 {
		t.Fatalf("expected deny on synthetic return with 2 misses: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
	stderr := strings.TrimSpace(string(resp.Stderr))
	wantStderr := returnClaimDenyPrefix + strings.Join(wantMisses, "; ")
	if stderr != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr, wantStderr)
	}
}
