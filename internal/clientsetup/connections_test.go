package clientsetup

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"strings"
	"testing"
)

func testConnectionProfile() ConnectionProfile {
	return ConnectionProfile{Version: 1, Gateway: GatewayConnection{
		BridgeCommand: "/opt/agent/mcp-bridge", TokenFile: "/private/agent-token",
		Endpoints: []GatewayEndpoint{{Name: "shared-tools", URL: "https://tools.example.test/mcp/"},
			{Name: "knowledge", URL: "https://tools.example.test/knowledge/mcp"}},
	}}
}

func TestConnectionRegistryAdapterParity(t *testing.T) {
	profile := testConnectionProfile()
	profile.Gateway.LocalServers = []Server{{Name: "local", Command: "/opt/agent/local-mcp"}}
	registry, err := ConnectionRegistry(t.Context(), profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Servers) != 3 {
		t.Fatal("gateway or local server lost")
	}
	for _, client := range []Client{Codex, Claude, Gemini, AGY, OpenCodeV1, Continue, Cline, Kilo} {
		t.Run(string(client), func(t *testing.T) {
			plan, err := BuildPlan(t.Context(), registry, client, nil)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(plan.Commands)
			if err != nil {
				t.Fatal(err)
			}
			combined := string(plan.Content) + string(encoded)
			for _, want := range []string{"--endpoint", "https://tools.example.test/mcp/", "--token-file", "/private/agent-token", "local"} {
				if !strings.Contains(combined, want) {
					t.Fatalf("adapter lost %s", want)
				}
			}
		})
	}
}

func TestConnectionProfileRejectsInvalidInputs(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`, `{"version":1,"version":1}`, `{"version":1,"token":"do-not-echo"}`,
		`{"version":1,"gateway":{"bridge_command":"/bridge","token_file":"/key","endpoints":null}}`,
	} {
		if _, err := DecodeConnectionProfile(t.Context(), []byte(raw)); err == nil {
			t.Fatalf("accepted invalid profile %s", raw)
		}
	}
	for _, endpoint := range []string{"http://tools.example.test/mcp", "https://u:secret@example.test/mcp", "https://example.test/mcp?token=secret", "https://example.test/mcp#fragment", "https:///mcp", "https://example.test/$TOKEN"} {
		profile := testConnectionProfile()
		profile.Gateway.Endpoints[0].URL = endpoint
		if _, err := ConnectionRegistry(t.Context(), profile); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid endpoint was accepted or leaked: %v", err)
		}
	}
}

func TestConnectionBoundsAndCancellation(t *testing.T) {
	profile := testConnectionProfile()
	profile.Gateway.Endpoints = profile.Gateway.Endpoints[:1]
	for i := 0; i < MaxServers-1; i++ {
		profile.Gateway.LocalServers = append(profile.Gateway.LocalServers, Server{Name: "local" + strings.Repeat("x", i), Command: "/local"})
	}
	if _, err := ConnectionRegistry(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	profile.Gateway.LocalServers = append(profile.Gateway.LocalServers, Server{Name: "overflow", Command: "/local"})
	if _, err := ConnectionRegistry(t.Context(), profile); err == nil {
		t.Fatal("accepted more than 32 servers")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ConnectionArtifacts(ctx, testConnectionProfile()); err == nil {
		t.Fatal("ignored canceled context")
	}
	profile = testConnectionProfile()
	profile.Gateway.LocalServers = []Server{{Name: "shared-tools", Command: "/local"}}
	if _, err := ConnectionRegistry(t.Context(), profile); err == nil {
		t.Fatal("accepted duplicate projected server identity")
	}
}

func TestMemoryBindingPreservesPrivateSettingsAndIsIdempotent(t *testing.T) {
	before := []byte(`{"apiToken":"private-fixture","bankIdTemplate":"{gitProject}","mapPathToBank":{"/other":"other-bank"},"unknown":{"keep":true}}`)
	binding := MemoryBinding{ProjectRoot: "/work/project", BankID: "private::project::example"}
	plan, err := BuildMemoryPlan(t.Context(), binding, before)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"private-fixture", "{gitProject}", "other-bank", `"keep": true`, "private::project::example"} {
		if !strings.Contains(string(plan.Content), want) {
			t.Fatalf("lost setting %s", want)
		}
	}
	metadata, err := json.Marshal(plan)
	if err != nil || bytes.Contains(metadata, []byte("private-fixture")) {
		t.Fatalf("plan metadata exposed credential: %v", err)
	}
	again, err := BuildMemoryPlan(t.Context(), binding, plan.Content)
	if err != nil || again.Changed || !bytes.Equal(again.Content, plan.Content) {
		t.Fatalf("memory binding is not byte-idempotent: %v", err)
	}
}

func TestMemoryBindingRejectsConflictAndBoundaries(t *testing.T) {
	binding := MemoryBinding{ProjectRoot: "/work/project", BankID: "bank"}
	for _, raw := range []string{`{"mapPathToBank":{"/work/project":"different"}}`, `{"mapPathToBank":null}`, `{"mapPathToBank":[]}`, `{`, `{} {}`, `{"duplicate":1,"duplicate":2}`} {
		if _, err := BuildMemoryPlan(t.Context(), binding, []byte(raw)); err == nil {
			t.Fatalf("accepted conflict or malformed config: %s", raw)
		}
	}
	binding.BankID = strings.Repeat("b", 256)
	if _, err := BuildMemoryPlan(t.Context(), binding, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	binding.BankID += "b"
	if _, err := BuildMemoryPlan(t.Context(), binding, []byte(`{}`)); err == nil {
		t.Fatal("accepted oversized bank")
	}
	binding = MemoryBinding{ProjectRoot: "/", BankID: "bank"}
	if _, err := BuildMemoryPlan(t.Context(), binding, []byte(`{}`)); err == nil {
		t.Fatal("accepted workstation-wide root mapping")
	}
}
