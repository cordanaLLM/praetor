package builder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUniversalBuilder_Positive(t *testing.T) {
	tmpDir := t.TempDir()
	builder := NewUniversalBuilder()

	cfg := &BuildConfig{
		Version:   1,
		Project:   "test-go",
		OutputDir: filepath.Join(tmpDir, "dist-go"),
		Optimize:  true,
		Targets: map[string]TargetConfig{
			"cli": {
				Runtime:      "go",
				Entrypoint:   "cmd/main.go",
				Capabilities: []string{"logging", "metrics"},
			},
		},
	}

	results, err := builder.Build(context.Background(), cfg, "cli")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || !results[0].Success || !results[0].Optimized {
		t.Errorf("expected 1 successful optimized result, got %+v", results)
	}
}

func TestUniversalBuilder_Polyglot(t *testing.T) {
	tmpDir := t.TempDir()
	builder := NewUniversalBuilder()

	cfg := &BuildConfig{
		Version:   1,
		Project:   "test-polyglot",
		OutputDir: filepath.Join(tmpDir, "dist-poly"),
		Targets: map[string]TargetConfig{
			"frontend": {Runtime: "svelte", Entrypoint: "src/App.svelte"},
			"backend":  {Runtime: "python", Entrypoint: "main.py"},
			"engine":   {Runtime: "rust", Entrypoint: "src/lib.rs"},
			"native":   {Runtime: "native-gpu", Entrypoint: "src/kernel.c"},
		},
	}

	results, err := builder.Build(context.Background(), cfg, "all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
}

func TestUniversalBuilder_Negative(t *testing.T) {
	builder := NewUniversalBuilder()
	cfg := &BuildConfig{Targets: map[string]TargetConfig{"app": {Runtime: "go"}}}

	var absentContext context.Context
	if _, err := builder.Build(absentContext, cfg, "app"); err == nil {
		t.Error("expected error with nil context, got nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := builder.Build(ctx, cfg, "app"); err == nil {
		t.Error("expected error with canceled context, got nil")
	}

	if _, err := builder.Build(context.Background(), cfg, "nonexistent"); err == nil {
		t.Error("expected error for nonexistent target, got nil")
	}

	badCfg := &BuildConfig{Targets: map[string]TargetConfig{"bad": {Runtime: "unknown"}}}
	if _, err := builder.Build(context.Background(), badCfg, "bad"); err == nil {
		t.Error("expected error for unsupported runtime, got nil")
	}
}

func TestUniversalBuilder_Boundary(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, ".framework-build.yaml")
	yamlContent := "version: 1\nproject: sample-project\ntargets:\n  srv:\n    runtime: go\n"

	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadBuildConfig(cfgPath)
	if err != nil || cfg.Project != "sample-project" || cfg.OutputDir != "dist" {
		t.Fatalf("LoadBuildConfig failed or unexpected: %v, %+v", err, cfg)
	}

	if _, err := LoadBuildConfig(filepath.Join(tmpDir, "missing.yaml")); err == nil {
		t.Error("expected error loading missing file, got nil")
	}
}

func TestNativeGPUConfig_Meson(t *testing.T) {
	cfg := DefaultNativeGPUConfig()
	args, err := cfg.GenerateMesonArgs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasC23 := false
	hasLTO := false
	for _, arg := range args {
		if strings.Contains(arg, "c_std=c23") {
			hasC23 = true
		}
		if strings.Contains(arg, "b_lto=true") {
			hasLTO = true
		}
	}
	if !hasC23 || !hasLTO {
		t.Errorf("missing expected meson args: %v", args)
	}
}

func TestNativeGPUConfig_CMake(t *testing.T) {
	cfg := &NativeGPUConfig{
		CStandard:    "c17",
		CppStandard:  "c++20",
		Accelerators: []string{"cuda"},
		Libraries:    []string{"libvmaf"},
		EnableLTO:    true,
	}

	args, err := cfg.GenerateCMakeArgs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasC17 := false
	hasCUDA := false
	for _, arg := range args {
		if strings.Contains(arg, "CMAKE_C_STANDARD=17") {
			hasC17 = true
		}
		if strings.Contains(arg, "WITH_CUDA=ON") {
			hasCUDA = true
		}
	}
	if !hasC17 || !hasCUDA {
		t.Errorf("missing expected cmake args: %v", args)
	}
}

func TestNativeGPUConfig_Negative(t *testing.T) {
	var cfg *NativeGPUConfig
	if _, err := cfg.GenerateMesonArgs(); err == nil {
		t.Error("expected error for nil config, got nil")
	}
	if _, err := cfg.GenerateCMakeArgs(); err == nil {
		t.Error("expected error for nil config, got nil")
	}
}

func TestPreBuildOptimizer_3D(t *testing.T) {
	optimizer := NewPreBuildOptimizer()
	runtimes := []string{"go", "svelte", "python", "rust", "native-gpu", "custom"}

	for _, r := range runtimes {
		plan := optimizer.Plan(r, []string{"logging"})
		if plan.Runtime != r {
			t.Errorf("expected runtime %s, got %s", r, plan.Runtime)
		}
		target := &TargetConfig{Runtime: r}
		if err := optimizer.Optimize(target, plan); err != nil {
			t.Errorf("failed to optimize target for %s: %v", r, err)
		}
	}

	if err := optimizer.Optimize(nil, optimizer.Plan("go", nil)); err == nil {
		t.Error("expected error for nil target, got nil")
	}
	if err := optimizer.Optimize(&TargetConfig{}, nil); err == nil {
		t.Error("expected error for nil plan, got nil")
	}
}

func TestLoadBuildConfigRejectsLinkedAndOversizedSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	if err := os.WriteFile(source, []byte("project: fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBuildConfigContext(t.Context(), link); err == nil {
		t.Fatal("linked config accepted")
	}
	if err := os.WriteFile(source, make([]byte, (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBuildConfigContext(t.Context(), source); err == nil {
		t.Fatal("oversized config accepted")
	}
}
