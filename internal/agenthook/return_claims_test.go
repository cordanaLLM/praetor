// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
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

// TestReturnClaims_Refused_MissingPath refuses a return that cites a file which does not exist.
func TestReturnClaims_Refused_MissingPath(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcript := plantClaudeTranscript(t, "")
	badReturn := "verdict: pass\nchanged: internal/agenthook/nonexistent.go\nran: none\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, badReturn+"prompt_injection: false\n")
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 2 || !strings.Contains(string(resp.Stderr), "nonexistent.go: file not found") {
		t.Fatalf("expected missing path refusal: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

// TestReturnClaims_Refused_LinePastEndOfFile refuses a return citing a line one past the file's last line.
func TestReturnClaims_Refused_LinePastEndOfFile(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcript := plantClaudeTranscript(t, "")
	// 5 lines in file, citing line 6 (line count + 1)
	badReturn := "verdict: pass\nchanged: internal/agenthook/testfile.txt:6\nran: none\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, badReturn+"prompt_injection: false\n")
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 2 || !strings.Contains(string(resp.Stderr), "line 6 exceeds file line count 5") {
		t.Fatalf("expected line past end refusal: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

// TestReturnClaims_Refused_ClaimedRunNoBashCall refuses a claimed command run that no Bash tool call made.
func TestReturnClaims_Refused_ClaimedRunNoBashCall(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	// Transcript with no matching Bash call
	transcriptData := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"git status"}}]}}` + "\n"
	transcript := plantClaudeTranscript(t, transcriptData)
	badReturn := "verdict: pass\nchanged: internal/agenthook/testfile.txt:5\nran: `go test -race`\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, badReturn+"prompt_injection: false\n")
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 2 || !strings.Contains(string(resp.Stderr), "go test -race: claimed run with no matching Bash tool call") {
		t.Fatalf("expected claimed run refusal: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

// TestReturnClaims_Accepted_ExistingPathAndLastValidLine accepts an existing path cited at its last valid line with a matching run.
func TestReturnClaims_Accepted_ExistingPathAndLastValidLine(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcriptData := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test -race ./..."}}]}}` + "\n"
	transcript := plantClaudeTranscript(t, transcriptData)
	goodReturn := "verdict: pass\nchanged: internal/agenthook/testfile.txt:5\nran: `go test -race`\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, goodReturn+"prompt_injection: false\n")
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 0 {
		t.Fatalf("expected accepted return: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

// TestReturnClaims_Accepted_HonestReturnBuildCouldNotRun accepts a return that states honestly that the build could not run.
func TestReturnClaims_Accepted_HonestReturnBuildCouldNotRun(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcript := plantClaudeTranscript(t, "")
	honestReturn := "verdict: fail\nchanged: internal/agenthook/testfile.txt:5\nran: build not run\nevidence: none\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, honestReturn+"prompt_injection: false\n")
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

// TestReturnClaims_SyntheticSurveyAllShapes runs every claim shape of a synthetic survey return through the check.
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
	stop := claudeSubagentStopPayload(t, root, transcript, synthetic+"prompt_injection: false\n")
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

func TestReturnClaims_CancellableCitationRead(t *testing.T) {
	root := repository(t, true)
	writeTestLines(t, root, "internal/agenthook/policy.go", 10, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	miss := checkCitation(ctx, root, caveman.ReturnCitation{Path: "internal/agenthook/policy.go", Line: 0})
	if miss == "" || !strings.Contains(miss, "context canceled") {
		t.Fatalf("expected context cancellation error on line 0 citation, got %q", miss)
	}
}

// TestReturnClaims_Refused_SymlinkEscapingRoot refuses a cited path that resolves through a symlink outside the repository.
func TestReturnClaims_Refused_SymlinkEscapingRoot(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("outside content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "internal", "agenthook", "escape_symlink.txt")
	if err := os.Symlink(outsideFile, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcript := plantClaudeTranscript(t, "")
	badReturn := "verdict: pass\nchanged: internal/agenthook/escape_symlink.txt:1\nran: none\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, badReturn+"prompt_injection: false\n")
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 2 || !strings.Contains(string(resp.Stderr), "escape_symlink.txt") {
		t.Fatalf("expected escaping symlink refusal: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

type cancelAfterCallsContext struct {
	context.Context
	remaining int
}

func (c *cancelAfterCallsContext) Err() error {
	if c.remaining <= 0 {
		return context.DeadlineExceeded
	}
	c.remaining--
	return nil
}

func TestReturnClaims_Skip_EndedContext(t *testing.T) {
	root := repository(t, true)
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	ctx := &cancelAfterCallsContext{Context: context.Background(), remaining: 2}
	canonical := Canonical{
		Return: "verdict: pass\nchanged: internal/agenthook/testfile.txt:5\nran: none\nevidence: exit 0\nopen: none\n",
	}
	entry := correlationEntry{Task: "review"}
	verdict := evaluateReturnClaims(ctx, "claude", root, canonical, entry)
	if verdict.Outcome != Skip {
		t.Fatalf("verdict.Outcome = %v, want Skip on ended context; reason %q", verdict.Outcome, verdict.Reason)
	}
}

// TestReturnClaims_Accepted_BashCommandPastIndex1023 finds a claimed run in a Bash call past the transcript's first 1023 entries.
func TestReturnClaims_Accepted_BashCommandPastIndex1023(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	var b strings.Builder
	for i := 0; i < 1024; i++ {
		fmt.Fprintf(&b, `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"echo dummy %d"}}]}}`+"\n", i)
	}
	b.WriteString(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test -race ./..."}}]}}` + "\n")
	transcript := plantClaudeTranscript(t, b.String())
	goodReturn := "verdict: pass\nchanged: internal/agenthook/testfile.txt:5\nran: `go test -race`\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, goodReturn+"prompt_injection: false\n")
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 0 {
		t.Fatalf("expected accepted return with command at index 1024: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

// TestReturnClaims_Refused_EchoArgumentNotExecutedCommand refuses a claimed run that only appears as an echo argument.
func TestReturnClaims_Refused_EchoArgumentNotExecutedCommand(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcriptData := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"echo go test -race"}}]}}` + "\n"
	transcript := plantClaudeTranscript(t, transcriptData)
	badReturn := "verdict: pass\nchanged: internal/agenthook/testfile.txt:5\nran: `go test -race`\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, badReturn+"prompt_injection: false\n")
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 2 || !strings.Contains(string(resp.Stderr), "go test -race: claimed run with no matching Bash tool call") {
		t.Fatalf("expected echo argument refusal: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
}

// cancelOnCitationReadContext cancels the parent context during citation read,
// after the loop-top check has passed.
type cancelOnCitationReadContext struct {
	context.Context
	cancel    context.CancelFunc
	errCalls  int
	topPassed bool
}

func (c *cancelOnCitationReadContext) Err() error {
	c.errCalls++
	if c.errCalls == 3 {
		c.topPassed = true
	}
	return c.Context.Err()
}

func (c *cancelOnCitationReadContext) Done() <-chan struct{} {
	if c.topPassed {
		c.cancel()
	}
	return c.Context.Done()
}

// TestReturnClaims_Positive_ContextCanceledDuringCitationRead verifies that cancellation
// during checkCitation yields Skip, never a Deny verdict.
func TestReturnClaims_Positive_ContextCanceledDuringCitationRead(t *testing.T) {
	root := repository(t, true)
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelOnCitationReadContext{Context: baseCtx, cancel: cancel}
	canonical := Canonical{
		Return: "verdict: pass\nchanged: internal/agenthook/testfile.txt:5\nran: none\nevidence: exit 0\nopen: none\n",
	}
	entry := correlationEntry{Task: "review"}
	verdict := evaluateReturnClaims(ctx, "claude", root, canonical, entry)
	if verdict.Outcome != Skip {
		t.Fatalf("verdict.Outcome = %v, want Skip; reason %q", verdict.Outcome, verdict.Reason)
	}
	if !strings.Contains(verdict.Reason, "return claims not judged") {
		t.Fatalf("verdict.Reason = %q, want 'return claims not judged' prefix", verdict.Reason)
	}
}

// TestReturnClaims_Negative_UncanceledContextGenuineFailureDenies verifies that an uncanceled
// context with an invalid citation yields Deny, not Skip.
func TestReturnClaims_Negative_UncanceledContextGenuineFailureDenies(t *testing.T) {
	root := repository(t, true)
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	canonical := Canonical{
		Return: "verdict: pass\nchanged: internal/agenthook/testfile.txt:99\nran: none\nevidence: exit 0\nopen: none\n",
	}
	entry := correlationEntry{Task: "review"}
	verdict := evaluateReturnClaims(context.Background(), "claude", root, canonical, entry)
	if verdict.Outcome != Deny {
		t.Fatalf("verdict.Outcome = %v, want Deny; reason %q", verdict.Outcome, verdict.Reason)
	}
	if !strings.Contains(verdict.Reason, "line 99 exceeds file line count 5") {
		t.Fatalf("verdict.Reason = %q, want line bound failure", verdict.Reason)
	}
}

// cancelBeforeLoopContext cancels the context after isReviewTask has checked it.
type cancelBeforeLoopContext struct {
	context.Context
	cancel       context.CancelFunc
	doneIsReview bool
}

func (c *cancelBeforeLoopContext) Err() error {
	if c.doneIsReview {
		c.cancel()
		return c.Context.Err()
	}
	c.doneIsReview = true
	return nil
}

// TestReturnClaims_Boundary_ContextCanceledAtLoopTop verifies that context cancellation
// caught at the top of the evaluation loop yields Skip.
func TestReturnClaims_Boundary_ContextCanceledAtLoopTop(t *testing.T) {
	root := repository(t, true)
	writeTestLines(t, root, "internal/agenthook/testfile.txt", 5, "")
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelBeforeLoopContext{Context: baseCtx, cancel: cancel}
	canonical := Canonical{
		Return: "verdict: pass\nchanged: internal/agenthook/testfile.txt:5\nran: none\nevidence: exit 0\nopen: none\n",
	}
	entry := correlationEntry{Task: "review"}
	verdict := evaluateReturnClaims(ctx, "claude", root, canonical, entry)
	if verdict.Outcome != Skip {
		t.Fatalf("verdict.Outcome = %v, want Skip; reason %q", verdict.Outcome, verdict.Reason)
	}
	if !strings.Contains(verdict.Reason, "return claims not judged") {
		t.Fatalf("verdict.Reason = %q, want 'return claims not judged' prefix", verdict.Reason)
	}
}

// TestReturnClaims_Boundary_NonRegularCitationNotReportedAsOversized verifies that a non-regular
// cited path (symlink) is reported with its read error rather than as an oversized file.
func TestReturnClaims_Boundary_NonRegularCitationNotReportedAsOversized(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	writeTestLines(t, root, "internal/agenthook/target.txt", 5, "")
	symlink := filepath.Join(root, "internal", "agenthook", "symlink.txt")
	if err := os.Symlink("target.txt", symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
	transcript := plantClaudeTranscript(t, "")
	ret := "verdict: pass\nchanged: internal/agenthook/symlink.txt:1\nran: none\nevidence: exit 0\nopen: none\n"
	stop := claudeSubagentStopPayload(t, root, transcript, ret+"prompt_injection: false\n")
	resp := runAgentHook(t, root, state, "claude", EventPostReturn, stop)
	if resp.ExitCode != 2 {
		t.Fatalf("expected deny on non-regular citation: exit %d stderr %q", resp.ExitCode, resp.Stderr)
	}
	stderr := string(resp.Stderr)
	if strings.Contains(stderr, "file exceeds 1 MiB bound") {
		t.Fatalf("non-regular citation must not be reported as oversized: %q", stderr)
	}
	if !strings.Contains(stderr, "source must be regular") {
		t.Fatalf("expected 'source must be regular' in stderr, got %q", stderr)
	}
}
