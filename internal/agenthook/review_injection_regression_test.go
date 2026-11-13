package agenthook

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/router"
)

// seedReviewCorrelation binds a review task to the same isolated handback fixture.
func seedReviewCorrelation(t *testing.T, root, state, task string) correlationStore {
	t.Helper()
	store, err := newCorrelationStore(t.Context(), root, state)
	if err != nil {
		t.Fatal(err)
	}
	entry := correlationEntry{Task: task, Resolution: config.Resolution{Register: config.TextRegisterInternal,
		Source: "surfaces.agent", ManifestSHA256: digestOf("version: 1\n")}}
	if err := store.reserveEntry(t.Context(), "claude", "s", "t", entry); err != nil {
		t.Fatal(err)
	}
	if _, err := store.promoteResolved(t.Context(), "claude", "s", "t", "agent", ""); err != nil {
		t.Fatal(err)
	}
	return store
}

// reviewReturn supplies the required return fields and one exact injection declaration.
func reviewReturn(flag string) string {
	return "verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none\nprompt_injection: " + flag + "\n"
}

// TestReviewInjectionPendingHandback rejects a replacement until the first attempt aborts.
func TestReviewInjectionPendingHandback(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	store := seedReviewCorrelation(t, root, state, "review")
	first := Canonical{ConversationID: "s", AgentID: "agent", ToolUseID: "first", Return: reviewReturn("true")}
	row, in := Registration{Client: "claude"}, Invocation{CorrelationDir: state}
	if got := evaluateAgentHandback(t.Context(), row, first, root, in); got.Outcome != Allow {
		t.Fatalf("first handback: %+v", got)
	}
	second := first
	second.ToolUseID, second.Return = "second", reviewReturn("false")
	if got := evaluateAgentHandback(t.Context(), row, second, root, in); got.Outcome != Deny {
		t.Errorf("pending finding replaced: %+v", got)
	}
	if err := store.markHandbackDelivered(t.Context(), "claude", "s", "agent", "second", second.Return); err == nil {
		t.Error("replacement receipt accepted")
	}
	if err := store.cancelHandback(t.Context(), "claude", "s", "agent", "first", first.Return); err != nil {
		t.Fatalf("original abort: %v", err)
	}
	if got := evaluateAgentHandback(t.Context(), row, second, root, in); got.Outcome != Allow {
		t.Fatalf("retry after abort: %+v", got)
	}
	if err := store.markHandbackDelivered(t.Context(), "claude", "s", "agent", "second", second.Return); err != nil {
		t.Fatalf("retry receipt: %v", err)
	}
}

// TestReviewInjectionOutcome measures stored outcomes for delivered and stop-only returns.
func TestReviewInjectionOutcome(t *testing.T) {
	cases := []struct {
		name, flag string
		delivered  bool
	}{
		{"stop-finding", "true", false}, {"delivered-finding", "true", true},
		{"stop-pass", "false", false}, {"delivered-pass", "false", true},
	}
	for index := 0; index < len(cases) && index < 4; index++ {
		tc := cases[index]
		t.Run(tc.name, func(t *testing.T) {
			root, state := claudeOutcomeRepository(t), t.TempDir()
			seedReviewCorrelation(t, root, state, "review")
			body := reviewReturn(tc.flag)
			if tc.delivered {
				deliverReviewReturn(t, root, state, body)
				body = "Report delivered through SubagentHandback."
			}
			response := runAgentHook(t, root, state, "claude", EventPostReturn,
				claudeSubagentStopPayload(t, root, plantClaudeTranscript(t, ""), body))
			if response.ExitCode != 0 {
				t.Fatalf("stop: %s", response.String())
			}
			assertReviewOutcome(t, root, tc.flag)
			if !tc.delivered && tc.flag == "true" && !strings.Contains(string(response.Stderr), ReviewInjectionMarker+"; task: review") {
				t.Errorf("stop finding notice missing: %s", response.String())
			}
		})
	}
}

// deliverReviewReturn validates and confirms one isolated handback through the native hook.
func deliverReviewReturn(t *testing.T, root, state, body string) {
	t.Helper()
	events := []Event{EventPreHandback, EventHandbackReceipt}
	for index := 0; index < len(events) && index < 2; index++ {
		event := events[index]
		native := map[Event]string{EventPreHandback: "PreToolUse", EventHandbackReceipt: "PostToolUse"}[event]
		response := runAgentHook(t, root, state, "claude", event, handbackEventPayload(t, native, "s", "agent", body))
		if response.ExitCode != 0 {
			t.Fatalf("handback: %s", response.String())
		}
	}
}

