package builder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOptimizerPreparesOptionsWithoutBuildEvidence(t *testing.T) {
	p := NewPreBuildOptimizer()
	plan := p.Plan("go", []string{"api"})
	target := &TargetConfig{Runtime: "go", Entrypoint: "missing.go", Options: map[string]string{"keep": "value"}}
	if err := p.Optimize(target, plan); err != nil {
		t.Fatal(err)
	}
	if target.Options["optimization_flags"] != "-ldflags=-s -w -trimpath CGO_ENABLED=0" || target.Options["strip_symbols"] != "true" {
		t.Fatalf("missing declared Go options: %+v", target.Options)
	}
	if len(plan.PrunedModules) != 0 || target.Options["keep"] != "value" || target.Entrypoint != "missing.go" {
		t.Fatalf("planning changed inputs outside options: %+v, %+v", target, plan)
	}
}

func TestLoadBuildConfigRejectsCanceledMalformedAndNonregularInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(file, []byte("targets: [invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, dir} {
		if cfg, err := LoadBuildConfigContext(t.Context(), path); err == nil || cfg != nil {
			t.Fatalf("invalid manifest accepted: %+v, %v", cfg, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if cfg, err := LoadBuildConfigContext(ctx, file); cfg != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled configuration read accepted: %+v, %v", cfg, err)
	}
}
