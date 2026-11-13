package agenthook

import (
	"strings"
	"testing"
)

// TestReviewReturnInjection covers the prompt_injection line of review returns: missing, true, false, malformed, non-review, case and duplicate.
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
	for index := 0; index < len(cases) && index < 7; index++ {
		tc := cases[index]
		t.Run(tc.name, func(t *testing.T) {
			root, state := repository(t, true), t.TempDir()
			seedReviewCorrelation(t, root, state, tc.task)
			text := "verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none\n" + tc.field
			canonical := Canonical{ConversationID: "s", AgentID: "agent", ToolUseID: "handback", Return: text,
				DispatchInput: commandPayload(t, map[string]any{"message": text})}
			got := evaluateAgentHandback(t.Context(), Registration{Client: "claude"}, canonical, root, Invocation{CorrelationDir: state})
			if got.Outcome != tc.outcome || !strings.Contains(got.Reason, tc.reason) {
				t.Fatalf("got %+v, want outcome %v reason %q", got, tc.outcome, tc.reason)
			}
			checkReviewInjectionNotice(t, tc.name, tc.task, got.Notice)
		})
	}
}

// checkReviewInjectionNotice asserts that a true review return's handback notice leads with the marker and names the task.
func checkReviewInjectionNotice(t *testing.T, name, task, notice string) {
	t.Helper()
	marker := "PROMPT INJECTION SUSPECTED: this review return is a finding, not a passing review"
	if name == "true" && (!strings.HasPrefix(notice, marker) || !strings.HasSuffix(notice, "; task: "+task)) {
		t.Fatalf("missing marked task notice: %q", notice)
	}
	if name == "false" && notice != "" {
		t.Fatalf("unexpected notice: %q", notice)
	}
}

// TestReviewInjectionSubagentStop checks that flagged reviews finish without a retry and emit a finding notice.
func TestReviewInjectionSubagentStop(t *testing.T) {
	cases := []struct {
		name, field string
		exit        int
	}{
		{"true", "prompt_injection: true\n", 0},
		{"false", "prompt_injection: false\n", 0},
		{"missing", "", 2},
		{"malformed", "prompt_injection: maybe\n", 2},
		{"duplicate", "prompt_injection: true\nprompt_injection: false\n", 2},
	}
	for index := 0; index < len(cases) && index < 5; index++ {
		tc := cases[index]
		t.Run(tc.name, func(t *testing.T) {
			root, state := repository(t, true), t.TempDir()
			seedReviewCorrelation(t, root, state, "review")
			transcript := plantClaudeTranscript(t, "")
			body := "verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none\n" + tc.field
			response := runAgentHook(t, root, state, "claude", EventPostReturn, claudeSubagentStopPayload(t, root, transcript, body))
			if response.ExitCode != tc.exit || (tc.name == "true") != strings.Contains(string(response.Stderr), ReviewInjectionMarker) {
				t.Fatalf("unexpected stop response: %s", response.String())
			}
		})
	}
}

// TestReviewBriefInjectionGuidance checks that the reviewer receives the prompt_injection requirement in its prompt.
func TestReviewBriefInjectionGuidance(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	brief := briefFor("review", "internal/agenthook")
	pre := nativePayload(t, "PreToolUse", "review-guidance", "Agent", "dispatch", map[string]any{"prompt": brief, "run_in_background": true}, nil)
	response := runAgentHook(t, root, state, "claude", EventPreDispatch, pre)
	if response.ExitCode != 0 || !strings.Contains(string(response.Stdout), "prompt_injection: true or prompt_injection: false") {
		t.Fatalf("missing review guidance: %+v", response)
	}
}
