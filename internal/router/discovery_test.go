package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestLocalDiscoveryErrorsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"valid", `{"models":[{"name":"local"}]}`, 200, false},
		{"bad-status", "", 500, true}, {"malformed", "{", 200, true},
		{"oversized", strings.Repeat(" ", MaxRoutingFileBytes+1), 200, true},
		{"serves neither listing", "", 404, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			got, err := queryLocalEndpoint(context.Background(), server.Client(), server.URL)
			if (err != nil) != tc.wantErr {
				t.Fatalf("models=%v error=%v", got, err)
			}
		})
	}
}

// listingServer serves body on path and 404 on every other path, like a runtime that
// implements only one listing protocol.
func listingServer(t *testing.T, path, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func discoveredIDs(models []ModelDescriptor) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// Positive: an Ollama endpoint on any port and a vLLM endpoint that serves only the
// OpenAI listing are both probed; the old filter queried only URLs containing 11434.
func TestDiscoverLocalModelsProbesEveryEndpoint(t *testing.T) {
	ollama := listingServer(t, "/api/tags", `{"models":[{"name":"qwen3:30b"},{"name":"gemma-2-9b"}]}`)
	vllm := listingServer(t, "/v1/models", `{"object":"list","data":[{"id":"Qwen/Qwen3-235B-A22B","object":"model"}]}`)
	models, err := DiscoverLocalModels(context.Background(), []string{ollama.URL, vllm.URL})
	if err != nil {
		t.Fatal(err)
	}
	if got := discoveredIDs(models); !slices.Equal(got, []string{"qwen3:30b", "gemma-2-9b", "Qwen/Qwen3-235B-A22B"}) {
		t.Fatalf("discovered %v", got)
	}
	for _, model := range models {
		if model.Family != DetectFamily(model.ID) || model.RPMLimit == 0 {
			t.Fatalf("descriptor %+v", model)
		}
	}
}

// Negative: a failing and an unreachable endpoint are each reported, and the models of
// the endpoint that answered are still returned.
func TestDiscoverLocalModelsIsolatesFailingEndpoints(t *testing.T) {
	good := listingServer(t, "/api/tags", `{"models":[{"name":"llama-3.2:3b"}]}`)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	models, err := DiscoverLocalModels(context.Background(), []string{broken.URL, good.URL, gone.URL})
	if got := discoveredIDs(models); !slices.Equal(got, []string{"llama-3.2:3b"}) {
		t.Fatalf("healthy endpoint lost: %v", got)
	}
	var endpoint *EndpointError
	if !errors.As(err, &endpoint) || endpoint.Endpoint != broken.URL {
		t.Fatalf("error %v does not name the first failed endpoint", err)
	}
	failures := discoveryFailures(err)
	if len(failures) != 2 || !strings.Contains(failures[0], "HTTP 500") || !strings.Contains(failures[1], gone.URL) {
		t.Fatalf("failures %q", failures)
	}
}

// Boundary: no endpoints is an empty inventory, not an error.
func TestDiscoverLocalModelsWithoutEndpoints(t *testing.T) {
	models, err := DiscoverLocalModels(context.Background(), nil)
	if err != nil || len(models) != 0 {
		t.Fatalf("models=%v err=%v", models, err)
	}
}
