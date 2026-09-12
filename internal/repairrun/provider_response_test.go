package repairrun

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func providerEncoded(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProviderResponseRejectsFailedAndContradictoryStates(t *testing.T) {
	changes := map[string]func(map[string]any){
		"incomplete":       func(v map[string]any) { v["status"] = "incomplete" },
		"error":            func(v map[string]any) { v["error"] = map[string]any{"message": "private"} },
		"missing error":    func(v map[string]any) { delete(v, "error") },
		"missing identity": func(v map[string]any) { delete(v, "model") },
		"incomplete detail": func(v map[string]any) {
			v["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
		},
		"missing usage": func(v map[string]any) { delete(v, "usage") },
		"negative usage": func(v map[string]any) {
			v["usage"] = map[string]any{"input_tokens": -1, "output_tokens": 42}
		},
		"null usage": func(v map[string]any) {
			v["usage"] = map[string]any{"input_tokens": nil, "output_tokens": 42}
		},
		"fractional usage": func(v map[string]any) {
			v["usage"] = map[string]any{"input_tokens": 0.5, "output_tokens": 42}
		},
		"overbound usage": func(v map[string]any) {
			v["usage"] = map[string]any{"input_tokens": 24, "output_tokens": 257}
		},
		"inconsistent total": func(v map[string]any) {
			v["usage"] = map[string]any{"input_tokens": 24, "output_tokens": 42, "total_tokens": 70}
		},
		"tool output": func(v map[string]any) {
			v["output"] = []any{map[string]any{"type": "function_call", "name": "run_shell"}}
		},
		"refusal": func(v map[string]any) {
			v["output"] = []any{map[string]any{"type": "message", "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "refusal", "refusal": "refused"}}}}
		},
		"truncated message": func(v map[string]any) {
			v["output"] = []any{map[string]any{"type": "message", "role": "assistant", "status": "incomplete"}}
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			value := providerFixtureResponse(t)
			change(value)
			if proposal, err := providerDecodeResponse(providerEncoded(t, value), 256); err == nil || proposal != nil {
				t.Fatal("invalid response was accepted")
			}
		})
	}
}

func TestProviderProposalStrictShapeAndUnicode(t *testing.T) {
	validEdit := `{"path":"file.go","original_sha256":"` + strings.Repeat("a", 64) + `","content":""}`
	invalid := []string{
		`{"summary":"x","edits":[],"extra":true}`,
		`{"summary":"x","Summary":"y","edits":[]}`,
		`{"summary":"x","summary":"y","edits":[]}`,
		`{"summary":"x","edits":null}`,
		`{"summary":null,"edits":[]}`,
		`{"summary":"x","Edits":[]}`,
		`{"summary":"\ud800","edits":[]}`,
		`{"summary":"\udc00","edits":[]}`,
		`{"summary":"x","edits":[` + strings.Replace(validEdit, `"content":""`, `"content":null`, 1) + `]}`,
		`{"summary":"x","edits":[` + strings.Replace(validEdit, `"path"`, `"PATH"`, 1) + `]}`,
		`{"summary":"x","edits":[` + strings.Replace(validEdit, `"content":""`, `"content":"","extra":true`, 1) + `]}`,
		`{"summary":"x","edits":[` + strings.TrimSuffix(strings.Repeat(validEdit+",", 9), ",") + `]}`,
		`{"summary":"x","edits":[` + strings.Replace(validEdit, `"content":""`, `"content":"`+strings.Repeat("x", providerPromptLimit+1)+`"`, 1) + `]}`,
	}
	for index, raw := range invalid {
		if proposal, err := providerDecodeProposal([]byte(raw)); err == nil || proposal != nil {
			t.Errorf("invalid proposal %d accepted", index)
		}
	}
	for _, summary := range []string{`normal`, `\ud83d\ude00`, `literal \\ud800`} {
		raw := `{"summary":"` + summary + `","edits":[` + validEdit + `]}`
		if _, err := providerDecodeProposal([]byte(raw)); err != nil {
			t.Errorf("valid Unicode rejected: %v", err)
		}
	}
}

func TestProviderJSONGlobalBounds(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"usage":{"input_tokens":1,"input_tokens":2}}`),
		[]byte(`{} {}`), {0xff}, []byte(`{"unfinished":`),
		[]byte(strings.Repeat("[", 17) + "0" + strings.Repeat("]", 17)),
		[]byte(`{"text":"` + strings.Repeat("x", providerResponseLimit) + `"}`),
		[]byte("[" + strings.TrimSuffix(strings.Repeat("0,", 65536), ",") + "]"),
	} {
		if err := providerValidateJSON(raw); err == nil {
			t.Fatal("invalid or unbounded JSON accepted")
		}
	}
}

func TestProviderResponseSizeCredentialAndCostBoundaries(t *testing.T) {
	valid := string(providerEncoded(t, providerFixtureResponse(t)))
	for _, test := range []struct{ name, body, contentType, cost string }{
		{"overflow", strings.Repeat(" ", providerResponseLimit+1), "application/json", ""},
		{"wrong type", valid, "text/plain", ""},
		{"direct credential", strings.Replace(valid, "Correct the boundary check", providerFixtureToken, 1), "application/json", ""},
		{"escaped credential", strings.Replace(valid, "Correct the boundary check", `sk-\\u0070rovider-fixture`, 1), "application/json", ""},
		{"bad cost", valid, "application/json", "NaN"},
		{"negative cost", valid, "application/json", "-0.1"},
		{"infinite cost", valid, "application/json", "1e999"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := providerFixtureClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				if test.cost != "" {
					w.Header().Set("X-Litellm-Response-Cost", test.cost)
				}
				if _, err := io.WriteString(w, test.body); err != nil && test.name != "overflow" {
					t.Error(err)
				}
			})
			proposal, err := providerRequest(t.Context(), ProviderConfig{BaseURL: "https://provider.example/v1", MaxOutputTokens: 256}, "public", providerFixtureToken, client)
			if err == nil || proposal != nil || strings.Contains(err.Error(), providerFixtureToken) {
				t.Fatalf("unsafe result: %+v %v", proposal, err)
			}
		})
	}
	for _, values := range [][]string{{}, {"0"}, {"0.01"}, {"0.1", "0.2"}} {
		header := http.Header{}
		for _, value := range values {
			header.Add("X-Litellm-Response-Cost", value)
		}
		cost, err := providerCost(header)
		if len(values) == 0 && (cost != nil || err != nil) || len(values) == 1 && (cost == nil || err != nil) || len(values) > 1 && err == nil {
			t.Fatalf("cost boundary %v: %v %v", values, cost, err)
		}
	}
}

func TestProviderUsagePreservesUnknownAndExplicitZeroCost(t *testing.T) {
	unknown := providerEncoded(t, Usage{InputTokens: 1, OutputTokens: 2})
	if strings.Contains(string(unknown), "cost_usd") {
		t.Fatal("unobserved cost must be omitted for strict retained-report readback")
	}
	zero := 0.0
	observed := providerEncoded(t, Usage{InputTokens: 1, OutputTokens: 2, CostUSD: &zero})
	if !strings.Contains(string(observed), `"cost_usd":0`) {
		t.Fatal("observed zero cost must remain explicit")
	}
}
