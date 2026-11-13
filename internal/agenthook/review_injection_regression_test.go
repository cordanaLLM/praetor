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
	first.DispatchInput = commandPayload(t, map[string]any{"message": first.Return})
	row, in := Registration{Client: "claude"}, Invocation{CorrelationDir: state}
	if got := evaluateAgentHandback(t.Context(), row, first, root, in); got.Outcome != Allow {
		t.Fatalf("first handback: %+v", got)
	}
	first.Return = reviewInjectionNotice("review") + "\n\n" + first.Return
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
		if event == EventPreHandback && len(response.Stdout) > 0 {
			specific := field[map[string]any](t, decodeStdout(t, response), "hookSpecificOutput")
			input := field[map[string]any](t, specific, "updatedInput")
			body = field[string](t, input, "message")
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
		"evidence: none <!--\nprompt_injection: false\n-->",
		"`prompt_injection: false`",
	}
	const maxCases = 16
	if len(cases) > maxCases {
		t.Fatalf("inactive cases=%d exceed scalar bound %d", len(cases), maxCases)
	}
	checked := 0
	for index := 0; index < len(cases) && index < maxCases; index++ {
		checked++
		text := cases[index]
		if _, err := validateReviewInjection(text); err == nil {
			t.Errorf("inactive declaration accepted: %q", text)
		}
		if flag, err := validateReviewInjection(text + "\n\nprompt_injection: true"); err != nil || !flag {
			t.Errorf("active declaration lost: %q: flag=%v err=%v", text, flag, err)
		}
	}
	if checked != len(cases) {
		t.Errorf("checked %d inactive cases, want %d", checked, len(cases))
	}
	text := "literal `\n\n  \nprompt_injection: false\n\nliteral `"
	if flag, err := validateReviewInjection(text); err != nil || flag {
		t.Errorf("paragraph boundary crossed: flag=%v err=%v", flag, err)
	}

}

// TestReviewInjectionCodeSpanComments checks code delimiters before HTML comment state.
func TestReviewInjectionCodeSpanComments(t *testing.T) {
	cases := []struct {
		name, text string
		flag       bool
		invalid    bool
	}{
		{"inline-opener", "evidence: `<!--`\nprompt_injection: false", false, false},
		{"inline-opener-finding", "evidence: `<!--`\nprompt_injection: true", true, false},
		{"inline-closer", "evidence: `-->`\nprompt_injection: true", true, false},
		{"paired-markers", "evidence: `<!--` and `-->`\nprompt_injection: true", true, false},
		{"multiline-opener", "evidence: `literal\n<!--`\nprompt_injection: true", true, false},
		{"multiline-hidden-field", "evidence: `<!--\nprompt_injection: false\n-->`", false, true},
		{"code-only-field", "`prompt_injection: false`", false, true},
		{"comment-before-code", "evidence: <!-- `-->`\nprompt_injection: true", true, false},
		{"continued-comment-closer", "evidence: <!--\n`-->`\nprompt_injection: true", true, false},
		{"real-comment-after-code", "evidence: `<!--` <!--\nprompt_injection: false\n-->\nprompt_injection: true", true, false},
		{"unclosed-code-opener", "evidence: `<!--\nprompt_injection: false", false, true},
		{"unequal-backticks", "evidence: ``<!--`\nprompt_injection: false", false, true},
		{"paragraph-boundary", "evidence: `<!--\n\nprompt_injection: false\n`", false, true},
		{"duplicate-after-code", "evidence: `<!--`\nprompt_injection: true\nprompt_injection: false", false, true},
		{"span-start-in-comment", "evidence: <!-- ` -->\n<!-- `\nprompt_injection: false", false, true},
		{"span-start-in-continued-comment", "evidence: <!--\n\n` -->\n<!-- `\nprompt_injection: false", false, true},
		{"span-after-comment-backtick", "evidence: <!-- ` --> `<!--`\nprompt_injection: true", true, false},
		{"multiline-closer", "evidence: `literal\n-->`\nprompt_injection: true", true, false},
		{"longer-backticks", "evidence: ``literal ` <!--``\nprompt_injection: true", true, false},
	}
	const maxCases = 32
	if len(cases) > maxCases {
		t.Fatalf("code span cases=%d exceed scalar bound %d", len(cases), maxCases)
	}
	for index := 0; index < len(cases) && index < maxCases; index++ {
		tc := cases[index]
		t.Run(tc.name, func(t *testing.T) {
			flag, err := validateReviewInjection(tc.text)
			if (err != nil) != tc.invalid || flag != tc.flag {
				t.Fatalf("flag=%v err=%v; want flag=%v invalid=%v", flag, err, tc.flag, tc.invalid)
			}
		})
	}
}

