package agenthook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/router"
)

func claudeRouteRepository(t *testing.T) string {
	t.Helper()
	root, _ := readOnlyRepository(t, true)
	body := strings.Replace(briefRoutingTemplate, "%s", "feature_implementation, unit_test_suites", 1)
	writeBriefFixture(t, root, router.DefaultConfigPath, body+"claude_subagent:\n  feature_implementation: {model: haiku, effort: low}\n")
	return root
}

func TestClaudeSubagentRouting(t *testing.T) {
	for _, readonly := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "readonly"}[readonly], func(t *testing.T) {
			checkClaudeSubagentRouting(t, readonly)
		})
	}
}

func checkClaudeSubagentRouting(t *testing.T, readonly bool) {
	t.Helper()
	root, state := claudeRouteRepository(t), t.TempDir()
	brief := validBrief
	if readonly {
		brief += "readonly: true\n"
	}
	resp := runAgentHook(t, root, state, "claude", EventPreDispatch, claudeDispatch(t, "route-session", "route-tool", brief, nil))
	if resp.ExitCode != 0 {
		t.Fatalf("dispatch denied: %s", resp)
	}
	specific := field[map[string]any](t, decodeStdout(t, resp), "hookSpecificOutput")
	assertClaudeRoutedInput(t, specific, brief, readonly)
	store, err := newCorrelationStore(t.Context(), root, state)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.promote(context.Background(), "claude", "route-session", "route-tool", "agent")
	if err != nil {
		t.Fatal(err)
	}
	assertClaudeRoute(t, entry)
}

func assertClaudeRoutedInput(t *testing.T, specific map[string]any, brief string, readonly bool) {
	t.Helper()
	input := field[map[string]any](t, specific, "updatedInput")
	if input["model"] != "haiku" || input["effort"] != "low" || input["run_in_background"] != true || input["description"] != "review the diff" {
		t.Fatalf("input: %v", input)
	}
	prompt := field[string](t, input, "prompt")
	if !strings.HasSuffix(prompt, brief) || (readonly && prompt == brief) {
		t.Fatalf("prompt rewrite lost: %s", prompt)
	}
	if !strings.Contains(field[string](t, specific, "permissionDecisionReason"), "routed feature_implementation -> haiku/low") {
		t.Fatal("route notice absent")
	}
}

func TestClaudeSubagentExplicitAndUnlisted(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
		brief string
	}{
		{"model", map[string]any{"model": "opus"}, validBrief},
		{"effort", map[string]any{"effort": "high"}, validBrief},
		{"empty_model", map[string]any{"model": ""}, validBrief},
		{"null_effort", map[string]any{"effort": nil}, validBrief},
		{"unlisted", nil, strings.Replace(validBrief, "feature_implementation", "unit_test_suites", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := runAgentHook(t, claudeRouteRepository(t), t.TempDir(), "claude", EventPreDispatch, claudeDispatch(t, "s", "t", tc.brief, tc.extra))
			if resp.ExitCode != 0 || len(resp.Stdout) != 0 {
				t.Fatalf("input changed: %s", resp)
			}
		})
	}
}

func assertClaudeRoute(t *testing.T, entry correlationEntry) {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
	route := field[map[string]any](t, row, "claude_subagent")
	if route["model"] != "haiku" || route["effort"] != "low" {
		t.Fatalf("correlation route: %s", data)
	}
}

func TestClaudeSubagentOtherClientsUnchanged(t *testing.T) {
	for _, client := range []string{"codex", "gemini", "agy"} {
		t.Run(client, func(t *testing.T) {
			fixture := loadAgentTextFixtures(t, client)[0]
			resp := runAgentHook(t, claudeRouteRepository(t), t.TempDir(), client, EventPreDispatch, fixture.Payload)
			if resp.ExitCode != 0 || strings.Contains(string(resp.Stdout), "updatedInput") || strings.Contains(string(resp.Stdout), "haiku") || strings.Contains(string(resp.Stderr), "routed") {
				t.Fatalf("other client changed: %s", resp)
			}
		})
	}
}

func TestClaudeSubagentOptionalEffort(t *testing.T) {
	root := claudeRouteRepository(t)
	body := strings.Replace(briefRoutingTemplate, "%s", "feature_implementation", 1)
	writeBriefFixture(t, root, router.DefaultConfigPath, body+"claude_subagent:\n  feature_implementation: {model: sonnet}\n")
	resp := runAgentHook(t, root, t.TempDir(), "claude", EventPreDispatch, claudeDispatch(t, "s", "t", validBrief, nil))
	if resp.ExitCode != 0 {
		t.Fatal(resp)
	}
	specific := field[map[string]any](t, decodeStdout(t, resp), "hookSpecificOutput")
	input := field[map[string]any](t, specific, "updatedInput")
	if _, present := input["effort"]; present || input["model"] != "sonnet" {
		t.Fatalf("optional effort: %v", input)
	}
}
