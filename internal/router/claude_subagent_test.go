package router

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestClaudeSubagentRoutes(t *testing.T) {
	for _, model := range []string{"haiku", "sonnet", "opus", "fable"} {
		for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max"} {
			t.Run(model+"/"+effort, func(t *testing.T) {
				body := routingFixture + "claude_subagent:\n  implement: {model: " + model + ", effort: '" + effort + "'}\n"
				if _, err := LoadRoutingConfig(routingInput(t, body)); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestClaudeSubagentInvalidRoutesNameKey(t *testing.T) {
	for _, entry := range []string{
		"unknown: {model: sonnet}", "implement: {model: other}",
		"implement: {model: sonnet, effort: extreme}", "implement: {effort: high}",
	} {
		t.Run(entry, func(t *testing.T) {
			_, err := LoadRoutingConfig(routingInput(t, routingFixture+"claude_subagent:\n  "+entry+"\n"))
			key := strings.SplitN(entry, ":", 2)[0]
			if !errors.Is(err, ErrInvalidRoutingConfig) || !strings.Contains(err.Error(), key) {
				t.Fatalf("key %s not named: %v", key, err)
			}
		})
	}
}

func TestClaudeSubagentRouteBounds(t *testing.T) {
	labels := make([]string, MaxClaudeSubagentRoutes)
	entries := make([]string, MaxClaudeSubagentRoutes)
	for i := 0; i < MaxClaudeSubagentRoutes; i++ {
		labels[i] = fmt.Sprintf("task_%d", i)
		entries[i] = "  " + labels[i] + ": {model: sonnet}\n"
	}
	body := strings.Replace(routingFixture, "target_tasks: [implement]", "target_tasks: ["+strings.Join(labels, ",")+"]", 1)
	body += "claude_subagent:\n" + strings.Join(entries, "")
	if _, err := LoadRoutingConfig(routingInput(t, body)); err != nil {
		t.Fatalf("at cap: %v", err)
	}
	if _, err := LoadRoutingConfig(routingInput(t, body+"  overflow: {model: sonnet}\n")); !errors.Is(err, ErrInvalidRoutingConfig) || !strings.Contains(err.Error(), "claude_subagent") {
		t.Fatalf("over cap: %v", err)
	}
}
