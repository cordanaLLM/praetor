package router

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const routingFixture = `version: 1
tiers:
  work:
    target_tasks: [implement]
    models:
      - id: explicit-free
        family: openai
        cost_per_m_in: 0
        cost_per_m_out: 0
        capabilities: [tools]
governance:
  exhaustion_threshold_percent: 80
`

func routingInput(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "routing-input")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRoutingConfigExplicitZeroAndFingerprint(t *testing.T) {
	cfg, err := LoadRoutingConfigContext(context.Background(), routingInput(t, routingFixture))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourceSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(routingFixture))) {
		t.Fatal("source fingerprint differs from loaded bytes")
	}
	model := cfg.Tiers["work"].Models[0]
	if !model.CostRatesDeclared || model.CostPerMIn != 0 || model.CostPerMOut != 0 {
		t.Fatal("explicit zero price lost")
	}
	route := routeTask(t, NewModelCapacityArbiter(cfg, nil), TaskRequest{Task: "implement", InputTokens: 1, Capabilities: []string{"tools"}})
	if route.Model.ID != "explicit-free" || route.EstimatedCost != 0 {
		t.Fatal("explicit free candidate rejected")
	}
}

func TestRoutingConfigRejectsAmbiguousOrInvalidValues(t *testing.T) {
	cases := []string{
		strings.Replace(routingFixture, "        cost_per_m_out: 0\n", "", 1),
		strings.Replace(routingFixture, "cost_per_m_out: 0", "cost_per_m_out: null", 1),
		strings.Replace(routingFixture, "cost_per_m_out: 0", "cost_per_m_out: .nan", 1),
		strings.Replace(routingFixture, "cost_per_m_out: 0", "cost_per_m_out: -1", 1),
		strings.Replace(routingFixture, "family: openai", "family: openai\n        family: google", 1),
		strings.Replace(routingFixture, "capabilities: [tools]", "capabilities: [tools, tools]", 1),
		routingFixture + "unexpected: true\n", routingFixture + "---\nversion: 1\n",
		strings.Replace(routingFixture, "version: 1", "version: 2", 1),
		strings.Replace(routingFixture, "family: openai", "family: openai\n        source: remote", 1),
	}
	for i, body := range cases {
		if _, err := LoadRoutingConfig(routingInput(t, body)); err == nil {
			t.Fatalf("invalid routing case %d accepted", i)
		}
	}
}

func TestRoutingConfigReadAndTagBounds(t *testing.T) {
	path := routingInput(t, routingFixture+strings.Repeat(" ", MaxRoutingFileBytes-len(routingFixture)))
	if _, err := LoadRoutingConfig(path); err != nil {
		t.Fatalf("exact byte bound rejected: %v", err)
	}
	if err := os.Truncate(path, MaxRoutingFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRoutingConfig(path); err == nil {
		t.Fatal("oversized config accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(routingInput(t, routingFixture), link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRoutingConfig(link); err == nil {
		t.Fatal("nonregular config accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadRoutingConfigContext(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	tags := make([]string, MaxRoutingTags)
	for i := 0; i < MaxRoutingTags; i++ {
		tags[i] = fmt.Sprintf("tag-%d", i)
	}
	cfg := taskConfig(taskModel("tagged", 1, 1, tags...))
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRoutingConfig(routingInput(t, string(data))); err != nil {
		t.Fatal(err)
	}
	if _, err := NewModelCapacityArbiter(cfg, nil).SelectForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 1, Capabilities: tags}); err != nil {
		t.Fatalf("exact capability bound: %v", err)
	}
	if err := validateRoutingTags(append(tags, "overflow")); err == nil {
		t.Fatal("cap+1 tags accepted")
	}
}

func TestRoutingConfigModelSourceField(t *testing.T) {
	// Every known model key at once: the mapping sits exactly on maxModelFieldNodes.
	allKeys := strings.Replace(routingFixture, "family: openai", "family: openai\n        source: local\n        rpm_limit: 1\n        tpm_limit: 1", 1)
	cfg, err := LoadRoutingConfig(routingInput(t, allKeys))
	if err != nil {
		t.Fatalf("model with every known key rejected: %v", err)
	}
	if source := cfg.Tiers["work"].Models[0].Source; source != SourceLocal {
		t.Fatalf("source %q, want local", source)
	}
	cfg, err = LoadRoutingConfig(routingInput(t, routingFixture))
	if err != nil || cfg.Tiers["work"].Models[0].Source != SourceOperator {
		t.Fatalf("absent source must load as operator-declared: %v", err)
	}
	extra := strings.Replace(allKeys, "rpm_limit: 1", "rpm_limit: 1\n        unknown: 1", 1)
	if _, err := LoadRoutingConfig(routingInput(t, extra)); err == nil {
		t.Fatal("model mapping past the field bound accepted")
	}
}
