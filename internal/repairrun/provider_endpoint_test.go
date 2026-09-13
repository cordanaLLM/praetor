package repairrun

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProviderGenerateUsesConfiguredTLSEndpoint(t *testing.T) {
	for _, basePath := range []string{"", "/api/provider/v2"} {
		t.Run(basePath, func(t *testing.T) {
			cfg := providerFixtureConfig(t, "printf '%s\\n' '"+providerFixtureToken+"'")
			var calls atomic.Int32
			response := providerFixtureResponse(t)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != basePath+"/responses" || r.Header.Get("Authorization") != "Bearer "+providerFixtureToken {
					t.Error("configured endpoint, path or authentication was not preserved")
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			cfg.BaseURL = server.URL + basePath
			client := providerClient()
			client.Transport = server.Client().Transport
			proposal, err := providerGenerate(t.Context(), cfg, "Public fixture context", client)
			if err != nil || proposal == nil || calls.Load() != 1 {
				t.Fatalf("configured TLS endpoint: calls=%d proposal=%v error=%v", calls.Load(), proposal != nil, err)
			}
		})
	}
}

func TestProviderEndpointRejectsAmbiguousSuffixBeforeHelper(t *testing.T) {
	cfg := providerFixtureConfig(t, "exit 97")
	for _, endpoint := range []string{"https://provider.example/", "https://provider.example/v1/", "https://provider.example/v1?", "https://user:secret@provider.example/v1"} {
		t.Run(endpoint, func(t *testing.T) {
			cfg.BaseURL = endpoint
			_, err := Generate(t.Context(), cfg, "Public fixture context")
			if err == nil || !strings.Contains(err.Error(), "endpoint") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("endpoint must fail before helper without exposing URL: %v", err)
			}
		})
	}
}
