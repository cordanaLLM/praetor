// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeHookFile writes one client hook file below root.
func writeHookFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hookDocument is a hook file registering command under event with matcher.
func hookDocument(event, matcher, command string) string {
	return `{"hooks": {"` + event + `": [{"matcher": "` + matcher + `", "hooks": [{"type": "command", "command": ` +
		`"` + command + `"}]}]}}`
}

// gateRegistered reports DispatchGateRegistered for root, failing the test on an error.
func gateRegistered(t *testing.T, root string) bool {
	t.Helper()
	gated, err := DispatchGateRegistered(t.Context(), root)
	if err != nil {
		t.Fatalf("DispatchGateRegistered: %v", err)
	}
	return gated
}

// Positive: the engine call in Claude Code's settings and the skew guard in Codex's hooks file
// each register the pre-dispatch row.
func TestDispatchGateRegistered_Positive(t *testing.T) {
	claude := t.TempDir()
	writeHookFile(t, claude, ".claude/settings.json", hookDocument("PreToolUse", "^Agent$", "praetorctl hook claude pre-dispatch"))
	if !gateRegistered(t, claude) {
		t.Error("engine call in .claude/settings.json not recognised")
	}
	codex := t.TempDir()
	writeHookFile(t, codex, ".codex/hooks.json", hookDocument("PreToolUse", "^spawn_agent$",
		`python3 -B \"$(git rev-parse --show-toplevel)/.config/agent/hooks/praetor_hook.py\" codex pre-dispatch`))
	if !gateRegistered(t, codex) {
		t.Error("skew guard in .codex/hooks.json not recognised")
	}
}

// Negative: no hook file, only the pre-tool row (what adoption registers), the pre-dispatch
// command under a matcher that never sees the dispatch tool, and a missing or cancelled context.
func TestDispatchGateRegistered_Negative(t *testing.T) {
	if gateRegistered(t, t.TempDir()) {
		t.Error("repository without hook files reported a dispatch gate")
	}
	preTool := t.TempDir()
	writeHookFile(t, preTool, ".claude/settings.json", hookDocument("PreToolUse", "^Bash$", "praetorctl hook claude pre-tool"))
	if gateRegistered(t, preTool) {
		t.Error("pre-tool row alone reported as a dispatch gate")
	}
	narrow := t.TempDir()
	writeHookFile(t, narrow, ".claude/settings.json", hookDocument("PreToolUse", "^Bash$", "praetorctl hook claude pre-dispatch"))
	if gateRegistered(t, narrow) {
		t.Error("pre-dispatch under the Bash matcher reported as a dispatch gate")
	}
	var nilContext context.Context
	if _, err := DispatchGateRegistered(nilContext, preTool); err == nil {
		t.Error("nil context must be an error")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := DispatchGateRegistered(cancelled, preTool); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: %v", err)
	}
}

// Boundary: a match-all matcher covers the dispatch tool, and so does Claude Code's literal
// Agent, which it reads as the exact tool name ^Agent$ selects; a file that does not parse, and
// a Gemini settings file with comments the strict parser refuses, prove no registration and
// are not an error.
func TestDispatchGateRegistered_Boundary(t *testing.T) {
	wildcard := t.TempDir()
	writeHookFile(t, wildcard, ".gemini/settings.json", hookDocument("BeforeTool", "*", "praetorctl hook gemini pre-dispatch"))
	if !gateRegistered(t, wildcard) {
		t.Error("match-all matcher not recognised")
	}
	literal := t.TempDir()
	writeHookFile(t, literal, ".claude/settings.json", hookDocument("PreToolUse", "Agent", "praetorctl hook claude pre-dispatch"))
	if !gateRegistered(t, literal) {
		t.Error("literal Agent matcher not recognised as the ^Agent$ row")
	}
	broken := t.TempDir()
	writeHookFile(t, broken, ".claude/settings.json", `{"hooks": [`)
	if gateRegistered(t, broken) {
		t.Error("unparseable settings reported a dispatch gate")
	}
	commented := t.TempDir()
	writeHookFile(t, commented, ".gemini/settings.json", "// comment\n"+hookDocument("BeforeTool", "^invoke_agent$", "praetorctl hook gemini pre-dispatch"))
	if gateRegistered(t, commented) {
		t.Error("JSONC settings the strict parser refuses reported a dispatch gate")
	}
}

// The clients whose repository file can carry the pre-dispatch row are exactly the native
// hook-file clients with such a row.
func TestDispatchGateClients(t *testing.T) {
	got := dispatchGateClients()
	if len(got) != 3 || got[0] != "claude" || got[1] != "codex" || got[2] != "gemini" {
		t.Errorf("dispatchGateClients = %q", got)
	}
}

// TestNativeHooks: the handlers adoption merges and DispatchGateRegistered looks up come from
// the registration table. Positive: Claude Code's pre-tool row becomes its PreToolUse handler
// with the engine call. Negative: a pair without a row (Codex pre-edit) and an unknown client
// yield none. Boundary: the timeout is stated in the hook file's unit, seconds for Claude Code
// and milliseconds for Gemini CLI, one handler per row, and each handler carries its file's
// matcher reading (exact literal for Claude Code, not for Gemini CLI).
func TestNativeHooks(t *testing.T) {
	claudeFile, _ := NativeHookFile("claude")
	hooks := NativeHooks("claude", claudeFile, EventPreTool)
	if len(hooks) != 1 || hooks[0].Event != "PreToolUse" || hooks[0].Command != "praetorctl hook claude pre-tool" || hooks[0].Timeout != 15 ||
		!hooks[0].ExactLiteral {
		t.Fatalf("claude pre-tool handlers = %+v", hooks)
	}
	codexFile, _ := NativeHookFile("codex")
	if hooks := NativeHooks("codex", codexFile, EventPreEdit); len(hooks) != 0 {
		t.Errorf("codex pre-edit has no row but yields %+v", hooks)
	}
	if hooks := NativeHooks("cl4ude", claudeFile, EventPreTool); len(hooks) != 0 {
		t.Errorf("unknown client yields %+v", hooks)
	}
	geminiFile, _ := NativeHookFile("gemini")
	for _, event := range []Event{EventPreTool, EventStop} {
		rows := 0
		for _, row := range Registrations("gemini") {
			if row.Event == event {
				rows++
			}
		}
		hooks := NativeHooks("gemini", geminiFile, event)
		if len(hooks) != rows || rows == 0 {
			t.Fatalf("gemini %s: %d handlers for %d rows", event, len(hooks), rows)
		}
		for _, hook := range hooks {
			if hook.Timeout < 1000 || hook.Timeout%1000 != 0 {
				t.Errorf("gemini %s timeout %d is not in milliseconds", event, hook.Timeout)
			}
			if hook.ExactLiteral {
				t.Errorf("gemini %s handler reads a literal matcher as exact", event)
			}
		}
	}
}
