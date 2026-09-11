package needs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNodeAnalyzer(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	pkgJSON := `{
		"name": "test-svelte-app",
		"dependencies": {
			"svelte": "^5.0.0",
			"@sveltejs/kit": "^2.0.0",
			"tailwindcss": "^3.4.0",
			"lucide-svelte": "^0.400.0",
			"bits-ui": "^0.21.0",
			"zod": "^3.23.0"
		},
		"devDependencies": {
			"vitest": "^1.6.0"
		}
	}`

	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}

	analyzer := NewNodeAnalyzer()
	if !analyzer.Detect(tempDir) {
		t.Fatal("expected NodeAnalyzer to detect package.json")
	}

	needs, err := analyzer.Analyze(ctx, tempDir)
	if err != nil {
		t.Fatalf("node analysis failed: %v", err)
	}

	if needs.Repository != "test-svelte-app" {
		t.Errorf("expected repo name test-svelte-app, got %s", needs.Repository)
	}
	if needs.Language != "typescript" {
		t.Errorf("expected language typescript, got %s", needs.Language)
	}

	foundCovered := 0
	for _, dep := range needs.Dependencies {
		if dep.Status == StatusCovered {
			foundCovered++
		}
	}
	if foundCovered < 5 {
		t.Errorf("expected at least 5 covered dependencies, got %d", foundCovered)
	}
}

func TestPythonAnalyzer(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	reqs := `
# Production requirements
fastapi==0.111.0
pydantic>=2.7.0
httpx~=0.27.0
sqlalchemy>=2.0.0
unknown-ml-lib==1.0.0
`
	if err := os.WriteFile(filepath.Join(tempDir, "requirements.txt"), []byte(reqs), 0644); err != nil {
		t.Fatalf("failed to write requirements.txt: %v", err)
	}

	analyzer := NewPythonAnalyzer()
	if !analyzer.Detect(tempDir) {
		t.Fatal("expected PythonAnalyzer to detect requirements.txt")
	}

	needs, err := analyzer.Analyze(ctx, tempDir)
	if err != nil {
		t.Fatalf("python analysis failed: %v", err)
	}

	if needs.Language != "python" {
		t.Errorf("expected language python, got %s", needs.Language)
	}

	hasGap := false
	for _, dep := range needs.Dependencies {
		if dep.Package == "unknown-ml-lib" && dep.Status == StatusGap {
			hasGap = true
		}
	}
	if !hasGap {
		t.Error("expected unknown-ml-lib to be classified as StatusGap")
	}
}

func TestRustAnalyzer(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	cargoToml := `[package]
name = "rust-compute"
version = "0.1.0"
edition = "2024"

[dependencies]
tokio = "1.38"
serde = "1.0"
axum = "0.7"
clap = "4.5"
custom-crate = "0.2"
`
	if err := os.WriteFile(filepath.Join(tempDir, "Cargo.toml"), []byte(cargoToml), 0644); err != nil {
		t.Fatalf("failed to write Cargo.toml: %v", err)
	}

	analyzer := NewRustAnalyzer()
	if !analyzer.Detect(tempDir) {
		t.Fatal("expected RustAnalyzer to detect Cargo.toml")
	}

	needs, err := analyzer.Analyze(ctx, tempDir)
	if err != nil {
		t.Fatalf("rust analysis failed: %v", err)
	}

	if needs.Language != "rust" {
		t.Errorf("expected language rust, got %s", needs.Language)
	}

	foundCovered := 0
	for _, dep := range needs.Dependencies {
		if dep.Status == StatusCovered {
			foundCovered++
		}
	}
	if foundCovered < 4 {
		t.Errorf("expected at least 4 covered crates, got %d", foundCovered)
	}
}

func TestNativeAnalyzer(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	meson := `project('vmafx-accelerator', 'c', 'cpp',
  version: '1.0.0',
  default_options: ['c_std=c23', 'cpp_std=c++20'])

dep_libavcodec = dependency('libavcodec')
dep_libvmaf = dependency('libvmaf')
dep_cuda = dependency('cuda')
dep_custom = dependency('custom_dsp')
`
	if err := os.WriteFile(filepath.Join(tempDir, "meson.build"), []byte(meson), 0644); err != nil {
		t.Fatalf("failed to write meson.build: %v", err)
	}

	analyzer := NewNativeAnalyzer()
	if !analyzer.Detect(tempDir) {
		t.Fatal("expected NativeAnalyzer to detect meson.build")
	}

	needs, err := analyzer.Analyze(ctx, tempDir)
	if err != nil {
		t.Fatalf("native analysis failed: %v", err)
	}

	if needs.Language != "native" {
		t.Errorf("expected language native, got %s", needs.Language)
	}

	foundVmafx := false
	foundCuda := false
	for _, dep := range needs.Dependencies {
		if dep.Package == "libvmaf" && dep.Status == StatusCovered {
			foundVmafx = true
		}
		if dep.Package == "cuda" && dep.Status == StatusCovered {
			foundCuda = true
		}
	}
	if !foundVmafx || !foundCuda {
		t.Errorf("expected libvmaf and cuda covered, got vmafx=%v, cuda=%v", foundVmafx, foundCuda)
	}
}

func TestPolyglotRegistryComposite(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// Multi-language repository: Go + Svelte
	goMod := `module github.com/test/polyglot-app
go 1.27
require (
	github.com/jackc/pgx/v5 v5.5.0
)
`
	pkgJSON := `{
		"name": "polyglot-frontend",
		"dependencies": {
			"svelte": "^5.0.0",
			"tailwindcss": "^3.4.0"
		}
	}`

	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	needs, err := DefaultRegistry().AnalyzePolyglot(ctx, tempDir)
	if err != nil {
		t.Fatalf("polyglot analysis failed: %v", err)
	}

	if len(needs.Languages) < 2 {
		t.Errorf("expected multiple languages detected, got %v", needs.Languages)
	}
	if len(needs.Dependencies) < 3 {
		t.Errorf("expected composite dependencies across Go and Svelte, got %d", len(needs.Dependencies))
	}
}
