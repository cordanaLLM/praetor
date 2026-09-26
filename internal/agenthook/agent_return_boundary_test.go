package agenthook

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// stopPayload is a Claude SubagentStop payload with the documented agent_type and
// stop_hook_active fields (https://code.claude.com/docs/en/hooks#subagentstop-input).
func stopPayload(t *testing.T, session, agent, agentType string, stopActive any, text string) []byte {
	t.Helper()
	fields := map[string]any{"hook_event_name": "SubagentStop", "session_id": session, "agent_id": agent,
		"agent_type": agentType, "last_assistant_message": text}
	if stopActive != nil {
		fields["stop_hook_active"] = stopActive
	}
	return commandPayload(t, fields)
}

func requireOutcome(t *testing.T, label string, response Response, exit int, text string) {
	t.Helper()
	if response.ExitCode != exit || !strings.Contains(string(response.Stderr), text) {
		t.Fatalf("%s: exit %d want %d, stderr must name %q: %+v", label, response.ExitCode, exit, text, response)
	}
}

// TestClaudeUnownedSubagentStopIsSkipped covers agents Praetor never dispatched: Claude
// Code's internal agents (empty agent_type, or the session's own agent name), and a launch
// whose receipt never bound an agent id (foreground or background-disabled). A deny would
// keep each of them running, so every one is a stated skip, never a hold.
func TestClaudeUnownedSubagentStopIsSkipped(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	pending := nativePayload(t, "PreToolUse", "session-fg", "Agent", "tool-fg",
		map[string]any{"prompt": validBrief}, nil)
	requireOutcome(t, "foreground reserve", runAgentHook(t, root, state, "claude", EventPreDispatch, pending), 0, "")
	for _, tc := range []struct{ name, session, agent, agentType, text string }{
		{"internal side question", "session-btw", "agent-btw", "", "The answer to your side question is yes."},
		{"internal agent under a named session agent", "session-named", "agent-suggest", "reviewer", "run the tests"},
		{"foreground launch without a receipt", "session-fg", "agent-fg", "general-purpose", proseReturn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := stopPayload(t, tc.session, tc.agent, tc.agentType, false, tc.text)
			requireOutcome(t, tc.name, runAgentHook(t, root, state, "claude", EventPostReturn, payload), 0,
				"no Praetor-owned dispatch binds this agent, skipped")
		})
	}
	prepareClaudeCorrelation(t, root, state, "session-owned", "tool-owned", "agent-owned", validBrief)
	owned := stopPayload(t, "session-owned", "agent-owned", "general-purpose", false, proseReturn)
	requireOutcome(t, "owned prose return", runAgentHook(t, root, state, "claude", EventPostReturn, owned), 2,
		"[BLOCKED BY HISS] subagent text register")
}

// TestClaudeUnownedHandbackIsSkipped: an uncorrelated agent's SubagentHandback is not a
// Praetor-owned report, so neither the pre-tool gate nor the receipt nor the abort holds it.
func TestClaudeUnownedHandbackIsSkipped(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	for _, tc := range []struct {
		event  Event
		native string
	}{
		{EventPreHandback, "PreToolUse"}, {EventHandbackReceipt, "PostToolUse"},
		{EventHandbackAbort, "PostToolUseFailure"}, {EventHandbackAbort, "PermissionDenied"},
	} {
		payload := handbackEventPayload(t, tc.native, "session-sdk", "agent-sdk", proseReturn)
		requireOutcome(t, string(tc.event)+"/"+tc.native, runAgentHook(t, root, state, "claude", tc.event, payload), 0,
			"no Praetor-owned dispatch binds this agent")
	}
	prepareClaudeCorrelation(t, root, state, "session-own", "tool-own", "agent-own", validBrief)
	valid := handbackPayload(t, "session-own", "agent-own", validReturn)
	requireOutcome(t, "owned handback", runAgentHook(t, root, state, "claude", EventPreHandback, valid), 0, "")
	receipt := handbackEventPayload(t, "PostToolUse", "session-own", "agent-own", validReturn)
	requireOutcome(t, "owned receipt", runAgentHook(t, root, state, "claude", EventHandbackReceipt, receipt), 0, "")
	requireOutcome(t, "second handback after delivery", runAgentHook(t, root, state, "claude", EventPreHandback, valid), 2,
		"already delivered")
}

