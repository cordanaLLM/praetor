package agenthook

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	validBrief  = "goal: patch hook\ninputs: internal/agenthook\nreturn: diff plus tests\nevidence: focused tests\ntask: feature_implementation\n"
	validReturn = "verdict: pass\nchanged: internal/agenthook\nran: go test ./internal/agenthook\nevidence: exit 0\nopen: none\n"
	proseReturn = "verdict: I think this is complete\nchanged: the hook\nran: the tests\nevidence: the output\nopen: none\n"
)

type agentTextFixture struct {
	Name      string          `json:"name"`
	Allow     bool            `json:"allow"`
	Payload   json.RawMessage `json:"payload"`
	Oversized bool            `json:"oversized"`
}

type agentReturnFixture struct {
	Name      string `json:"name"`
	Capture   bool   `json:"capture"`
	Enforce   bool   `json:"enforce"`
	Body      string `json:"body"`
	Missing   bool   `json:"missing"`
	Oversized bool   `json:"oversized"`
}

func loadAgentTextFixtures(t *testing.T, client string) []agentTextFixture {
	t.Helper()
	return loadAgentTextFixtureFile(t, filepath.Join(client, "cases.json"))
}

func loadAgentTextFixtureFile(t *testing.T, name string) []agentTextFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "agent-text", name))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []agentTextFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 4 {
		t.Fatalf("%s fixtures = %d, want positive, negative, missing, oversized", name, len(fixtures))
	}
	return fixtures
}

func loadAgentReturnFixtures(t *testing.T) []agentReturnFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "agent-text", "return-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []agentReturnFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 4 {
		t.Fatalf("return fixtures = %d, want positive, negative, missing, oversized", len(fixtures))
	}
	return fixtures
}

func TestAgentBriefFixturesPerClaimedAdapter(t *testing.T) {
	for _, client := range []string{"codex", "claude", "gemini", "agy"} {
		for _, fixture := range loadAgentTextFixtures(t, client) {
			t.Run(client+"/"+fixture.Name, func(t *testing.T) {
				root := repository(t, true)
				payload := fixture.Payload
				if fixture.Oversized {
					payload = oversizedBriefPayload(t, client)
				}
				response := runAgentHook(t, root, t.TempDir(), client, EventPreDispatch, payload)
				allowed := response.ExitCode == 0
				if client == "agy" {
					allowed = agyStdoutAllows(t, string(EventPreDispatch), response)
				}
				if allowed != fixture.Allow {
					t.Fatalf("allow=%v want %v: %+v", allowed, fixture.Allow, response)
				}
			})
		}
	}
}

func TestAgentReturnFixturesPerRegisterEnforcingAdapter(t *testing.T) {
	for _, fixture := range loadAgentReturnFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			root, state := repository(t, true), t.TempDir()
			payload := prepareReturnFixture(t, root, state, fixture)
			response := runAgentHook(t, root, state, "claude", EventPostReturn, payload)
			if (response.ExitCode == 0) != fixture.Enforce {
				t.Fatalf("allow=%v want %v: %+v", response.ExitCode == 0, fixture.Enforce, response)
			}
		})
	}
}

func TestClaudeHandbackFixturesGateTheDeliveredReport(t *testing.T) {
	for _, fixture := range loadAgentTextFixtureFile(t, filepath.Join("claude", "handback-cases.json")) {
		t.Run(fixture.Name, func(t *testing.T) {
			root, state := repository(t, true), t.TempDir()
			prepareClaudeCorrelation(t, root, state, "fixture-session", "fixture-tool", "fixture-agent", validBrief)
			payload := fixture.Payload
			if fixture.Oversized {
				payload = nativePayload(t, "PreToolUse", "fixture-session", "SubagentHandback", "handback-tool",
					map[string]any{"message": strings.Repeat("x", MaxInputBytes)}, nil)
			}
			response := runAgentHook(t, root, state, "claude", Event("pre-handback"), payload)
			if (response.ExitCode == 0) != fixture.Allow {
				t.Fatalf("allow=%v want %v: %+v", response.ExitCode == 0, fixture.Allow, response)
			}
		})
	}
}

