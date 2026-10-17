package clientsetup

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestBuildProjectPlanAllowsOnlyPraetorctlPathCommand(t *testing.T) {
	registry := Registry{Version: 1, Servers: []Server{{Name: "asdecided", Command: "praetorctl", Args: []string{"decisions", "serve"}}}}
	for _, client := range []Client{Claude, Gemini, OpenCodeV1, Continue} {
		plan, err := BuildProjectPlan(t.Context(), registry, client, nil)
		if err != nil {
			t.Fatalf("%s: %v", client, err)
		}
		if !strings.Contains(string(plan.Content), "praetorctl") {
			t.Fatalf("%s config omits PATH command: %s", client, plan.Content)
		}
	}
	registry.Servers[0].Command = "some-other-command"
	if _, err := BuildProjectPlan(t.Context(), registry, Claude, nil); err == nil {
		t.Fatal("unallow-listed PATH command accepted")
	}
	registry.Servers[0].Command = hostAbsolute("opt", "praetorctl")
	if _, err := BuildProjectPlan(t.Context(), registry, Claude, nil); err == nil {
		t.Fatal("absolute command accepted through project-only validator")
	}
	registry.Servers[0].Command = "praetorctl"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := BuildProjectPlan(ctx, registry, Claude, nil); err == nil {
		t.Fatal("cancelled project plan succeeded")
	}
	registry = Registry{Version: 1}
	for i := 0; i < MaxServers; i++ {
		registry.Servers = append(registry.Servers, Server{Name: fmt.Sprintf("server-%02d", i), Command: "praetorctl"})
	}
	if _, err := BuildProjectPlan(t.Context(), registry, Claude, nil); err != nil {
		t.Fatalf("project server boundary rejected: %v", err)
	}
	registry.Servers = append(registry.Servers, Server{Name: "overflow", Command: "praetorctl"})
	if _, err := BuildProjectPlan(t.Context(), registry, Claude, nil); err == nil {
		t.Fatal("project registry beyond server bound accepted")
	}
}
