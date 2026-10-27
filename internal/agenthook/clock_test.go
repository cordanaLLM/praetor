package agenthook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestClockContextUsesInjectedLocalTime(t *testing.T) {
	prior := time.Local
	time.Local = time.FixedZone("CET", 3600)
	t.Cleanup(func() { time.Local = prior })
	now := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	want := "clock: 2026-01-02 04:04:05 CET (from the system clock; use this, never estimate)"
	if got := clockContext(now); got != want {
		t.Fatalf("clock line = %q, want %q", got, want)
	}
}

func TestRunPromptClockAdapters(t *testing.T) {
	tests := []struct {
		client, native string
		payload        string
		wantShape      func(map[string]any) string
	}{
		{"claude", "UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit"}`, standardClockOutput},
		{"codex", "UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit"}`, standardClockOutput},
		{"gemini", "BeforeAgent", `{"hook_event_name":"BeforeAgent"}`, standardClockOutput},
		{"agy", "PreInvocation", `{}`, agyClockOutput},
		{"opencode", "messages.transform", `{}`, openCodeClockOutput},
	}
	for _, tc := range tests {
		t.Run(tc.client, func(t *testing.T) {
			resp := runClock(t, tc.client, "prompt", tc.payload)
			if resp.ExitCode != 0 {
				t.Fatalf("clock hook failed: %+v", resp)
			}
			var output map[string]any
			if err := json.Unmarshal(resp.Stdout, &output); err != nil {
				t.Fatal(err)
			}
			if got := tc.wantShape(output); got != wantClockLine() {
				t.Fatalf("%s clock shape %q, want %q (native event %s)", tc.client, got, wantClockLine(), tc.native)
			}
		})
	}
}

func TestRunPostToolClockAdapters(t *testing.T) {
	for _, client := range []string{"claude", "codex", "gemini", "opencode"} {
		t.Run(client, func(t *testing.T) {
			native := `{"hook_event_name":"PostToolUse"}`
			if client == "gemini" {
				native = `{"hook_event_name":"AfterTool"}`
			}
			if client == "opencode" {
				native = `{}`
			}
			resp := runClock(t, client, "post-tool", native)
			if resp.ExitCode != 0 {
				t.Fatalf("clock hook failed: %+v", resp)
			}
			var output map[string]any
			if err := json.Unmarshal(resp.Stdout, &output); err != nil {
				t.Fatal(err)
			}
			var got string
			if client == "opencode" {
				got = openCodeClockOutput(output)
			} else {
				got = standardClockOutput(output)
			}
			if got != wantClockLine() {
				t.Fatalf("%s post-tool clock %q, want %q", client, got, wantClockLine())
			}
		})
	}
}

func TestRunClockNegativeMalformedAndUnsupportedPairs(t *testing.T) {
	for _, tc := range []struct{ client, payload string }{
		{"claude", `{"hook_event_name":"Stop"}`},
		{"codex", `{"hook_event_name":"Stop"}`},
		{"gemini", `{"hook_event_name":"AfterAgent"}`},
		{"agy", `not-json`},
		{"opencode", `not-json`},
	} {
		response := Run(context.Background(), Invocation{Client: tc.client, Event: "prompt", Stdin: strings.NewReader(tc.payload)})
		if tc.client == "agy" {
			var output map[string]any
			if err := json.Unmarshal(response.Stdout, &output); err != nil || agyClockOutput(output) == "" || !strings.Contains(agyClockOutput(output), "Invalid hook input") {
				t.Errorf("agy invalid prompt was not surfaced: %+v", response)
			}
			continue
		}
		if response.ExitCode != 2 || len(response.Stdout) != 0 || !strings.Contains(string(response.Stderr), "Invalid hook input") {
			t.Errorf("%s invalid prompt payload accepted: %+v", tc.client, response)
		}
	}
	if row, err := ParseArguments("agy", "post-tool"); err == nil || row != (Registration{}) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported agy post-tool pair: %+v %v", row, err)
	}
}

func runClock(t *testing.T, client, event, payload string) Response {
	t.Helper()
	prior := time.Local
	time.Local = time.FixedZone("CET", 3600)
	t.Cleanup(func() { time.Local = prior })
	now := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	// An absolute directory on every platform: "/tmp" is no absolute path on Windows, and the
	// post-tool deny for it stayed hidden only while a deny carried the clock as context (#1386).
	return Run(context.Background(), Invocation{Client: client, Event: event, Stdin: strings.NewReader(payload), WorkDir: t.TempDir(), Now: now})
}

func wantClockLine() string {
	return "clock: 2026-01-02 04:04:05 CET (from the system clock; use this, never estimate)"
}

func standardClockOutput(output map[string]any) string {
	contextOutput, ok := output["hookSpecificOutput"].(map[string]any)
	if !ok {
		return ""
	}
	line, ok := contextOutput["additionalContext"].(string)
	if !ok {
		return ""
	}
	return line
}

func agyClockOutput(output map[string]any) string {
	steps, ok := output["injectSteps"].([]any)
	if !ok {
		return ""
	}
	if len(steps) != 1 {
		return ""
	}
	step, ok := steps[0].(map[string]any)
	if !ok {
		return ""
	}
	line, ok := step["ephemeralMessage"].(string)
	if !ok {
		return ""
	}
	return line
}

func openCodeClockOutput(output map[string]any) string {
	line, ok := output["additionalContext"].(string)
	if !ok {
		return ""
	}
	return line
}