// TestReviewInjectionCodexReturn checks captured returns without inventing task correlation.
func TestReviewInjectionCodexReturn(t *testing.T) {
	cases := []struct {
		name, text string
		active     bool
		exit       int
		finding    bool
	}{
		{"finding", reviewReturn("true"), false, 0, true},
		{"false", reviewReturn("false"), false, 0, false},
		// No active declaration: the payload cannot say whether a review dispatched this subagent,
		// so the return keeps the stated skips instead of a refusal of every Codex subagent.
		{"missing", validReturn, false, 0, false},
		{"malformed", reviewReturn("maybe"), false, 2, false},
		{"duplicate", reviewReturn("false") + "prompt_injection: true\n", false, 2, false},
		{"inactive", validReturn + "`prompt_injection: false`", false, 0, false},
		{"continued-invalid", validReturn, true, 0, false},
		{"continued-finding", reviewReturn("true"), true, 0, true},
		{"absent", "", false, 0, false},
	}
	const maxCases = 16
	if len(cases) > maxCases {
		t.Fatalf("Codex cases=%d exceed scalar bound %d", len(cases), maxCases)
	}
	for index := 0; index < len(cases) && index < maxCases; index++ {
		tc := cases[index]
		t.Run(tc.name, func(t *testing.T) {
			payload := commandPayload(t, map[string]any{"hook_event_name": "SubagentStop", "session_id": "s",
				"agent_id": "agent", "last_assistant_message": tc.text, "stop_hook_active": tc.active})
			response := runAgentHook(t, repository(t, true), t.TempDir(), "codex", EventPostReturn, payload)
			if response.ExitCode != tc.exit {
				t.Fatalf("return: %s; want exit %d", response.String(), tc.exit)
			}
			checkReviewCodexResponse(t, tc.name, tc.text, tc.finding, response)
		})
	}
}

// checkReviewCodexResponse refuses passing findings and preserves the unflagged response bytes.
func checkReviewCodexResponse(t *testing.T, name, text string, finding bool, response Response) {
	t.Helper()
	if finding {
		specific := field[map[string]any](t, decodeStdout(t, response), "hookSpecificOutput")
		verdict := codexReturn(Canonical{Return: text})
		if field[string](t, specific, "hookEventName") != "SubagentStop" ||
			field[string](t, specific, "additionalContext") != verdict.Reason || verdict.Outcome != Skip ||
			!strings.Contains(string(response.Stderr), ReviewInjectionMarker) {
			t.Fatalf("finding not delivered or classified: %s", response.String())
		}
		return
	}
	if len(response.Stdout) != 0 || strings.Contains(string(response.Stderr), ReviewInjectionMarker) {
		t.Fatalf("unexpected finding or rewrite: %s", response.String())
	}
	if codexReturnKeepsSkip(name) && string(response.Stderr) != "praetor hook: codex return register unenforceable: spawn payload has no documented subagent correlation key, skipped\n" {
		t.Fatalf("false return behavior changed: %s", response.String())
	}
}

