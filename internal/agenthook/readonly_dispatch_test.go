package agenthook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
)

const readOnlyAgents = "# Project\n\nBefore concluding any turn:\n```bash\nmake verify-all\n```\n\n" +
	"## Rules\n\n10. **State ledger discipline (HISS-17).** Keep ledger private.\n    - Turn end: `praetorctl state sync .`\n"

// readOnlyRepository is a governed repository holding AGENTS.md and, with compiled, the
// AGENTS.readonly.md compile-context writes from it; it returns the root and the projection.
func readOnlyRepository(t *testing.T, compiled bool) (string, string) {
	t.Helper()
	root := repository(t, true)
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(readOnlyAgents), 0o600); err != nil {
		t.Fatal(err)
	}
	projection, err := agentcontext.ReadOnlyProjection(readOnlyAgents)
	if err != nil {
		t.Fatal(err)
	}
	if compiled {
		if err := os.WriteFile(filepath.Join(root, agentcontext.CanonicalReadOnlyFile), []byte(projection), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, projection
}

func decodeStdout(t *testing.T, resp Response) map[string]any {
	t.Helper()
	var output map[string]any
	if err := json.Unmarshal(resp.Stdout, &output); err != nil {
		t.Fatalf("stdout is not one JSON object: %v: %s (stderr %s)", err, resp.Stdout, resp.Stderr)
	}
	return output
}

// field returns object[key] as a T, failing the test when it is absent or another type.
func field[T any](t *testing.T, object map[string]any, key string) T {
	t.Helper()
	return as[T](t, object[key], key)
}

// as returns value as a T, failing the test, which names what, when it is another type.
func as[T any](t *testing.T, value any, what string) T {
	t.Helper()
	typed, ok := value.(T)
	if !ok {
		t.Fatalf("%s is %T, want %T: %v", what, value, typed, value)
	}
	return typed
}

func claudeDispatch(t *testing.T, session, toolID, brief string, extra map[string]any) []byte {
	t.Helper()
	input := map[string]any{"prompt": brief, "run_in_background": true, "description": "review the diff"}
	for key, value := range extra {
		input[key] = value
	}
	return nativePayload(t, "PreToolUse", session, "Agent", toolID, input, nil)
}

// TestReadOnlyDispatch_Positive_ClaudeRewritesThePrompt: a brief marked read-only reaches the
// subagent through updatedInput: the whole tool input comes back, every field unchanged but
// the prompt, which opens with the banner and the compiled projection ahead of the brief. The
// parent receives no additionalContext.
func TestReadOnlyDispatch_Positive_ClaudeRewritesThePrompt(t *testing.T) {
	root, projection := readOnlyRepository(t, true)
	brief := validBrief + "readonly: true\n"
	resp := runAgentHook(t, root, t.TempDir(), "claude", EventPreDispatch,
		claudeDispatch(t, "sess-ro", "tool-ro", brief, map[string]any{"subagent_type": "general-purpose", "model": "sonnet"}))
	if resp.ExitCode != 0 {
		t.Fatalf("read-only dispatch denied: %s", resp)
	}
	specific := field[map[string]any](t, decodeStdout(t, resp), "hookSpecificOutput")
	if specific["hookEventName"] != "PreToolUse" || specific["permissionDecision"] != "allow" {
		t.Fatalf("hookSpecificOutput: %s", resp.Stdout)
	}
	if _, leaked := specific["additionalContext"]; leaked {
		t.Fatalf("additionalContext reaches the parent, not the subagent: %s", resp.Stdout)
	}
	input := field[map[string]any](t, specific, "updatedInput")
	for key, want := range map[string]any{"run_in_background": true, "description": "review the diff", "subagent_type": "general-purpose", "model": "sonnet"} {
		if input[key] != want {
			t.Errorf("updatedInput.%s = %v, want %v unchanged", key, input[key], want)
		}
	}
	prompt := field[string](t, input, "prompt")
	if !strings.HasPrefix(prompt, agentcontext.ReadOnlyBanner) || !strings.Contains(prompt, projection) || !strings.HasSuffix(prompt, brief) {
		t.Fatalf("rewritten prompt:\n%s", prompt)
	}
	if strings.Contains(prompt, "make verify-all") || strings.Contains(prompt, "state sync") {
		t.Fatalf("rewritten prompt carries a mutating command:\n%s", prompt)
	}
	if !strings.Contains(field[string](t, specific, "permissionDecisionReason"), agentcontext.CanonicalReadOnlyFile) {
		t.Errorf("rewrite not named in permissionDecisionReason: %s", resp.Stdout)
	}
}

// TestReadOnlyDispatch_Negative_ClaudeReceiptDetectsARefusedRewrite: a client that launched the
// subagent with the original prompt (input changes refused) is reported at the dispatch
// receipt, never passed silently; the launch that carried the rewrite is allowed.
func TestReadOnlyDispatch_Negative_ClaudeReceiptDetectsARefusedRewrite(t *testing.T) {
	root, _ := readOnlyRepository(t, true)
	state := t.TempDir()
	brief := validBrief + "readonly: true\n"
	launched := map[string]any{"status": "async_launched", "agentId": "agent-refused"}
	pre := runAgentHook(t, root, state, "claude", EventPreDispatch, claudeDispatch(t, "sess-r", "tool-refused", brief, nil))
	if pre.ExitCode != 0 {
		t.Fatalf("pre-dispatch: %s", pre)
	}
	refused := runAgentHook(t, root, state, "claude", EventDispatchReceipt, nativePayload(t, "PostToolUse", "sess-r", "Agent",
		"tool-refused", map[string]any{"prompt": brief, "run_in_background": true}, launched))
	if refused.ExitCode != 2 || !strings.Contains(string(refused.Stderr), "read-only projection not delivered") {
		t.Fatalf("refused rewrite passed silently: %s", refused)
	}

	pre = runAgentHook(t, root, state, "claude", EventPreDispatch, claudeDispatch(t, "sess-r", "tool-kept", brief, nil))
	specific := field[map[string]any](t, decodeStdout(t, pre), "hookSpecificOutput")
	rewritten := field[map[string]any](t, specific, "updatedInput")
	launched["agentId"] = "agent-kept"
	kept := runAgentHook(t, root, state, "claude", EventDispatchReceipt, nativePayload(t, "PostToolUse", "sess-r", "Agent",
		"tool-kept", rewritten, launched))
	if kept.ExitCode != 0 {
		t.Fatalf("delivered rewrite reported as refused: %s", kept)
	}
}

// TestReadOnlyDispatch_Positive_ClaudeSubagentStartByRole: a read-only role receives the
// projection as SubagentStart additionalContext, which lands in the subagent's own context; any
// other role receives nothing.
func TestReadOnlyDispatch_Positive_ClaudeSubagentStartByRole(t *testing.T) {
	root, projection := readOnlyRepository(t, true)
	start := func(agentType string) Response {
		return runAgentHook(t, root, t.TempDir(), "claude", EventSubagentStart, commandPayload(t, map[string]any{
			"hook_event_name": "SubagentStart", "session_id": "sess-start", "agent_id": "agent-start", "agent_type": agentType}))
	}
	auditor := start("praetor-auditor")
	specific := field[map[string]any](t, decodeStdout(t, auditor), "hookSpecificOutput")
	if auditor.ExitCode != 0 || specific["hookEventName"] != "SubagentStart" || specific["additionalContext"] != projection {
		t.Fatalf("read-only role start: %s", auditor)
	}
	if worker := start("general-purpose"); worker.ExitCode != 0 || len(worker.Stdout) != 0 {
		t.Fatalf("read-write role start: %s", worker)
	}
}

// TestReadOnlyDispatch_Positive_AgyOverwritesThePrompt: agy's PreToolUse contract has no
// additionalContext; the projection reaches the read-only subagent through overwrite, a shallow
// merge, so the whole Subagents array comes back with every other key and subagent unchanged.
func TestReadOnlyDispatch_Positive_AgyOverwritesThePrompt(t *testing.T) {
	root, projection := readOnlyRepository(t, true)
	brief := validBrief + "readonly: true\n"
	resp := runAgentHook(t, root, t.TempDir(), "agy", EventPreDispatch, commandPayload(t, map[string]any{
		"conversationId": "sess-agy", "toolCall": map[string]any{"name": "invoke_subagent", "args": map[string]any{
			"TaskMode": "parallel",
			"Subagents": []map[string]any{
				{"Prompt": brief, "TypeName": "general", "Role": "Reviewer", "Workspace": "inherit"},
				{"Prompt": validBrief, "TypeName": "general", "Role": "Worker", "Workspace": "inherit"},
			}}}}))
	output := decodeStdout(t, resp)
	if output["decision"] != "allow" {
		t.Fatalf("agy read-only dispatch: %s", resp)
	}
	if _, ignored := output["additionalContext"]; ignored {
		t.Fatalf("agy contract has no additionalContext: %s", resp.Stdout)
	}
	overwrite := field[map[string]any](t, output, "overwrite")
	if len(overwrite) != 1 {
		t.Fatalf("overwrite must carry Subagents alone: %s", resp.Stdout)
	}
	subagents := field[[]any](t, overwrite, "Subagents")
	if len(subagents) != 2 {
		t.Fatalf("overwrite.Subagents: %s", resp.Stdout)
	}
	first, second := as[map[string]any](t, subagents[0], "Subagents[0]"), as[map[string]any](t, subagents[1], "Subagents[1]")
	prompt := field[string](t, first, "Prompt")
	if !strings.HasPrefix(prompt, agentcontext.ReadOnlyBanner) || !strings.Contains(prompt, projection) || !strings.HasSuffix(prompt, brief) {
		t.Fatalf("read-only subagent prompt:\n%s", prompt)
	}
	if first["Role"] != "Reviewer" || first["Workspace"] != "inherit" || first["TypeName"] != "general" {
		t.Errorf("read-only subagent lost a key: %v", first)
	}
	if second["Prompt"] != validBrief || second["Role"] != "Worker" {
		t.Errorf("read-write subagent changed: %v", second)
	}
	if reason := field[string](t, output, "reason"); !strings.Contains(reason, agentcontext.CanonicalReadOnlyFile) {
		t.Errorf("rewrite not named in reason: %s", resp.Stdout)
	}
}

// TestReadOnlyDispatch_Boundary_NoChannelNamesTheSubstitution: Codex and Gemini expose no
// channel that reaches the subagent, so a read-only dispatch is allowed with the substitution
// named on the hook's output, never silently and never with the full context injected.
func TestReadOnlyDispatch_Boundary_NoChannelNamesTheSubstitution(t *testing.T) {
	root, _ := readOnlyRepository(t, true)
	brief := validBrief + "readonly: true\n"
	for client, payload := range map[string][]byte{
		"codex": nativePayload(t, "PreToolUse", "sess-codex", "spawn_agent", "tool-codex", map[string]any{"message": brief}, nil),
		"gemini": commandPayload(t, map[string]any{"hook_event_name": "BeforeTool", "session_id": "sess-gemini",
			"tool_name": "invoke_agent", "tool_input": map[string]any{"prompt": brief}}),
	} {
		resp := runAgentHook(t, root, t.TempDir(), client, EventPreDispatch, payload)
		if resp.ExitCode != 0 || len(resp.Stdout) != 0 {
			t.Fatalf("%s read-only dispatch: %s", client, resp)
		}
		if !strings.Contains(string(resp.Stderr), "read-only projection not delivered: "+client+" has no channel") {
			t.Fatalf("%s substitution not named: %s", client, resp)
		}
	}
}

// TestReadOnlyDispatch_Negative_MissingProjectionFailsClosed: major 6. Without the compiled
// AGENTS.readonly.md a read-only dispatch is denied with the fix named, never given the full
// context; a read-only role at SubagentStart, which cannot block, gets the banner and the
// substitution named instead of the projection.
func TestReadOnlyDispatch_Negative_MissingProjectionFailsClosed(t *testing.T) {
	root, _ := readOnlyRepository(t, false)
	brief := validBrief + "readonly: true\n"
	claude := runAgentHook(t, root, t.TempDir(), "claude", EventPreDispatch, claudeDispatch(t, "sess-m", "tool-m", brief, nil))
	if claude.ExitCode != 2 || !strings.Contains(string(claude.Stderr), agentcontext.CanonicalReadOnlyFile) {
		t.Fatalf("claude dispatch without projection: %s", claude)
	}
	role := runAgentHook(t, root, t.TempDir(), "claude", EventPreDispatch,
		claudeDispatch(t, "sess-m", "tool-role", validBrief, map[string]any{"subagent_type": "praetor-auditor"}))
	if role.ExitCode != 2 || strings.Contains(string(role.Stdout), "Keep ledger private") {
		t.Fatalf("read-only role dispatch without projection: %s", role)
	}
	agy := runAgentHook(t, root, t.TempDir(), "agy", EventPreDispatch, commandPayload(t, map[string]any{
		"conversationId": "sess-agy", "toolCall": map[string]any{"name": "invoke_subagent", "args": map[string]any{
			"Subagents": []map[string]any{{"Prompt": brief}}}}}))
	if output := decodeStdout(t, agy); output["decision"] != "deny" || !strings.Contains(field[string](t, output, "reason"), agentcontext.CanonicalReadOnlyFile) {
		t.Fatalf("agy dispatch without projection: %s", agy)
	}
	start := runAgentHook(t, root, t.TempDir(), "claude", EventSubagentStart, commandPayload(t, map[string]any{
		"hook_event_name": "SubagentStart", "session_id": "sess-m", "agent_id": "agent-m", "agent_type": "praetor-auditor"}))
	added := field[string](t, field[map[string]any](t, decodeStdout(t, start), "hookSpecificOutput"), "additionalContext")
	if start.ExitCode != 0 || !strings.HasPrefix(added, agentcontext.ReadOnlyBanner) ||
		!strings.Contains(added, "not delivered") || strings.Contains(added, "Keep ledger private") {
		t.Fatalf("subagent start without projection: %s", start)
	}
}

// TestReadOnlyDispatch_Boundary_ReadWriteBriefIsUnchanged: an unmarked brief and an explicit
// readonly: false keep the plain allow; an invalid readonly value is denied.
func TestReadOnlyDispatch_Boundary_ReadWriteBriefIsUnchanged(t *testing.T) {
	root, _ := readOnlyRepository(t, true)
	for name, brief := range map[string]string{"absent": validBrief, "false": validBrief + "readonly: false\n"} {
		resp := runAgentHook(t, root, t.TempDir(), "claude", EventPreDispatch, claudeDispatch(t, "sess-"+name, "tool-"+name, brief, nil))
		if resp.ExitCode != 0 || len(resp.Stdout) != 0 || len(resp.Stderr) != 0 {
			t.Fatalf("%s read-write dispatch changed: %s", name, resp)
		}
	}
	bad := runAgentHook(t, root, t.TempDir(), "claude", EventPreDispatch, claudeDispatch(t, "sess-bad", "tool-bad", validBrief+"readonly: maybe\n", nil))
	if bad.ExitCode != 2 || !strings.Contains(string(bad.Stderr), "invalid boolean value") {
		t.Fatalf("invalid readonly value: %s", bad)
	}
}

// TestReadOnlyDispatch_Boundary_SubagentStartContextCap: Claude Code moves an additionalContext
// over 10,000 characters to a file the agent is not asked to read; a projection over the cap is
// replaced by the banner and the file to read, named, and one at the cap is delivered whole.
func TestReadOnlyDispatch_Boundary_SubagentStartContextCap(t *testing.T) {
	for name, size := range map[string]int{"at cap": claudeContextCap, "over cap": claudeContextCap + 1} {
		root := repository(t, true)
		projection := "# P\n\n" + agentcontext.ReadOnlyBanner + "\n"
		projection += strings.Repeat("x", size-len(projection))
		if err := os.WriteFile(filepath.Join(root, agentcontext.CanonicalReadOnlyFile), []byte(projection), 0o600); err != nil {
			t.Fatal(err)
		}
		resp := runAgentHook(t, root, t.TempDir(), "claude", EventSubagentStart, commandPayload(t, map[string]any{
			"hook_event_name": "SubagentStart", "session_id": "sess-cap", "agent_id": "agent-cap", "agent_type": "reviewer"}))
		added := field[string](t, field[map[string]any](t, decodeStdout(t, resp), "hookSpecificOutput"), "additionalContext")
		switch name {
		case "at cap":
			if added != projection {
				t.Fatalf("%s: projection not delivered whole (%d chars)", name, len(added))
			}
		default:
			if len(added) > claudeContextCap || !strings.HasPrefix(added, agentcontext.ReadOnlyBanner) ||
				!strings.Contains(added, filepath.Join(root, agentcontext.CanonicalReadOnlyFile)) {
				t.Fatalf("%s: %q", name, added)
			}
		}
	}
}

// TestReadOnlyDispatch_Negative_DeliveryOutsideClaudeFailsClosed: the generic encoder renders a
// context delivery in Claude Code's shape only; any other client's dialect denies it.
func TestReadOnlyDispatch_Negative_DeliveryOutsideClaudeFailsClosed(t *testing.T) {
	for _, client := range []string{"codex", "gemini", "lefthook"} {
		dialect, _ := DialectFor(client)
		resp := dialect.Encode(Canonical{Event: EventPreDispatch}, Verdict{Outcome: Allow, UpdatedInput: json.RawMessage(`{}`)})
		if resp.ExitCode == 0 || len(resp.Stdout) != 0 {
			t.Errorf("%s encoded a delivery it has no shape for: %s", client, resp)
		}
	}
	claude, _ := DialectFor("claude")
	if resp := claude.Encode(Canonical{Event: EventPreDispatch}, Verdict{Outcome: Allow, Notice: "named"}); resp.ExitCode != 0 ||
		string(resp.Stderr) != "praetor hook: named\n" || len(resp.Stdout) != 0 {
		t.Errorf("notice-only allow: %s", resp)
	}
}