func TestCodexReturnCaptureReportsUnenforceableCorrelation(t *testing.T) {
	root := repository(t, true)
	for _, fixture := range loadAgentReturnFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			fields := map[string]any{
				"hook_event_name": "SubagentStop",
				"session_id":      "fixture-session",
				"agent_id":        "fixture-agent",
			}
			if !fixture.Missing {
				body := fixture.Body
				if fixture.Oversized {
					body = strings.Repeat("x", MaxInputBytes)
				}
				fields["last_assistant_message"] = body
			}
			response := runAgentHook(t, root, t.TempDir(), "codex", EventPostReturn, commandPayload(t, fields))
			if (response.ExitCode == 0) != fixture.Capture {
				t.Fatalf("capture=%v want %v: %+v", response.ExitCode == 0, fixture.Capture, response)
			}
			if fixture.Capture && !strings.Contains(string(response.Stderr), "return register unenforceable") {
				t.Fatalf("capture hid register gap: %+v", response)
			}
		})
	}
}

func TestClaudeHandbackCorrelationRetainsInvalidRetryAndIgnoresClosingText(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	prepareClaudeCorrelation(t, root, state, "session-1", "tool-1", "agent-1", validBrief)
	invalid := handbackPayload(t, "session-1", "agent-1", proseReturn)
	if response := runAgentHook(t, root, state, "claude", EventPreHandback, invalid); response.ExitCode != 2 {
		t.Fatalf("invalid handback: %+v", response)
	}
	valid := handbackPayload(t, "session-1", "agent-1", validReturn)
	if response := runAgentHook(t, root, state, "claude", EventPreHandback, valid); response.ExitCode != 0 {
		t.Fatalf("valid retry lost correlation: %+v", response)
	}
	mismatch := handbackEventPayload(t, "PostToolUse", "session-1", "agent-1", proseReturn)
	if response := runAgentHook(t, root, state, "claude", EventHandbackReceipt, mismatch); response.ExitCode != 2 ||
		!strings.Contains(string(response.Stderr), "does not match") {
		t.Fatalf("mismatched handback receipt accepted: %+v", response)
	}
	receipt := handbackEventPayload(t, "PostToolUse", "session-1", "agent-1", validReturn)
	if response := runAgentHook(t, root, state, "claude", EventHandbackReceipt, receipt); response.ExitCode != 0 {
		t.Fatalf("valid handback receipt: %+v", response)
	}
	if response := runAgentHook(t, root, state, "claude", EventHandbackReceipt, receipt); response.ExitCode != 2 ||
		!strings.Contains(string(response.Stderr), "already delivered") {
		t.Fatalf("duplicate handback receipt accepted: %+v", response)
	}
	closing := returnPayload(t, "SubagentStop", "session-1", "agent-1", "Report delivered through SubagentHandback.")
	if response := runAgentHook(t, root, state, "claude", EventPostReturn, closing); response.ExitCode != 0 {
		t.Fatalf("closing text was mistaken for the report: %+v", response)
	}
	if response := runAgentHook(t, root, state, "claude", EventPostReturn, closing); response.ExitCode != 2 ||
		!strings.Contains(string(response.Stderr), "correlation missing") {
		t.Fatalf("completed correlation remained: %+v", response)
	}
}

func TestClaudeHandbackIsNotDeliveredUntilTheToolSucceeds(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	prepareClaudeCorrelation(t, root, state, "session-delivery", "tool-delivery", "agent-delivery", validBrief)
	handback := handbackPayload(t, "session-delivery", "agent-delivery", validReturn)
	if response := runAgentHook(t, root, state, "claude", EventPreHandback, handback); response.ExitCode != 0 {
		t.Fatalf("valid pre-handback: %+v", response)
	}
	closing := returnPayload(t, "SubagentStop", "session-delivery", "agent-delivery", proseReturn)
	if response := runAgentHook(t, root, state, "claude", EventPostReturn, closing); response.ExitCode != 2 {
		t.Fatalf("failed handback was recorded as delivered: %+v", response)
	}
}

func TestClaudeFailedOrDeniedHandbackRetainsFallbackValidation(t *testing.T) {
	for _, nativeEvent := range []string{"PostToolUseFailure", "PermissionDenied"} {
		t.Run(nativeEvent, func(t *testing.T) {
			root, state := repository(t, true), t.TempDir()
			prepareClaudeCorrelation(t, root, state, "session-abort", "tool-abort", "agent-abort", validBrief)
			pre := handbackPayload(t, "session-abort", "agent-abort", validReturn)
			if response := runAgentHook(t, root, state, "claude", EventPreHandback, pre); response.ExitCode != 0 {
				t.Fatalf("pre-handback: %+v", response)
			}
			abort := handbackEventPayload(t, nativeEvent, "session-abort", "agent-abort", validReturn)
			if response := runAgentHook(t, root, state, "claude", EventHandbackAbort, abort); response.ExitCode != 0 {
				t.Fatalf("handback abort: %+v", response)
			}
			invalid := returnPayload(t, "SubagentStop", "session-abort", "agent-abort", proseReturn)
			if response := runAgentHook(t, root, state, "claude", EventPostReturn, invalid); response.ExitCode != 2 {
				t.Fatalf("failed handback bypassed fallback: %+v", response)
			}
			valid := returnPayload(t, "SubagentStop", "session-abort", "agent-abort", validReturn)
			if response := runAgentHook(t, root, state, "claude", EventPostReturn, valid); response.ExitCode != 0 {
				t.Fatalf("valid fallback retry: %+v", response)
			}
		})
	}
}

