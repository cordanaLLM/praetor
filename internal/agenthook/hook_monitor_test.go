package agenthook

import (
	"strings"
	"testing"
)

// Claude Code 2.1.314 runs hooks that match Bash on the shell command a Monitor call starts
// (lusoris/praetor#946). The command reaches the hook as tool_input.command of a Monitor
// payload, so the command policy judges it as it judges the same command from Bash. The
// payloads are only fed to the hook; nothing here runs them.

// Negative (engine-hook-monitor-command-guard-20261030): a hook-evasion push started by
// Monitor is refused like the same push started by Bash.
func TestRunClaudeMonitorCommandIsGuarded(t *testing.T) {
	root := repository(t, true)
	for _, tool := range []string{"Bash", "Monitor"} {
		payload := []byte(`{"hook_event_name":"PreToolUse","tool_name":"` + tool + `",` +
			`"tool_input":{"command":"git push --no-verify origin main","description":"push","timeout_ms":60000}}`)
		deny := serve(t, "claude", "pre-tool", root, payload)
		if deny.ExitCode != 2 || !strings.HasPrefix(string(deny.Stderr), "[BLOCKED BY HISS] ") {
			t.Errorf("%s hook-evasion push: want a deny, got %+v", tool, deny)
		}
	}
}

// Positive: a harmless Monitor command passes, and so does a Monitor that opens a WebSocket,
// which starts no shell command at all.
func TestRunClaudeMonitorHarmlessPasses(t *testing.T) {
	root := repository(t, true)
	for name, input := range map[string]string{
		"command":   `{"command":"tail -f build.log | grep --line-buffered ERROR","description":"errors","timeout_ms":60000}`,
		"websocket": `{"ws":{"url":"wss://events.example.test/stream"},"description":"events","timeout_ms":60000}`,
	} {
		payload := []byte(`{"hook_event_name":"PreToolUse","tool_name":"Monitor","tool_input":` + input + `}`)
		if allow := serve(t, "claude", "pre-tool", root, payload); allow.ExitCode != 0 || len(allow.Stderr) != 0 {
			t.Errorf("Monitor %s: want an allow, got %+v", name, allow)
		}
	}
}

// Boundary: a Monitor command that is not text is refused as invalid input instead of being
// read as "no command"; a Monitor without tool_input is invalid too.
func TestRunClaudeMonitorMalformedCommandIsRefused(t *testing.T) {
	root := repository(t, true)
	for name, payload := range map[string]string{
		"number":     `{"tool_name":"Monitor","tool_input":{"command":7,"description":"x","timeout_ms":1000}}`,
		"no input":   `{"tool_name":"Monitor"}`,
		"input text": `{"tool_name":"Monitor","tool_input":"git push --no-verify"}`,
	} {
		response := serve(t, "claude", "pre-tool", root, []byte(payload))
		if response.ExitCode != 2 || !strings.Contains(string(response.Stderr), "Invalid hook input") {
			t.Errorf("Monitor %s: want invalid input, got %+v", name, response)
		}
	}
}
