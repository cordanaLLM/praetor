package repairrun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/dogfood"
)

func TestJobProviderAppliesTheRegisterBudget(t *testing.T) {
	provider := ProviderConfig{MaxOutputTokens: 1024}
	cases := map[string]struct {
		job  *dogfood.RepairJob
		want int
	}{
		"budget below the configured limit": {&dogfood.RepairJob{MaxOutputTokens: 512}, 512},
		"no budget":                         {&dogfood.RepairJob{}, 1024},
		"no job":                            {nil, 1024},
		"budget equal to the limit":         {&dogfood.RepairJob{MaxOutputTokens: 1024}, 1024},
		"budget above the limit never raises spend": {&dogfood.RepairJob{MaxOutputTokens: config.RegisterMaxTokensCeiling}, 1024},
		"smallest valid budget":                     {&dogfood.RepairJob{MaxOutputTokens: config.RegisterMaxTokensFloor}, config.RegisterMaxTokensFloor},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := jobProvider(provider, tc.job).MaxOutputTokens; got != tc.want {
				t.Fatalf("max output tokens = %d, want %d", got, tc.want)
			}
			if provider.MaxOutputTokens != 1024 {
				t.Fatal("jobProvider mutated the configured provider")
			}
		})
	}
}

// The provider limits and the manifest budget share one pair of constants, so a budget
// the manifest accepts is one the provider path accepts.
func TestProviderLimitsMatchTheRegisterBudgetRange(t *testing.T) {
	cfg := ProviderConfig{MaxInputBytes: 1}
	for budget, want := range map[int]bool{
		config.RegisterMaxTokensFloor - 1: false, config.RegisterMaxTokensFloor: true,
		config.RegisterMaxTokensCeiling: true, config.RegisterMaxTokensCeiling + 1: false, 0: false,
	} {
		cfg.MaxOutputTokens = budget
		if got := providerValidLimits(cfg); got != want {
			t.Errorf("providerValidLimits(%d) = %v, want %v", budget, got, want)
		}
	}
}

func TestProviderRequestForwardsTheJobBudget(t *testing.T) {
	cfg := providerFixtureConfig(t, "printf '%s\\n' '"+providerFixtureToken+"'")
	cfg.MaxOutputTokens = 1024
	for name, job := range map[string]*dogfood.RepairJob{"budget": {MaxOutputTokens: 512}, "fallback": {}} {
		want := jobProvider(cfg, job).MaxOutputTokens
		response := providerFixtureResponse(t)
		client := providerFixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["max_output_tokens"] != float64(want) {
				t.Errorf("%s: max_output_tokens = %v, want %d", name, body["max_output_tokens"], want)
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Error(err)
			}
		})
		if _, err := providerGenerate(t.Context(), jobProvider(cfg, job), "Public context", client); err != nil {
			t.Fatalf("%s: generation: %v", name, err)
		}
	}
}

func TestRunRecordsRegisterAndTightensProviderBudget(t *testing.T) {
	f := newRunFixture(t, 1)
	f.config.RepairPolicy.Register, f.config.RepairPolicy.MaxOutputTokens = string(config.TextRegisterInternal), 512
	f.save(t)
	seen := 0
	generate := func(_ context.Context, provider ProviderConfig, _ string) (*Proposal, error) {
		seen = provider.MaxOutputTokens
		return nil, errors.New("provider failed")
	}
	result, err := run(t.Context(), f.configPath, f.reportPath, generate, fakeVerification(false))
	if err == nil || result.Status != "agent_failed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if seen != 512 {
		t.Fatalf("provider saw a %d-token limit, want the 512-token budget of the job", seen)
	}
	if result.Register != "internal" {
		t.Fatalf("report register = %q, want internal", result.Register)
	}

	// An invalid register row in the run configuration is rejected before any provider call.
	g := newRunFixture(t, 1)
	g.config.RepairPolicy.Register = "loud"
	g.save(t)
	if _, err := run(t.Context(), g.configPath, g.reportPath, noProvider(t), fakeVerification(false)); err == nil {
		t.Fatal("an unsupported register must fail the run")
	}
}