func TestClaudeHandbackTransitionsAreBoundToTheToolUse(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	prepareClaudeCorrelation(t, root, state, "session-tools", "dispatch-tool", "agent-tools", validBrief)
	first := handbackEventPayloadWithID(t, "PreToolUse", "session-tools", "agent-tools", "handback-old", validReturn)
	if response := runAgentHook(t, root, state, "claude", EventPreHandback, first); response.ExitCode != 0 {
		t.Fatalf("first pre-handback: %+v", response)
	}
	second := handbackEventPayloadWithID(t, "PreToolUse", "session-tools", "agent-tools", "handback-new", validReturn)
	if response := runAgentHook(t, root, state, "claude", EventPreHandback, second); response.ExitCode != 0 {
		t.Fatalf("replacement pre-handback: %+v", response)
	}
	stale := handbackEventPayloadWithID(t, "PostToolUseFailure", "session-tools", "agent-tools", "handback-old", validReturn)
	if response := runAgentHook(t, root, state, "claude", EventHandbackAbort, stale); response.ExitCode != 2 ||
		!strings.Contains(string(response.Stderr), "does not match") {
		t.Fatalf("stale handback abort cleared a newer validation: %+v", response)
	}
	receipt := handbackEventPayloadWithID(t, "PostToolUse", "session-tools", "agent-tools", "handback-new", validReturn)
	if response := runAgentHook(t, root, state, "claude", EventHandbackReceipt, receipt); response.ExitCode != 0 {
		t.Fatalf("current handback receipt: %+v", response)
	}
}

