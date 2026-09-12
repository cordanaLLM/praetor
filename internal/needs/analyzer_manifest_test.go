package needs

import (
	"context"
	"testing"
)

func TestPythonAnalyzerManifestSyntaxForms(t *testing.T) {
	tempDir := t.TempDir()
	pyproject := "[project]\nname = \"svc\"\n" +
		"dependencies = [\"fastapi>=0.111\", \"pydantic>=2\"]\n\n" +
		"[project.optional-dependencies]\ndev = [\"httpx~=0.27\"]\n\n" +
		"[tool.poetry.dependencies]\npython = \"^3.12\"\nredis = \"^5.0\"\n"
	writeFixture(t, tempDir, "pyproject.toml", pyproject)

	repoNeeds, err := NewPythonAnalyzer().Analyze(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("python analysis failed: %v", err)
	}

	found := make(map[string]CapabilityStatus, len(repoNeeds.Dependencies))
	for _, dep := range repoNeeds.Dependencies {
		found[dep.Package] = dep.Status
	}
	for _, want := range []string{"fastapi", "pydantic", "httpx", "redis"} {
		if _, ok := found[want]; !ok {
			t.Errorf("expected %q to be discovered, got %v", want, found)
		}
	}
	if _, ok := found["python"]; ok {
		t.Error("the poetry python constraint must not be reported as a dependency")
	}
	if repoNeeds.Readiness.Score != 100.0 {
		t.Errorf("expected every catalogued dependency to be covered, got %.1f", repoNeeds.Readiness.Score)
	}
}

func TestPythonReqLineDecorations3D(t *testing.T) {
	deps := make(map[string]string)
	// Positive: a fully decorated PEP 508 specifier.
	parsePythonReqLine(`requests[security]>=2.31 ; python_version<"3.12"  # pinned`, deps)
	// Boundary: a bare, capitalised name with no version at all.
	parsePythonReqLine("Click", deps)
	// Negative: an empty specifier contributes nothing.
	parsePythonReqLine("", deps)

	if deps["requests"] != "2.31" {
		t.Errorf("expected extras and markers to be stripped, got %v", deps)
	}
	if _, ok := deps["click"]; !ok {
		t.Errorf("expected a bare, case-folded requirement, got %v", deps)
	}
	if len(deps) != 2 {
		t.Errorf("expected exactly 2 requirements, got %v", deps)
	}
}

func TestPythonAnalyzerSetupPyOnly(t *testing.T) {
	tempDir := t.TempDir()
	setupPy := "from setuptools import setup\n\nsetup(\n    name=\"svc\",\n" +
		"    install_requires=[\n        \"fastapi>=0.111\",\n        \"unknown-ml-lib==1.0.0\",\n    ],\n)\n"
	writeFixture(t, tempDir, "setup.py", setupPy)

	repoNeeds, err := NewPythonAnalyzer().Analyze(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("python analysis failed: %v", err)
	}
	if len(repoNeeds.Dependencies) != 2 {
		t.Fatalf("expected 2 setup.py dependencies, got %+v", repoNeeds.Dependencies)
	}
	if repoNeeds.Readiness.Score != 50.0 {
		t.Errorf("expected 50%% readiness (1 covered, 1 gap), got %.1f", repoNeeds.Readiness.Score)
	}
}

func TestRustAnalyzerTableForms(t *testing.T) {
	tempDir := t.TempDir()
	cargo := "[package]\nname = \"svc\"\n\n" +
		"[workspace.dependencies]\nserde = \"1.0\"\n\n" +
		"[dependencies]\ntokio = { version = \"1.38\", features = [\"full\"] }\n\n" +
		"[dependencies.axum]\nversion = \"0.7\"\n\n" +
		"[dev-dependencies]\nclap = \"4.5\"\n"
	writeFixture(t, tempDir, "Cargo.toml", cargo)

	repoNeeds, err := NewRustAnalyzer().Analyze(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("rust analysis failed: %v", err)
	}

	versions := make(map[string]string, len(repoNeeds.Dependencies))
	for _, dep := range repoNeeds.Dependencies {
		versions[dep.Package] = dep.Version
	}
	for _, want := range []string{"serde", "tokio", "axum", "clap"} {
		if _, ok := versions[want]; !ok {
			t.Errorf("expected crate %q to be discovered, got %v", want, versions)
		}
	}
	if versions["tokio"] != "1.38" {
		t.Errorf("expected the inline table version to be extracted, got %q", versions["tokio"])
	}
	if versions["axum"] != "0.7" {
		t.Errorf("expected the sub-table version to be extracted, got %q", versions["axum"])
	}
}

func TestRustAnalyzerNegativeMissingManifest(t *testing.T) {
	if _, err := parseCargoToml("/nonexistent/Cargo.toml"); err == nil {
		t.Fatal("expected an error for a missing Cargo.toml")
	}
}

func TestNativeAnalyzerCMakeIsCaseInsensitive(t *testing.T) {
	tempDir := t.TempDir()
	cmake := "cmake_minimum_required(VERSION 3.28)\nproject(accel)\n" +
		"find_package(CUDA REQUIRED)\nfind_package(Vulkan REQUIRED)\nfind_package(CustomDsp)\n"
	writeFixture(t, tempDir, "CMakeLists.txt", cmake)

	repoNeeds, err := NewNativeAnalyzer().Analyze(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("native analysis failed: %v", err)
	}

	status := make(map[string]CapabilityStatus, len(repoNeeds.Dependencies))
	for _, dep := range repoNeeds.Dependencies {
		status[dep.Package] = dep.Status
	}
	if status["CUDA"] != StatusCovered {
		t.Errorf("expected find_package(CUDA) to be covered, got %q", status["CUDA"])
	}
	if status["Vulkan"] != StatusCovered {
		t.Errorf("expected find_package(Vulkan) to be covered, got %q", status["Vulkan"])
	}
	if status["CustomDsp"] != StatusGap {
		t.Errorf("expected an unknown package to stay a gap, got %q", status["CustomDsp"])
	}
}

func TestNativeAnalyzerDeduplicatesAcrossManifests(t *testing.T) {
	tempDir := t.TempDir()
	writeFixture(t, tempDir, "meson.build", "dep_cuda = dependency('cuda')\n")
	writeFixture(t, tempDir, "CMakeLists.txt", "find_package(CUDA REQUIRED)\n")

	repoNeeds, err := NewNativeAnalyzer().Analyze(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("native analysis failed: %v", err)
	}
	if len(repoNeeds.Dependencies) != 1 {
		t.Fatalf("expected cuda to be counted once across both manifests, got %+v", repoNeeds.Dependencies)
	}
}
