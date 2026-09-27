package agenthook

import (
	"testing"
	"time"
)

func rowOf(t *testing.T, client string, event Event) Registration {
	t.Helper()
	row, err := ParseArguments(client, string(event))
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// Positive: the engine call under any path or executable suffix, the skew guard however it is
// quoted, and the Python pre-tool adapters all serve the pre-tool row.
func TestServedBy_Positive_EngineCallSkewGuardAndAdapters(t *testing.T) {
	claude := rowOf(t, "claude", EventPreTool)
	for _, line := range []string{
		"praetorctl hook claude pre-tool",
		"/usr/local/bin/praetorctl hook claude pre-tool",
		`C:\tools\praetorctl.exe hook claude pre-tool`,
		"standardsctl hook claude pre-tool",
		`python3 -B "${CLAUDE_PROJECT_DIR}/.config/agent/hooks/praetor_hook.py" claude pre-tool`,
		"python3 -B ${CLAUDE_PROJECT_DIR}/.config/agent/hooks/command_guard.py",
		`python3 -B "$(git rev-parse --show-toplevel)/.config/agent/hooks/codex_pre_tool.py"`,
		"python3 .config/agent/hooks/block_evasion.py",
	} {
		if !claude.ServedBy(line) {
			t.Errorf("%q does not serve %+v", line, claude)
		}
	}
	if gemini := rowOf(t, "gemini", EventPreEdit); !gemini.ServedBy("python3 -B .config/agent/hooks/praetor_hook.py gemini pre-edit") {
		t.Error("skew guard call does not serve the gemini pre-edit row")
	}
}

// Negative: another client, another event, another subcommand, a name that only ends like an
// adapter, and a pre-tool adapter under a row that is not pre-tool serve nothing.
func TestServedBy_Negative_OtherRowsAndLookalikes(t *testing.T) {
	claude := rowOf(t, "claude", EventPreTool)
	for _, line := range []string{
		"praetorctl hook codex pre-tool",
		"praetorctl hook claude pre-edit",
		"praetorctl audit claude pre-tool",
		"echo hook claude pre-tool",
		"python3 my_command_guard.py",
		"python3 praetor_hook.py gemini pre-tool",
	} {
		if claude.ServedBy(line) {
			t.Errorf("%q serves %+v", line, claude)
		}
	}
	if edit := rowOf(t, "claude", EventPreEdit); edit.ServedBy("python3 -B .config/agent/hooks/command_guard.py") {
		t.Error("the pre-tool adapter serves the pre-edit row")
	}
}

// Boundary: an empty line, a call cut after the client, and an engine call as the last token
// serve nothing; the arguments after the event do not matter.
func TestServedBy_Boundary_TruncatedAndTrailingArguments(t *testing.T) {
	claude := rowOf(t, "claude", EventPreTool)
	for _, line := range []string{"", "   ", "praetorctl hook claude", "praetorctl hook", "praetorctl"} {
		if claude.ServedBy(line) {
			t.Errorf("%q serves %+v", line, claude)
		}
	}
	if !claude.ServedBy("praetorctl hook claude pre-tool --extra") {
		t.Error("trailing arguments hide the engine call")
	}
}

// Positive, negative and boundary: the native clients have one hook file each, with the timeout
// unit of their tracked registrations; AGY, context-only clients and an empty id have none.
func TestNativeHookFile(t *testing.T) {
	for client, want := range map[string]HookFile{
		"claude": {Path: ".claude/settings.json", TimeoutUnit: time.Second},
		"codex":  {Path: ".codex/hooks.json", TimeoutUnit: time.Second},
		"gemini": {Path: ".gemini/settings.json", TimeoutUnit: time.Millisecond},
	} {
		if got, ok := NativeHookFile(client); !ok || got != want {
			t.Errorf("%s: %+v, %v; want %+v", client, got, ok, want)
		}
	}
	for _, client := range []string{"agy", "cursor", "lefthook", ""} {
		if got, ok := NativeHookFile(client); ok {
			t.Errorf("%q has a hook file: %+v", client, got)
		}
	}
}