// assertReviewOutcome reads the outcome row and refuses success for a flagged review.
func assertReviewOutcome(t *testing.T, root, flag string) {
	t.Helper()
	rows, err := router.ReadOutcomes(t.Context(), filepath.Join(root, router.DefaultOutcomeLogPath))
	if err != nil || len(rows) != 1 {
		t.Fatalf("outcome rows: %+v %v", rows, err)
	}
	want := router.OutcomeOK
	if flag == "true" {
		want = router.OutcomeFail
	}
	if rows[0].Result != want {
		t.Errorf("result=%s, want %s", rows[0].Result, want)
	}
}

// TestReviewInjectionActiveDeclarations refuses declarations carried only by inactive text.
func TestReviewInjectionActiveDeclarations(t *testing.T) {
	cases := []string{
		"```text\nprompt_injection: false\n```",
		"~~~\nprompt_injection: false\n~~~",
		"> prompt_injection: false",
		"\"prompt_injection: false\"",
		"`\nprompt_injection: false\n`",
		"<!-- caveman:off -->\nprompt_injection: false\n<!-- caveman:on -->",
		"<!--\nprompt_injection: false\n-->",
	}
	for index := 0; index < len(cases) && index < 7; index++ {
		text := cases[index]
		if _, err := validateReviewInjection(text); err == nil {
			t.Errorf("inactive declaration accepted: %q", text)
		}
		if flag, err := validateReviewInjection(text + "\n\nprompt_injection: true"); err != nil || !flag {
			t.Errorf("active declaration lost: %q: flag=%v err=%v", text, flag, err)
		}
	}
	text := "literal `\n\n  \nprompt_injection: false\n\nliteral `"
	if flag, err := validateReviewInjection(text); err != nil || flag {
		t.Errorf("paragraph boundary crossed: flag=%v err=%v", flag, err)
	}

}

// TestReviewInjectionBounds checks the exact line and byte bounds and their first excess.
func TestReviewInjectionBounds(t *testing.T) {
	const field = "prompt_injection: false"
	cases := []string{field + strings.Repeat("\n", (1<<16)-1), field + strings.Repeat(" ", MaxInputBytes-len(field))}
	for index := 0; index < len(cases) && index < 2; index++ {
		text := cases[index]
		if flag, err := validateReviewInjection(text); err != nil || flag {
			t.Errorf("exact bound denied: bytes=%d: %v %v", len(text), flag, err)
		}
		if _, err := validateReviewInjection(text + "\n"); err == nil {
			t.Errorf("excess accepted: bytes=%d", len(text)+1)
		}
	}
}

// TestReviewInjectionHandbackContext checks the tool caller receives the finding as context.
func TestReviewInjectionHandbackContext(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	seedReviewCorrelation(t, root, state, "review")
	response := runAgentHook(t, root, state, "claude", EventPreHandback, handbackPayload(t, "s", "agent", reviewReturn("true")))
	var output struct {
		Specific struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(response.Stdout, &output); err != nil {
		t.Fatalf("no delivered context: %s (%v)", response.String(), err)
	}
	if response.ExitCode != 0 || output.Specific.Context != ReviewInjectionMarker+"; task: review" {
		t.Fatalf("finding context: %s", response.String())
	}
}

// TestReviewInjectionDispatchPrompt checks guidance reaches the reviewer in the rewritten prompt.
func TestReviewInjectionDispatchPrompt(t *testing.T) {
	for index := 0; index < 2; index++ {
		readonly := index == 1
		root, _ := readOnlyRepository(t, true)
		state := t.TempDir()
		brief := briefFor("review", "internal/agenthook")
		if readonly {
			brief += "readonly: true\n"
		}
		pre := claudeDispatch(t, "s", "t", brief, nil)
		response := runAgentHook(t, root, state, "claude", EventPreDispatch, pre)
		var output struct {
			Specific struct {
				Input map[string]any `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(response.Stdout, &output); err != nil {
			t.Fatalf("no rewritten dispatch: %s (%v)", response.String(), err)
		}
		prompt, ok := output.Specific.Input["prompt"].(string)
		if !ok {
			t.Fatalf("prompt is not a string: %s", response.String())
		}
		if response.ExitCode != 0 || !strings.Contains(prompt, "review return requires prompt_injection: true or prompt_injection: false") || !strings.Contains(prompt, brief) {
			t.Fatalf("missing reviewer guidance: %s", response.String())
		}
		launch := nativePayload(t, "PostToolUse", "s", "Agent", "t", output.Specific.Input,
			map[string]any{"status": "async_launched", "agentId": "agent"})
		if got := runAgentHook(t, root, state, "claude", EventDispatchReceipt, launch); got.ExitCode != 0 {
			t.Fatalf("rewritten launch rejected: %s", got.String())
		}
	}
}