// TestClaudeReturnStopHookActiveEscape: the first register violation keeps the subagent
// running with the reason; once a stop hook continued it, the next violation is a stated
// skip and the binding stays for a later resume.
func TestClaudeReturnStopHookActiveEscape(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	prepareClaudeCorrelation(t, root, state, "session-loop", "tool-loop", "agent-loop", validBrief)
	first := stopPayload(t, "session-loop", "agent-loop", "general-purpose", false, proseReturn)
	requireOutcome(t, "first violation", runAgentHook(t, root, state, "claude", EventPostReturn, first), 2,
		"[BLOCKED BY HISS]")
	null := stopPayload(t, "session-loop", "agent-loop", "general-purpose", nil, proseReturn)
	requireOutcome(t, "absent stop_hook_active", runAgentHook(t, root, state, "claude", EventPostReturn, null), 2,
		"[BLOCKED BY HISS]")
	continued := stopPayload(t, "session-loop", "agent-loop", "general-purpose", true, proseReturn)
	response := runAgentHook(t, root, state, "claude", EventPostReturn, continued)
	requireOutcome(t, "continued violation", response, 0, "not blocked again after a stop-hook continuation (stop_hook_active)")
	if bytes.Contains(response.Stderr, []byte("[BLOCKED BY HISS]")) {
		t.Fatalf("skip reason still claims a block: %q", response.Stderr)
	}
	valid := stopPayload(t, "session-loop", "agent-loop", "general-purpose", false, validReturn)
	requireOutcome(t, "resumed valid return", runAgentHook(t, root, state, "claude", EventPostReturn, valid), 0, "")
	if response := runAgentHook(t, root, state, "claude", EventPostReturn, valid); !strings.Contains(string(response.Stderr),
		"no Praetor-owned dispatch") {
		t.Fatalf("valid return did not release the binding: %+v", response)
	}
	wrongType := stopPayload(t, "session-loop", "agent-loop", "general-purpose", "yes", proseReturn)
	requireOutcome(t, "non-boolean stop_hook_active", runAgentHook(t, root, state, "claude", EventPostReturn, wrongType), 2,
		"stop_hook_active must be boolean")
}

// TestReturnStoreFailureIsNotAHold: a store the subagent cannot repair is a stated skip at
// SubagentStop, while the pre-launch gate keeps failing closed on the same fault.
func TestReturnStoreFailureIsNotAHold(t *testing.T) {
	root := repository(t, true)
	payload := stopPayload(t, "session-store", "agent-store", "general-purpose", false, proseReturn)
	requireOutcome(t, "return with broken store", runAgentHook(t, root, "relative/store", "claude", EventPostReturn, payload), 0,
		"subagent return not judged: correlation directory override must be absolute")
	brief := nativePayload(t, "PreToolUse", "session-store", "Agent", "tool-store",
		map[string]any{"prompt": validBrief, "run_in_background": true}, nil)
	requireOutcome(t, "dispatch with broken store", runAgentHook(t, root, "relative/store", "claude", EventPreDispatch, brief), 2,
		"reserve dispatch")
}

func TestCodexReturnWithoutUsableBodyIsSkipped(t *testing.T) {
	root := repository(t, true)
	for name, body := range map[string]any{"null": nil, "blank": " \n\t"} {
		payload := commandPayload(t, map[string]any{"hook_event_name": "SubagentStop", "session_id": "codex-session",
			"agent_id": "codex-agent", "last_assistant_message": body, "stop_hook_active": false})
		requireOutcome(t, name, runAgentHook(t, root, t.TempDir(), "codex", EventPostReturn, payload), 0,
			"last_assistant_message is absent")
	}
	missingAgent := commandPayload(t, map[string]any{"hook_event_name": "SubagentStop", "session_id": "codex-session"})
	requireOutcome(t, "missing agent id", runAgentHook(t, root, t.TempDir(), "codex", EventPostReturn, missingAgent), 0,
		"codex return not judged, its register is unenforceable: Invalid hook input: agent_id must be nonempty text")
}

