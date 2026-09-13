package repairrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const providerFixtureToken = "sk-provider-fixture"

func providerFixtureConfig(t *testing.T, script string) ProviderConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential-helper")
	data := []byte("#!/bin/sh\nset -eu\n" + script + "\n")
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return ProviderConfig{BaseURL: "https://provider.example/v1", TokenCommand: path,
		TokenCommandSHA256: hex.EncodeToString(sum[:]), Model: "cordana/fixture", MaxInputBytes: providerPromptLimit, MaxOutputTokens: 256}
}

func providerFixtureResponse(t *testing.T) map[string]any {
	t.Helper()
	proposal := map[string]any{"summary": "Correct the boundary check", "edits": []any{map[string]any{
		"path": "internal/example.go", "original_sha256": strings.Repeat("a", 64), "content": "package example\n"}}}
	data, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"id": "resp_fixture", "model": "fixture-backend", "status": "completed", "error": nil, "incomplete_details": nil,
		"usage": map[string]any{"input_tokens": 24, "output_tokens": 42, "total_tokens": 66},
		"output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": string(data)}}}}}
}

type providerFixtureTransport struct {
	target *url.URL
	inner  http.RoundTripper
}

func (transport providerFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = transport.target.Scheme, transport.target.Host
	return transport.inner.RoundTrip(clone)
}

func providerFixtureClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := providerClient()
	client.Transport = providerFixtureTransport{target: target, inner: server.Client().Transport}
	return client
}

func TestProviderGenerateTLSRequestAndUsage(t *testing.T) {
	t.Setenv("PROVIDER_TEST_SECRET", "must-not-inherit")
	cfg := providerFixtureConfig(t, "[ -z \"${PROVIDER_TEST_SECRET:-}\" ]\nprintf '%s\\n' '"+providerFixtureToken+"'")
	var calls atomic.Int32
	response := providerFixtureResponse(t)
	client := providerFixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer "+providerFixtureToken {
			t.Error("unexpected endpoint or authentication")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != cfg.Model || body["input"] != "Public context" || body["tool_choice"] != "none" || body["store"] != false || body["stream"] != false || body["max_output_tokens"] != float64(256) {
			t.Error("request contract widened")
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 0 {
			t.Error("tools must be explicitly empty")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Litellm-Response-Cost", "0.00012")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	})
	proposal, err := providerGenerate(t.Context(), cfg, "Public context", client)
	if err != nil || proposal == nil {
		t.Fatalf("generation: %v", err)
	}
	if calls.Load() != 1 || proposal.ActualModel != "fixture-backend" || proposal.ResponseID != "resp_fixture" || len(proposal.Edits) != 1 {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
	if proposal.Usage.InputTokens != 24 || proposal.Usage.OutputTokens != 42 || proposal.Usage.CostUSD == nil || *proposal.Usage.CostUSD != 0.00012 {
		t.Fatalf("wrong usage: %+v", proposal.Usage)
	}
}

func TestProviderRejectsRedirectAndHTTPErrorWithoutRetry(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cfg := providerFixtureConfig(t, "printf '%s\\n' '"+providerFixtureToken+"'")
			var calls atomic.Int32
			client := providerFixtureClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "https://unreviewed.invalid/leak")
				w.WriteHeader(status)
				if _, err := io.WriteString(w, providerFixtureToken); err != nil {
					t.Error(err)
				}
			})
			proposal, err := providerGenerate(t.Context(), cfg, "Public context", client)
			if err == nil || proposal != nil || strings.Contains(err.Error(), providerFixtureToken) || calls.Load() != 1 {
				t.Fatalf("error/attempt contract: %v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestProviderValidationPrecedesHelperExecution(t *testing.T) {
	cfg := providerFixtureConfig(t, "exit 0")
	for _, limits := range [][2]int{{1, 256}, {providerPromptLimit, 8192}} {
		valid := cfg
		valid.MaxInputBytes, valid.MaxOutputTokens = limits[0], limits[1]
		if err := ValidateProviderConfig(valid); err != nil {
			t.Fatalf("valid boundary rejected: %v", err)
		}
	}
	for _, change := range []func(*ProviderConfig){
		func(c *ProviderConfig) { c.BaseURL = "http://provider.example/v1" },
		func(c *ProviderConfig) { c.BaseURL += "?credential=bad" },
		func(c *ProviderConfig) { c.BaseURL = "https://user@provider.example/v1" },
		func(c *ProviderConfig) { c.TokenCommand = "relative" },
		func(c *ProviderConfig) { c.TokenCommandSHA256 = strings.Repeat("A", 64) },
		func(c *ProviderConfig) { c.Model = "" },
		func(c *ProviderConfig) { c.MaxInputBytes = providerPromptLimit + 1 },
		func(c *ProviderConfig) { c.MaxOutputTokens = 255 },
		func(c *ProviderConfig) { c.MaxOutputTokens = 8193 },
	} {
		bad := cfg
		change(&bad)
		if err := ValidateProviderConfig(bad); err == nil {
			t.Fatalf("invalid config accepted: %+v", bad)
		}
	}
	for _, input := range []string{"", strings.Repeat("x", providerPromptLimit+1), string([]byte{0xff})} {
		if _, err := Generate(t.Context(), cfg, input); err == nil || strings.Contains(err.Error(), "helper") {
			t.Fatalf("input must fail before helper: %v", err)
		}
	}
	var missing context.Context
	if _, err := Generate(missing, cfg, "context"); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestProviderTransportHasNoProxyAndBoundedDeadline(t *testing.T) {
	client := providerClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || !transport.DisableKeepAlives || client.Timeout != 120*time.Second {
		t.Fatal("provider transport is not isolated or bounded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cfg := providerFixtureConfig(t, "printf '%s\\n' '"+providerFixtureToken+"'")
	if _, err := providerGenerate(ctx, cfg, "context", client); err == nil {
		t.Fatal("cancelled operation accepted")
	}
}
