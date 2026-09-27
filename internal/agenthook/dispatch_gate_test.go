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

// Boundary: a match-all matcher covers the dispatch tool; a file that does not parse, and a
// Gemini settings file with comments the strict parser refuses, prove no registration and are
// not an error.
func TestDispatchGateRegistered_Boundary(t *testing.T) {
	wildcard := t.TempDir()
	writeHookFile(t, wildcard, ".gemini/settings.json", hookDocument("BeforeTool", "*", "praetorctl hook gemini pre-dispatch"))
	if !gateRegistered(t, wildcard) {
		t.Error("match-all matcher not recognised")
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
