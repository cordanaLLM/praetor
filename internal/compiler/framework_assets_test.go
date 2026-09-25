package compiler

import (
	"context"
	"errors"
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

	var absentContext context.Context
	if _, err := CompileFrameworkAssets(absentContext, kit, outDir); err == nil {
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

// TestCompileFrameworkAssets_RejectsUnsafeKitNameBeforeWriting pins BUG-618: kit_name
// names the agent rule file, so a name that is not one file-name component is refused
// before the output directory is even created.
func TestCompileFrameworkAssets_RejectsUnsafeKitNameBeforeWriting(t *testing.T) {
	names := []string{"../x", "..", "a/b", `a\b`, ".hidden", "-flag", "kit name", strings.Repeat("k", 65)}
	for _, name := range names {
		outDir := filepath.Join(t.TempDir(), "out")
		_, err := CompileFrameworkAssets(context.Background(), &FrameworkKitConfig{KitName: name}, outDir)
		if !errors.Is(err, ErrInvalidKitName) {
			t.Errorf("kit_name %q: error = %v, want ErrInvalidKitName", name, err)
		}
		if _, statErr := os.Stat(outDir); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("kit_name %q: output dir exists after refusal (stat err %v)", name, statErr)
		}
	}
}

// TestCompileFrameworkAssets_WritesEveryAssetUnderOutputDir checks the whole asset set
// lands under outputDir, with the rule file named after the kit.
func TestCompileFrameworkAssets_WritesEveryAssetUnderOutputDir(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "kit-out")
	res, err := CompileFrameworkAssets(context.Background(), &FrameworkKitConfig{KitName: "pykit", Language: "python"}, outDir)
	if err != nil {
		t.Fatalf("CompileFrameworkAssets() error = %v", err)
	}
	want := []string{
		filepath.Join(outDir, "llms.txt"),
		filepath.Join(outDir, "llms-full.txt"),
		filepath.Join(outDir, ".agents", "rules", "pykit.md"),
		filepath.Join(outDir, "templates", ".framework-build.yaml"),
		filepath.Join(outDir, "templates", "README.md"),
	}
	got := []string{res.LLMsTxtPath, res.LLMsFullTxtPath, res.AgentRulePath,
		res.Templates[".framework-build.yaml"], res.Templates["README.md"]}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("asset %d path = %q, want %q", i, got[i], want[i])
		}
		if _, err := os.Stat(want[i]); err != nil {
			t.Errorf("asset %s not written: %v", want[i], err)
		}
	}
}

// TestCompileFrameworkAssets_KitNameBoundaries accepts the longest allowed name and a
// name with inner dots, both of which stay one component inside the rules directory.
func TestCompileFrameworkAssets_KitNameBoundaries(t *testing.T) {
	for _, name := range []string{strings.Repeat("k", 64), "a..b", "kit.v2_x-y", "9"} {
		outDir := filepath.Join(t.TempDir(), "out")
		res, err := CompileFrameworkAssets(context.Background(), &FrameworkKitConfig{KitName: name}, outDir)
		if err != nil {
			t.Fatalf("kit_name %q: unexpected error %v", name, err)
		}
		if want := filepath.Join(outDir, ".agents", "rules", name+".md"); res.AgentRulePath != want {
			t.Errorf("kit_name %q: rule path %q, want %q", name, res.AgentRulePath, want)
		}
	}
}

// TestCompileFrameworkAssets_RefusesRuleSymlinkOutsideRulesDir covers the ConfinePath
// guard: a pre-planted rule file that links outside the rules directory is not followed.
func TestCompileFrameworkAssets_RefusesRuleSymlinkOutsideRulesDir(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "out")
	rulesDir := filepath.Join(outDir, ".agents", "rules")
	if err := os.MkdirAll(rulesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside.md")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(rulesDir, "kit.md")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if _, err := CompileFrameworkAssets(context.Background(), &FrameworkKitConfig{KitName: "kit"}, outDir); err == nil {
		t.Fatal("CompileFrameworkAssets() followed a rule symlink outside the rules directory")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep\n" {
		t.Fatalf("symlink target was rewritten: %q, %v", data, err)
	}
}

func TestLoadFrameworkKitConfig_ReadsDeclaredKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kit.yaml")
	body := "kit_name: rustkit\nlanguage: rust\nversion: 0.1.0\ndescription: Rust kit\nrules: [No unsafe]\nskills: [clippy]\ncomponents: [core]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	kit, err := LoadFrameworkKitConfig(path)
	if err != nil {
		t.Fatalf("LoadFrameworkKitConfig() error = %v", err)
	}
	if kit.KitName != "rustkit" || kit.Language != "rust" || len(kit.Rules) != 1 || kit.Components[0] != "core" {
		t.Fatalf("LoadFrameworkKitConfig() = %+v", kit)
	}
}

func TestLoadFrameworkKitConfig_RefusesBadInput(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"unknown.yaml":  "kit_name: kit\nkitname: typo\n",
		"escape.yaml":   "kit_name: ../escape\n",
		"empty.yaml":    "",
		"twodocs.yaml":  "kit_name: a\n---\nkit_name: b\n",
		"noname.yaml":   "language: go\n",
		"oversize.yaml": "kit_name: kit\ndescription: " + strings.Repeat("x", maxKitConfigBytes) + "\n",
	}
	for name, body := range cases {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFrameworkKitConfig(path); err == nil {
			t.Errorf("%s: LoadFrameworkKitConfig() accepted bad input", name)
		}
	}
	for _, path := range []string{"", "   ", filepath.Join(dir, "missing.yaml")} {
		if _, err := LoadFrameworkKitConfig(path); err == nil {
			t.Errorf("LoadFrameworkKitConfig(%q) accepted a missing config", path)
		}
	}
}