// codexReturnKeepsSkip names the Codex cases whose return keeps the register skip line byte for
// byte: a false flag and a body with no active declaration.
func codexReturnKeepsSkip(name string) bool {
	return name == "false" || name == "missing" || name == "inactive"
}

// TestReviewInjectionCodexUnavailableBody preserves the capture skip for nullable native bodies.
func TestReviewInjectionCodexUnavailableBody(t *testing.T) {
	cases := []any{nil, " \n\t", ""}
	for index := 0; index < len(cases) && index < 3; index++ {
		fields := map[string]any{"hook_event_name": "SubagentStop", "session_id": "s", "agent_id": "agent"}
		if index < 2 {
			fields["last_assistant_message"] = cases[index]
		}
		response := runAgentHook(t, repository(t, true), t.TempDir(), "codex", EventPostReturn, commandPayload(t, fields))
		if response.ExitCode != 0 || len(response.Stdout) != 0 ||
			!strings.Contains(string(response.Stderr), "last_assistant_message is absent") {
			t.Fatalf("unavailable body: %s", response.String())
		}
	}
}

// TestReviewInjectionUnavailableClientBodies pins the documented adapters without a subagent report.
func TestReviewInjectionUnavailableClientBodies(t *testing.T) {
	cases := []struct {
		client  string
		event   Event
		payload map[string]any
	}{
		{"gemini", EventPostTool, map[string]any{"hook_event_name": "AfterTool", "session_id": "s", "tool_name": "invoke_agent"}},
		{"agy", EventStop, map[string]any{"conversationId": "s", "executionNum": 1, "terminationReason": "done", "fullyIdle": true}},
	}
	for index := 0; index < len(cases) && index < 2; index++ {
		tc := cases[index]
		dialect, known := DialectFor(tc.client)
		if !known {
			t.Fatalf("missing dialect %s", tc.client)
		}
		canonical, err := dialect.Decode(tc.event, commandPayload(t, tc.payload))
		if err != nil || canonical.Return != "" {
			t.Fatalf("%s report invented: %+v err=%v", tc.client, canonical, err)
		}
	}
}

