package router

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutingOffloadLabels(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", DefaultConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err := ParseRoutingConfig(data, "shipped"); err != nil || cfg.Offload != nil {
		t.Fatalf("shipped config must have no offload rule: %+v, %v", cfg, err)
	}
	withOffload := append(data, []byte("\noffload:\n  labels: [feature_implementation]\n")...)
	path := filepath.Join(t.TempDir(), "routing.yaml")
	if err := os.WriteFile(path, withOffload, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRoutingConfigContext(context.Background(), path)
	if err != nil || cfg.Offload == nil || len(cfg.Offload.Labels) != 1 {
		t.Fatalf("declared offload refused: %+v, %v", cfg, err)
	}
	for _, bad := range []string{"not_declared", "feature_implementation, feature_implementation"} {
		changed := strings.Replace(string(withOffload), "[feature_implementation]", "["+bad+"]", 1)
		if _, err := ParseRoutingConfig([]byte(changed), "invalid"); err == nil || !strings.Contains(err.Error(), "offload") {
			t.Fatalf("invalid offload %q accepted: %v", bad, err)
		}
	}
}
