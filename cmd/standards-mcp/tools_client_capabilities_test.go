package main

import (
	"encoding/json"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientsetup"
)

func TestClientCapabilitiesToolMatchesSharedRegistry(t *testing.T) {
	srv, _ := newFixtureServer(t)
	result := callTool(t, srv, "standards_client_capabilities", nil)
	if result.IsError {
		t.Fatal(result)
	}
	want, err := clientsetup.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content[0].Text != string(data) {
		t.Fatal("MCP capabilities differ from shared inventory")
	}
	if result := callTool(t, srv, "standards_client_capabilities", map[string]any{"install": true}); !result.IsError {
		t.Fatal("capabilities accepted an installation request")
	}
}
