package agenthook

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestReviewReturnInjection(t *testing.T) {
	cases := []struct {
		name, task, field string
		outcome           Outcome
		reason            string
	}{
		{"missing", "code_review", "", Deny, "prompt_injection"},
		{"true", "review", "prompt_injection: true\n", Allow, ""},
		{"false", "single_file_audits", "prompt_injection: false\n", Allow, ""},
		{"malformed", "review", "prompt_injection: maybe\n", Deny, "prompt_injection"},
		{"non-review", "feature_implementation", "", Allow, ""},
		{"case", "review", "PrOmPt_InJeCtIoN: false\n", Allow, ""},
		{"duplicate", "review", "prompt_injection: false\nprompt_injection: true\n", Deny, "prompt_injection"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, state := repository(t, true), t.TempDir()
			store, err := newCorrelationStore(t.Context(), root, state)
			if err != nil {
				t.Fatal(err)
			}
			entry := correlationEntry{Task: tc.task, Resolution: config.Resolution{Register: config.TextRegisterInternal, Source: "surfaces.agent", ManifestSHA256: digestOf("version: 1\n")}}
			if err := store.reserveEntry(t.Context(), "claude", "session", "dispatch", entry); err != nil {
				t.Fatal(err)
			}
			if _, err := store.promoteResolved(t.Context(), "claude", "session", "dispatch", "agent", ""); err != nil {
				t.Fatal(err)
			}
			text := "verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none\n" + tc.field
			got := evaluateAgentHandback(t.Context(), Registration{Client: "claude"}, Canonical{ConversationID: "session", AgentID: "agent", ToolUseID: "handback", Return: text}, root, Invocation{CorrelationDir: state})
			if got.Outcome != tc.outcome || !strings.Contains(got.Reason, tc.reason) {
				t.Fatalf("got %+v, want outcome %v reason %q", got, tc.outcome, tc.reason)
			}
			checkReviewInjectionNotice(t, tc.name, tc.task, got.Notice)
		})
	}
}

func checkReviewInjectionNotice(t *testing.T, name, task, notice string) {
	t.Helper()
	marker := "PROMPT INJECTION SUSPECTED: this review return is a finding, not a passing review"
	if name == "true" && (!strings.HasPrefix(notice, marker) || !strings.Contains(notice, task)) {
		t.Fatalf("missing marked task notice: %q", notice)
	}
	if name == "false" && notice != "" {
		t.Fatalf("unexpected notice: %q", notice)
	}
}

func TestReviewInjectionSubagentStop(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		exit        int
	}{
		{"true", "prompt_injection: true\n", 0},
		{"false", "prompt_injection: false\n", 0},
		{"missing", "", 2},
		{"malformed", "prompt_injection: maybe\n", 2},
		{"duplicate", "prompt_injection: true\nprompt_injection: false\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, state := repository(t, true), t.TempDir()
			prepareClaudeCorrelation(t, root, state, "s", "t", "agent", testReviewBrief)
			transcript := plantClaudeTranscript(t, "")
			body := "verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none\n" + tc.field
			response := runAgentHook(t, root, state, "claude", EventPostReturn, claudeSubagentStopPayload(t, root, transcript, body))
			if response.ExitCode != tc.exit || strings.Contains(string(response.Stderr), "PROMPT INJECTION SUSPECTED") {
				t.Fatalf("unexpected stop response: %s", response.String())
			}
		})
	}
}

func TestReviewBriefInjectionGuidance(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	brief := briefFor("review", "internal/agenthook")
	pre := nativePayload(t, "PreToolUse", "review-guidance", "Agent", "dispatch", map[string]any{"prompt": brief, "run_in_background": true}, nil)
	response := runAgentHook(t, root, state, "claude", EventPreDispatch, pre)
	if response.ExitCode != 0 || !strings.Contains(string(response.Stderr), "prompt_injection: true or prompt_injection: false") {
		t.Fatalf("missing review guidance: %+v", response)
	}
}