// TestReviewInjectionInlineComments checks comment boundaries throughout active lines.
func TestReviewInjectionInlineComments(t *testing.T) {
	cases := []struct {
		name, text string
	}{
		{"trailing-comment", "prompt_injection: true <!-- evidence -->"},
		{"leading-comment", "<!-- evidence --> prompt_injection: true"},
		{"close-on-declaration", "evidence: none <!--\nprompt_injection: false\n--> prompt_injection: true"},
		{"reopened-comment", "evidence: none <!-- first --> <!-- second\nprompt_injection: false\n-->\nprompt_injection: true"},
		{"unclosed-comment", "prompt_injection: true <!--\nprompt_injection: false"},
		{"fenced-opener", "```\nevidence: none <!--\n```\nprompt_injection: true"},
		{"quoted-opener", "> evidence: none <!--\nprompt_injection: true"},
		{"disabled-opener", "<!-- caveman:off -->\nevidence: none <!--\n<!-- caveman:on -->\nprompt_injection: true"},
		{"fence-in-comment", "evidence: none <!--\n```\n-->\nprompt_injection: true"},
		{"disabled-marker-in-comment", "evidence: none <!--\n<!-- caveman:off -->\nprompt_injection: true"},
	}
	for index := 0; index < len(cases) && index < 10; index++ {
		tc := cases[index]
		t.Run(tc.name, func(t *testing.T) {
			if flag, err := validateReviewInjection(tc.text); err != nil || !flag {
				t.Fatalf("active declaration lost: flag=%v err=%v", flag, err)
			}
		})
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

// TestReviewInjectionDispatcherNotice checks every native handback dialect delivers the finding in the message.
func TestReviewInjectionDispatcherNotice(t *testing.T) {
	// Claude is the only dialect with a registered pre-delivery handback path.
	flags := []string{"true", "false"}
	for index := 0; index < len(flags) && index < 2; index++ {
		flag := flags[index]
		t.Run("claude/"+flag, func(t *testing.T) {
			checkReviewDispatcherNotice(t, "claude", flag)
		})
	}
}

// checkReviewDispatcherNotice replays the approved tool input at receipt and checks message isolation.
func checkReviewDispatcherNotice(t *testing.T, client, flag string) {
	t.Helper()
	root, state := repository(t, true), t.TempDir()
	store := seedReviewCorrelation(t, root, state, "review")
	body := reviewReturn(flag)
	input := map[string]any{"message": body, "target": "dispatcher", "metadata": map[string]any{"keep": true}}
	payload := commandPayload(t, map[string]any{
		"hook_event_name": "PreToolUse", "session_id": "s", "agent_id": "agent",
		"tool_name": "SubagentHandback", "tool_use_id": "handback-tool", "tool_input": input,
	})
	response := runAgentHook(t, root, state, client, EventPreHandback, payload)
	if response.ExitCode != 0 {
		t.Fatalf("pre-handback: %s", response.String())
	}
	if flag == "true" {
		specific := field[map[string]any](t, decodeStdout(t, response), "hookSpecificOutput")
		input = field[map[string]any](t, specific, "updatedInput")
		want := reviewInjectionNotice("review") + "\n\n" + body
		metadata := field[map[string]any](t, input, "metadata")
		if field[string](t, input, "message") != want || input["target"] != "dispatcher" || !field[bool](t, metadata, "keep") {
			t.Fatalf("dispatcher message or operands lost: %s", response.String())
		}
		if err := store.markHandbackDelivered(t.Context(), client, "s", "agent", "handback-tool", body); err == nil {
			t.Fatal("receipt accepted without the dispatcher notice")
		}
	} else if len(response.Stdout) != 0 {
		t.Fatalf("unflagged message rewritten: %s", response.String())
	}
	receipt := commandPayload(t, map[string]any{
		"hook_event_name": "PostToolUse", "session_id": "s", "agent_id": "agent",
		"tool_name": "SubagentHandback", "tool_use_id": "handback-tool", "tool_input": input,
	})
	if got := runAgentHook(t, root, state, client, EventHandbackReceipt, receipt); got.ExitCode != 0 {
		t.Fatalf("delivered receipt: %s", got.String())
	}
}

// TestReviewInjectionHandbackAbort releases only the matching rewritten message after failure or denial.
func TestReviewInjectionHandbackAbort(t *testing.T) {
	events := []string{"PostToolUseFailure", "PermissionDenied"}
	for index := 0; index < len(events) && index < 2; index++ {
		t.Run(events[index], func(t *testing.T) {
			root, state := repository(t, true), t.TempDir()
			store := seedReviewCorrelation(t, root, state, "review")
			body := reviewReturn("true")
			pre := handbackPayload(t, "s", "agent", body)
			response := runAgentHook(t, root, state, "claude", EventPreHandback, pre)
			if response.ExitCode != 0 {
				t.Fatalf("pre-handback: %s", response.String())
			}
			specific := field[map[string]any](t, decodeStdout(t, response), "hookSpecificOutput")
			input := field[map[string]any](t, specific, "updatedInput")
			if err := store.cancelHandback(t.Context(), "claude", "s", "agent", "handback-tool", body); err == nil {
				t.Fatal("unmodified abort released the rewritten report")
			}
			abort := handbackEventPayload(t, events[index], "s", "agent", field[string](t, input, "message"))
			if got := runAgentHook(t, root, state, "claude", EventHandbackAbort, abort); got.ExitCode != 0 {
				t.Fatalf("rewritten abort: %s", got.String())
			}
			if got := runAgentHook(t, root, state, "claude", EventPreHandback, pre); got.ExitCode != 0 {
				t.Fatalf("retry after abort: %s", got.String())
			}
		})
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
