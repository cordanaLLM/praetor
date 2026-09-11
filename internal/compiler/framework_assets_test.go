package compiler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileFrameworkAssets_Sveltesentio(t *testing.T) {
	tmpDir := t.TempDir()
	kit := &FrameworkKitConfig{
		KitName:     "sveltesentio",
		Language:    "svelte",
		Version:     "5.0.0",
		Description: "Universal Svelte 5 frontend component library and state harness",
		Rules: []string{
			"Use Svelte 5 runes exclusively",
			"Purge unused CSS in production builds",
		},
		Skills:     []string{"modern-web-guidance", "a11y-debugging"},
		Components: []string{"Button", "Modal", "Card"},
	}

	outDir := filepath.Join(tmpDir, "sveltesentio-out")
	res, err := CompileFrameworkAssets(context.Background(), kit, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	llmsData, err := os.ReadFile(res.LLMsTxtPath)
	if err != nil || !strings.Contains(string(llmsData), "sveltesentio") {
		t.Errorf("llms.txt missing expected content: %s", string(llmsData))
	}

	ruleData, err := os.ReadFile(res.AgentRulePath)
	if err != nil || !strings.Contains(string(ruleData), "modern-web-guidance") {
		t.Errorf("agent rule missing expected skill: %s", string(ruleData))
	}
}

func TestCompileFrameworkAssets_NativeGPU(t *testing.T) {
	tmpDir := t.TempDir()
	kit := &FrameworkKitConfig{
		KitName:     "template-native-gpu",
		Language:    "c",
		Version:     "1.0.0",
		Description: "High-performance C23/C++20 GPU compute kernels with Vulkan and CUDA",
		Rules: []string{
			"Enforce ASan and UBSan in test configurations",
			"Target C23 standards with no compiler warnings",
		},
	}

	outDir := filepath.Join(tmpDir, "native-gpu-out")
	res, err := CompileFrameworkAssets(context.Background(), kit, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fullData, err := os.ReadFile(res.LLMsFullTxtPath)
	if err != nil || !strings.Contains(string(fullData), "ASan and UBSan") {
		t.Errorf("llms-full.txt missing rules: %s", string(fullData))
	}
}

func TestCompileFrameworkAssets_Negative(t *testing.T) {
	tmpDir := t.TempDir()
	kit := &FrameworkKitConfig{KitName: "test"}
	outDir := filepath.Join(tmpDir, "neg-out")

	if _, err := CompileFrameworkAssets(nil, kit, outDir); err == nil {
		t.Error("expected error for nil context, got nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CompileFrameworkAssets(ctx, kit, outDir); err == nil {
		t.Error("expected error for canceled context, got nil")
	}

	if _, err := CompileFrameworkAssets(context.Background(), nil, outDir); err == nil {
		t.Error("expected error for nil config, got nil")
	}

	emptyKit := &FrameworkKitConfig{KitName: ""}
	if _, err := CompileFrameworkAssets(context.Background(), emptyKit, outDir); err == nil {
		t.Error("expected error for empty kit name, got nil")
	}
}

func TestCompileFrameworkAssets_Boundary(t *testing.T) {
	tmpDir := t.TempDir()
	kit := &FrameworkKitConfig{KitName: "minimal-kit", Language: "go"}
	outDir := filepath.Join(tmpDir, "minimal-out")

	res, err := CompileFrameworkAssets(context.Background(), kit, outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.KitName != "minimal-kit" {
		t.Errorf("expected kit name minimal-kit, got %s", res.KitName)
	}
}
