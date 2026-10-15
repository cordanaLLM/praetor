// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// handedOff runs one claude pre-tool call from root through Run with handoff.
func handedOff(t *testing.T, root string, payload []byte, handoff EngineHandoff) Response {
	t.Helper()
	return Run(context.Background(), Invocation{
		Client: "claude", Event: "pre-tool", Stdin: bytes.NewReader(payload),
		Getenv: noEnvironment, WorkDir: root, Policy: policy(t), Handoff: handoff,
	})
}

// #1173. Positive: Run hands the handoff the workspace root and the payload it read, and returns
// a handled answer as it is. Negative: a refusal is a deny in the client's dialect. Boundary: a
// note is printed ahead of this engine's own verdict, which still reads the same payload.
func TestRun_Handoff_3D(t *testing.T) {
	root := repository(t, true)
	payload := commandPayload(t, map[string]any{"hook_event_name": "PreToolUse", "tool_input": map[string]any{"command": "git status"}})
	var gotRoot string
	var gotPayload []byte
	handled := handedOff(t, root, payload, func(_ context.Context, root string, row Registration, data []byte) HandoffResult {
		gotRoot, gotPayload = root, data
		return HandoffResult{Handled: true, Response: Response{Stderr: []byte("pinned"), ExitCode: 0}}
	})
	if string(handled.Stderr) != "pinned" || !bytes.Equal(gotPayload, payload) {
		t.Fatalf("handled = %s, payload %q", handled, gotPayload)
	}
	if resolved, _ := filepath.EvalSymlinks(root); !strings.EqualFold(filepath.Clean(gotRoot), filepath.Clean(resolved)) &&
		!strings.EqualFold(filepath.Clean(gotRoot), filepath.Clean(root)) {
		t.Fatalf("handoff root %q, want %q", gotRoot, root)
	}
	denied := handedOff(t, root, payload, func(context.Context, string, Registration, []byte) HandoffResult {
		return HandoffResult{Deny: "PRAETOR_REF='main' is no usable pin"}
	})
	if denied.ExitCode != 2 || !strings.Contains(string(denied.Stderr), "PRAETOR_REF='main' is no usable pin") {
		t.Fatalf("denied = %s", denied)
	}
	noted := handedOff(t, root, payload, func(context.Context, string, Registration, []byte) HandoffResult {
		return HandoffResult{Note: "praetor hook: judged by this engine"}
	})
	if noted.ExitCode != 0 || !strings.HasPrefix(string(noted.Stderr), "praetor hook: judged by this engine\n") {
		t.Fatalf("noted = %s; want this engine's allow after the note", noted)
	}
}
