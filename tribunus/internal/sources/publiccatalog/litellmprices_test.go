package publiccatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const sampleLiteLLMPrices = `{
  "sample_spec": {"input_cost_per_token": 0.0, "litellm_provider": "one of https://docs.litellm.ai/docs/providers"},
  "gpt-4": {"input_cost_per_token": 0.00003, "output_cost_per_token": 0.00006, "max_input_tokens": 8192, "litellm_provider": "openai", "mode": "chat"}
}`

func TestFetchLiteLLMPrices_Positive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleLiteLLMPrices))
	}))
	defer server.Close()

	records, err := fetchLiteLLMPrices(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetchLiteLLMPrices() = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1 (sample_spec skipped)", len(records))
	}
	rec := records[0]
	if rec.ModelID != "gpt-4" {
		t.Fatalf("ModelID = %q, want gpt-4", rec.ModelID)
	}
	if rec.PriceInPerM == nil || *rec.PriceInPerM != 30 {
		t.Fatalf("PriceInPerM = %v, want 30", rec.PriceInPerM)
	}
	if rec.ContextWindow == nil || *rec.ContextWindow != 8192 {
		t.Fatalf("ContextWindow = %v, want 8192", rec.ContextWindow)
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestFetchLiteLLMPrices_Negative(t *testing.T) {
	t.Run("unreachable", func(t *testing.T) {
		if _, err := fetchLiteLLMPrices(context.Background(), "http://127.0.0.1:1"); err == nil {
			t.Fatal("fetchLiteLLMPrices() = nil error, want failure for an unreachable host")
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("[]")) // an array, not the expected object
		}))
		defer server.Close()
		if _, err := fetchLiteLLMPrices(context.Background(), server.URL); err == nil {
			t.Fatal("fetchLiteLLMPrices() = nil error, want failure for a body that is not an object")
		}
	})
}

// TestFetchLiteLLMPrices_Boundary covers max_input_tokens vs the legacy
// max_tokens fallback: when max_input_tokens is absent, max_tokens must be
// used instead, matching the price map's own documented fallback order.
func TestFetchLiteLLMPrices_Boundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"legacy-model": {"max_tokens": 4096, "litellm_provider": "openai"}}`))
	}))
	defer server.Close()

	records, err := fetchLiteLLMPrices(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetchLiteLLMPrices() = %v", err)
	}
	if len(records) != 1 || records[0].ContextWindow == nil || *records[0].ContextWindow != 4096 {
		t.Fatalf("records = %+v, want one record with ContextWindow=4096 from the max_tokens fallback", records)
	}
}
