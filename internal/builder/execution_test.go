package builder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildRejectsUnexecutedTargetWithoutSideEffects(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "absent", "output")
	options := map[string]string{"keep": "unchanged"}
	cfg := &BuildConfig{Project: "fixture", OutputDir: output, Optimize: true,
		Targets: map[string]TargetConfig{"app": {
			Runtime: "go", Entrypoint: filepath.Join(dir, "missing.go"), Options: options,
		}}}
	results, err := NewUniversalBuilder().Build(t.Context(), cfg, "app")
	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("unimplemented build claimed success: %+v", results)
	}
	if len(results) != 1 || results[0].Success || results[0].Optimized || len(results[0].Artifacts) != 0 {
		t.Fatalf("expected one unexecuted result without artifacts: %+v", results)
	}
	if results[0].Status != BuildUnavailable || results[0].Reason == "" || results[0].OutputLogs != "" {
		t.Fatalf("missing truthful execution state: %+v", results)
	}
	if _, statErr := os.Stat(filepath.Dir(output)); !os.IsNotExist(statErr) {
		t.Fatalf("unavailable build touched output directory: %v", statErr)
	}
	if len(options) != 1 || options["keep"] != "unchanged" {
		t.Fatalf("unavailable build mutated input options: %v", options)
	}
}

func TestBuildReportsEveryUnavailableRuntime(t *testing.T) {
	runtimes := []string{"go", "svelte", "typescript", "python", "rust", "native-gpu", "native", "c", "cpp"}
	for _, runtime := range runtimes {
		t.Run(runtime, func(t *testing.T) {
			cfg := &BuildConfig{Targets: map[string]TargetConfig{"app": {Runtime: runtime}}}
			results, err := NewUniversalBuilder().Build(t.Context(), cfg, "app")
			if !errors.Is(err, ErrBackendUnavailable) || len(results) != 1 {
				t.Fatalf("expected unavailable backend: %+v, %v", results, err)
			}
			if results[0].Status != BuildUnavailable || results[0].Success || results[0].Optimized || len(results[0].Artifacts) != 0 {
				t.Fatalf("unexecuted runtime returned execution evidence: %+v", results)
			}
		})
	}
}

func TestBuildSeparatesUnknownRuntimeAndSortsResults(t *testing.T) {
	cfg := &BuildConfig{Targets: map[string]TargetConfig{
		"z-known": {Runtime: "go"}, "a-unknown": {Runtime: "custom"},
	}}
	results, err := NewUniversalBuilder().Build(t.Context(), cfg, "all")
	if !errors.Is(err, ErrBackendUnavailable) || !errors.Is(err, ErrUnsupportedRuntime) {
		t.Fatalf("lost distinct errors: %v", err)
	}
	if len(results) != 2 || results[0].Target != "a-unknown" || results[1].Target != "z-known" {
		t.Fatalf("missing deterministic rejected targets: %+v", results)
	}
	if results[0].Status != BuildUnsupported || results[1].Status != BuildUnavailable {
		t.Fatalf("wrong availability states: %+v", results)
	}
}

func TestBuildSelectionBounds(t *testing.T) {
	cfg := &BuildConfig{Targets: make(map[string]TargetConfig)}
	for i := range 128 {
		cfg.Targets[fmt.Sprintf("target-%03d", i)] = TargetConfig{Runtime: "go"}
	}
	b := NewUniversalBuilder()
	if results, err := b.Build(t.Context(), cfg, "all"); len(results) != 128 || !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("128 selected targets rejected before inspection: %d, %v", len(results), err)
	}
	cfg.Targets["overflow"] = TargetConfig{Runtime: "go"}
	if results, err := b.Build(t.Context(), cfg, "all"); results != nil || err == nil || errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("oversized selection inspected: %d, %v", len(results), err)
	}
	if results, err := b.Build(t.Context(), cfg, "overflow"); len(results) != 1 || !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("bounded single selection failed: %d, %v", len(results), err)
	}
}

func TestBuildInputAndCancellationErrorsHaveNoResults(t *testing.T) {
	b := NewUniversalBuilder()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if results, err := b.Build(ctx, &BuildConfig{}, "all"); results != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %+v, %v", results, err)
	}
	for _, cfg := range []*BuildConfig{nil, {}, {Targets: map[string]TargetConfig{}}} {
		if results, err := b.Build(t.Context(), cfg, "all"); results != nil || err == nil {
			t.Fatalf("invalid config accepted: %+v, %v", results, err)
		}
	}
}

func TestBuildDoesNotTouchLinkedOutput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "preserve")
	if err := os.WriteFile(file, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "output")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	cfg := &BuildConfig{OutputDir: link, Targets: map[string]TargetConfig{"preserve": {Runtime: "go"}}}
	if _, err := NewUniversalBuilder().Build(t.Context(), cfg, "all"); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("expected backend rejection before output access: %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("existing output changed: %q, %v", data, err)
	}
}