func TestReturnBoundaryOnlyRewritesReturnDenies(t *testing.T) {
	deny := Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] subagent text register: C1"}
	row := func(client string, event Event) Registration {
		t.Helper()
		found, err := ParseArguments(client, string(event))
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	claudeReturn, codexReturn := row("claude", EventPostReturn), row("codex", EventPostReturn)
	claudeStop, claudeHandback := row("claude", EventStop), row("claude", EventPreHandback)
	for _, tc := range []struct {
		name   string
		row    Registration
		active bool
		in     Verdict
		want   Outcome
	}{
		{"first return violation", claudeReturn, false, deny, Deny},
		{"continued return violation", claudeReturn, true, deny, Skip},
		{"codex return failure", codexReturn, false, deny, Skip},
		{"allowed continued return", claudeReturn, true, Verdict{Outcome: Allow}, Allow},
		{"main-agent stop keeps its block", claudeStop, true, deny, Deny},
		{"pre-tool handback keeps its block", claudeHandback, true, deny, Deny},
	} {
		got := returnBoundary(tc.row, Canonical{Event: tc.row.Event, StopActive: tc.active}, tc.in)
		if got.Outcome != tc.want || (got.Outcome == Skip && strings.HasPrefix(got.Reason, "[BLOCKED BY HISS]")) {
			t.Errorf("%s: %+v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestClaudeSubagentStopMatcherExcludesInternalAgents replays the documented matcher rule:
// a matcher with characters outside [A-Za-z0-9_ ,|-] is an unanchored JavaScript regular
// expression, and one that cannot match "" never fires for an empty agent_type.
func TestClaudeSubagentStopMatcherExcludesInternalAgents(t *testing.T) {
	row, err := ParseArguments("claude", string(EventPostReturn))
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`^[A-Za-z0-9_ ,|-]*$`).MatchString(row.Matcher) {
		t.Fatalf("matcher %q is on Claude Code's exact-match path", row.Matcher)
	}
	matcher := regexp.MustCompile(row.Matcher)
	if matcher.MatchString("") {
		t.Fatalf("matcher %q fires for internal agents with an empty agent_type", row.Matcher)
	}
	for _, agentType := range []string{"general-purpose", "Explore", "my-plugin:reviewer", "x"} {
		if !matcher.MatchString(agentType) {
			t.Errorf("matcher %q misses agent_type %q", row.Matcher, agentType)
		}
	}
}

// TestRunSkipsAnEventNewerThanTheEngine: a registration naming an event this engine has
// never heard of is engine skew, answered with a stated skip in the client's own dialect.
// Misregistered known events, unknown clients, malformed arguments and agy stay usage errors.
func TestRunSkipsAnEventNewerThanTheEngine(t *testing.T) {
	root := repository(t, true)
	for _, client := range []string{"claude", "codex", "gemini", "lefthook"} {
		response := serve(t, client, "future-event", root, []byte(`{}`))
		requireOutcome(t, client, response, 0, "this praetorctl serves no future-event event")
		if !bytes.HasSuffix(response.Stderr, []byte(", skipped\n")) || len(response.Stdout) != 0 {
			t.Errorf("%s: skew is not a plain skip: %+v", client, response)
		}
	}
	for _, pair := range [][2]string{
		{"agy", "future-event"}, {"opencode", "future-event"}, {"codex", "pre-edit"}, {"gemini", "post-return"},
		{"claude", "future_event"}, {"claude", ""},
	} {
		requireOutcome(t, pair[0]+" "+pair[1], serve(t, pair[0], pair[1], root, []byte(`{}`)), usageExit,
			"usage: praetorctl hook <client> <event>")
	}
}