func TestClaudeHandbackCorrelationCrossesAnIsolatedGitWorktree(t *testing.T) {
	root := repository(t, true)
	if _, err := util.RunGit(t.Context(), root, "add", manifestName); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(t.Context(), root, "-c", "user.name=Praetor Test", "-c", "user.email=test@example.invalid",
		"commit", "-q", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(t.TempDir(), "isolated-agent")
	if _, err := util.RunGit(t.Context(), root, "worktree", "add", "--detach", child, "HEAD"); err != nil {
		t.Fatal(err)
	}
	prepareClaudeCorrelation(t, root, "", "worktree-session", "worktree-tool", "worktree-agent", validBrief)
	handback := commandPayload(t, map[string]any{
		"hook_event_name": "PreToolUse", "session_id": "worktree-session", "agent_id": "worktree-agent",
		"agent_type": "general-purpose", "cwd": child, "tool_name": "SubagentHandback", "tool_use_id": "handback-tool",
		"tool_input": map[string]any{"message": validReturn},
	})
	if response := runAgentHook(t, child, "", "claude", EventPreHandback, handback); response.ExitCode != 0 {
		t.Fatalf("isolated handback lost parent correlation: %+v", response)
	}
	receipt := commandPayload(t, map[string]any{
		"hook_event_name": "PostToolUse", "session_id": "worktree-session", "agent_id": "worktree-agent",
		"agent_type": "general-purpose", "cwd": child, "tool_name": "SubagentHandback", "tool_use_id": "handback-tool",
		"tool_input": map[string]any{"message": validReturn},
	})
	if response := runAgentHook(t, child, "", "claude", EventHandbackReceipt, receipt); response.ExitCode != 0 {
		t.Fatalf("isolated receipt lost parent correlation: %+v", response)
	}
	closing := commandPayload(t, map[string]any{
		"hook_event_name": "SubagentStop", "session_id": "worktree-session", "agent_id": "worktree-agent", "cwd": child,
	})
	if response := runAgentHook(t, child, "", "claude", EventPostReturn, closing); response.ExitCode != 0 {
		t.Fatalf("isolated stop did not finish correlation: %+v", response)
	}
}

func TestClaudeFailedOrDeniedDispatchReleasesPendingCorrelation(t *testing.T) {
	for _, nativeEvent := range []string{"PostToolUseFailure", "PermissionDenied"} {
		t.Run(nativeEvent, func(t *testing.T) {
			root, state := repository(t, true), t.TempDir()
			pre := nativePayload(t, "PreToolUse", "abort-session", "Agent", "abort-tool",
				map[string]any{"prompt": validBrief, "run_in_background": true}, nil)
			if response := runAgentHook(t, root, state, "claude", EventPreDispatch, pre); response.ExitCode != 0 {
				t.Fatalf("reserve: %+v", response)
			}
			abort := nativePayload(t, nativeEvent, "abort-session", "Agent", "abort-tool",
				map[string]any{"prompt": validBrief}, nil)
			if response := runAgentHook(t, root, state, "claude", Event("dispatch-abort"), abort); response.ExitCode != 0 {
				t.Fatalf("cleanup: %+v", response)
			}
			if response := runAgentHook(t, root, state, "claude", EventPreDispatch, pre); response.ExitCode != 0 {
				t.Fatalf("recovery reserve: %+v", response)
			}
		})
	}
	root := repository(t, true)
	missingID := nativePayload(t, "PostToolUseFailure", "abort-session", "Agent", "",
		map[string]any{"prompt": validBrief}, nil)
	if response := runAgentHook(t, root, t.TempDir(), "claude", Event("dispatch-abort"), missingID); response.ExitCode != 2 {
		t.Fatalf("abort without correlation id accepted: %+v", response)
	}
}

func TestClaudeReturnRespectsStoredSocialRegister(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	brief := "task: commit_message_synthesis\ngoal: I will draft a clear commit message for the change.\n"
	pre := nativePayload(t, "PreToolUse", "social-session", "Agent", "social-tool",
		map[string]any{"prompt": brief, "run_in_background": true}, nil)
	receipt := nativePayload(t, "PostToolUse", "social-session", "Agent", "social-tool", map[string]any{"prompt": brief},
		map[string]any{"status": "async_launched", "agentId": "social-agent"})
	if response := runAgentHook(t, root, state, "claude", EventPreDispatch, pre); response.ExitCode != 0 {
		t.Fatalf("brief: %+v", response)
	}
	if response := runAgentHook(t, root, state, "claude", EventDispatchReceipt, receipt); response.ExitCode != 0 {
		t.Fatalf("receipt: %+v", response)
	}
	result := returnPayload(t, "SubagentStop", "social-session", "social-agent", "This commit fixes the native hook boundary.")
	if response := runAgentHook(t, root, state, "claude", EventPostReturn, result); response.ExitCode != 0 {
		t.Fatalf("social return: %+v", response)
	}
}

func TestCodexBriefDoesNotReserveUncorrelatableState(t *testing.T) {
	root, state := repository(t, true), t.TempDir()
	pre := nativePayload(t, "PreToolUse", "session-2", "spawn_agent", "tool-2", map[string]any{"message": validBrief}, nil)
	if response := runAgentHook(t, root, state, "codex", EventPreDispatch, pre); response.ExitCode != 0 {
		t.Fatalf("brief: %+v", response)
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 0 {
		t.Fatalf("codex brief left unusable correlation state: entries=%v err=%v", entries, err)
	}
}

func TestAgentTrafficBoundaries(t *testing.T) {
	root := repository(t, true)
	foreground := nativePayload(t, "PreToolUse", "session", "Agent", "tool", map[string]any{
		"prompt": validBrief, "run_in_background": false,
	}, nil)
	if response := runAgentHook(t, root, t.TempDir(), "claude", EventPreDispatch, foreground); response.ExitCode != 2 {
		t.Fatalf("foreground Claude dispatch accepted: %+v", response)
	}
	subagents := make([]map[string]any, MaxDispatchBriefs+1)
	for index := 0; index < len(subagents); index++ {
		subagents[index] = map[string]any{"Prompt": validBrief}
	}
	agy := commandPayload(t, map[string]any{"toolCall": map[string]any{"name": "invoke_subagent", "args": map[string]any{"Subagents": subagents}}})
	if response := runAgentHook(t, root, t.TempDir(), "agy", EventPreDispatch, agy); agyStdoutAllows(t, string(EventPreDispatch), response) {
		t.Fatalf("oversized agy batch accepted: %+v", response)
	}
	for _, client := range []string{"opencode-v1", "continue", "cline", "kilo"} {
		for _, event := range []Event{EventPreDispatch, EventDispatchReceipt, EventPostReturn} {
			if response := runAgentHook(t, root, t.TempDir(), client, event, []byte(`{}`)); response.ExitCode != usageExit {
				t.Fatalf("unsupported %s %s accepted: %+v", client, event, response)
			}
		}
	}
}

func TestClaudeAgentAndHandbackToolsCannotSubstituteForEachOther(t *testing.T) {
	dialect, ok := DialectFor("claude")
	if !ok {
		t.Fatal("claude dialect missing")
	}
	handback := handbackPayload(t, "session", "agent", validReturn)
	if _, err := dialect.Decode(EventDispatchReceipt, handback); err == nil {
		t.Fatal("SubagentHandback accepted as an Agent dispatch receipt")
	}
	agent := nativePayload(t, "PreToolUse", "session", "Agent", "tool", map[string]any{"message": validReturn}, nil)
	if _, err := dialect.Decode(EventPreHandback, agent); err == nil {
		t.Fatal("Agent accepted as a SubagentHandback report")
	}
}

func prepareReturnFixture(t *testing.T, root, state string, fixture agentReturnFixture) []byte {
	t.Helper()
	body := fixture.Body
	if fixture.Oversized {
		body = strings.Repeat("x", MaxInputBytes)
	}
	prepareClaudeCorrelation(t, root, state, "fixture-session", "fixture-tool", "fixture-agent", validBrief)
	fields := map[string]any{"hook_event_name": "SubagentStop", "session_id": "fixture-session", "agent_id": "fixture-agent"}
	if !fixture.Missing {
		fields["last_assistant_message"] = body
	}
	return commandPayload(t, fields)
}

func prepareClaudeCorrelation(t *testing.T, root, state, session, toolID, agentID, brief string) {
	t.Helper()
	input := map[string]any{"prompt": brief, "run_in_background": true}
	receipt := map[string]any{"status": "async_launched", "agentId": agentID}
	pre := nativePayload(t, "PreToolUse", session, "Agent", toolID, input, nil)
	post := nativePayload(t, "PostToolUse", session, "Agent", toolID, map[string]any{"prompt": brief}, receipt)
	if response := runAgentHook(t, root, state, "claude", EventPreDispatch, pre); response.ExitCode != 0 {
		t.Fatalf("prepare brief: %+v", response)
	}
	if response := runAgentHook(t, root, state, "claude", EventDispatchReceipt, post); response.ExitCode != 0 {
		t.Fatalf("prepare receipt: %+v", response)
	}
}

func runAgentHook(t *testing.T, root, state, client string, event Event, payload []byte) Response {
	t.Helper()
	return Run(context.Background(), Invocation{Client: client, Event: string(event), Stdin: bytes.NewReader(payload),
		Getenv: noEnvironment, WorkDir: root, Policy: policy(t), CorrelationDir: state})
}

func nativePayload(t *testing.T, event, session, tool, toolID string, input, response map[string]any) []byte {
	t.Helper()
	fields := map[string]any{"hook_event_name": event, "session_id": session, "tool_name": tool, "tool_input": input}
	if toolID != "" {
		fields["tool_use_id"] = toolID
	}
	if response != nil {
		fields["tool_response"] = response
	}
	return commandPayload(t, fields)
}

func returnPayload(t *testing.T, event, session, agent, text string) []byte {
	t.Helper()
	return commandPayload(t, map[string]any{"hook_event_name": event, "session_id": session, "agent_id": agent,
		"last_assistant_message": text})
}

func handbackPayload(t *testing.T, session, agent, text string) []byte {
	t.Helper()
	return handbackEventPayload(t, "PreToolUse", session, agent, text)
}

func handbackEventPayload(t *testing.T, event, session, agent, text string) []byte {
	t.Helper()
	return handbackEventPayloadWithID(t, event, session, agent, "handback-tool", text)
}

func handbackEventPayloadWithID(t *testing.T, event, session, agent, toolID, text string) []byte {
	t.Helper()
	return commandPayload(t, map[string]any{
		"hook_event_name": event, "session_id": session, "agent_id": agent,
		"agent_type": "general-purpose", "tool_name": "SubagentHandback", "tool_use_id": toolID,
		"tool_input": map[string]any{"message": text},
	})
}

func oversizedBriefPayload(t *testing.T, client string) []byte {
	t.Helper()
	body := strings.Repeat("x", MaxInputBytes)
	if client == "agy" {
		return commandPayload(t, map[string]any{"conversationId": "session", "toolCall": map[string]any{
			"name": "invoke_subagent", "args": map[string]any{"Subagents": []map[string]any{{"Prompt": body}}},
		}})
	}
	tool, key, event := "invoke_agent", "prompt", "BeforeTool"
	if client == "claude" {
		tool, event = "Agent", "PreToolUse"
	}
	if client == "codex" {
		tool, key, event = "spawn_agent", "message", "PreToolUse"
	}
	return nativePayload(t, event, "session", tool, "tool", map[string]any{key: body}, nil)
}
