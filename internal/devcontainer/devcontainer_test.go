package devcontainer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// TestSynthesize_3D verifies Synthesize against positive, negative, and boundary inputs.
func TestSynthesize_3D(t *testing.T) {
	// 1. Positive Test
	manifest := &config.Manifest{
		Version: 1,
		Repository: config.RepositoryMetadata{
			Owner: "cordanaLLM",
			Name:  "praetor",
		},
		Profiles: []string{"framework"},
		Facets:   []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed"},
	}

	dc, err := Synthesize(manifest)
	if err != nil {
		t.Fatalf("expected nil error on positive synthesis, got: %v", err)
	}
	if dc.Name != "cordanaLLM/praetor" {
		t.Fatalf("expected name cordanaLLM/praetor, got: %s", dc.Name)
	}
	if dc.RemoteUser != "vscode" {
		t.Fatalf("expected remoteUser vscode, got: %s", dc.RemoteUser)
	}
	if !strings.Contains(dc.PostCreateCommand, "standardsctl compile-context") {
		t.Fatalf("expected postCreateCommand to contain compile-context, got: %s", dc.PostCreateCommand)
	}

	// 2. Negative Test: Nil manifest
	nilDC, err := Synthesize(nil)
	if err == nil {
		t.Fatalf("expected error on nil manifest, got nil")
	}
	if nilDC != nil {
		t.Fatalf("expected nil devcontainer on nil manifest, got: %v", nilDC)
	}

	// 3. Boundary Test: Empty manifest with no repository metadata or profiles
	emptyManifest := &config.Manifest{
		Version:    1,
		Repository: config.RepositoryMetadata{},
		Profiles:   []string{},
		Facets:     []string{},
	}
	boundaryDC, err := Synthesize(emptyManifest)
	if err != nil {
		t.Fatalf("expected nil error on empty manifest boundary, got: %v", err)
	}
	if boundaryDC.Name != "workspace" {
		t.Fatalf("expected fallback name workspace, got: %s", boundaryDC.Name)
	}
	if boundaryDC.PostCreateCommand != "make verify-all" {
		t.Fatalf("expected default postCreateCommand make verify-all, got: %s", boundaryDC.PostCreateCommand)
	}
}

// TestSynthesizeFromProfiles_3D verifies SynthesizeFromProfiles against positive, negative, and boundary inputs.
func TestSynthesizeFromProfiles_3D(t *testing.T) {
	// 1. Positive Test
	dc, err := SynthesizeFromProfiles("test-service", []string{"service"}, []string{"security:high"})
	if err != nil {
		t.Fatalf("expected nil error on positive synthesis, got: %v", err)
	}
	if dc.Name != "test-service" {
		t.Fatalf("expected name test-service, got: %s", dc.Name)
	}
	if _, ok := dc.Features[CommonUtilsFeature]; !ok {
		t.Fatalf("expected security:high to add %s feature", CommonUtilsFeature)
	}
	if dc.Customizations == nil || dc.Customizations.VSCode == nil {
		t.Fatalf("expected VSCode customizations to be initialized")
	}

	// 2. Negative Test: Unknown profile and facet
	unknownDC, err := SynthesizeFromProfiles("", []string{"nonexistent-profile"}, []string{"unknown:facet"})
	if err != nil {
		t.Fatalf("expected nil error with unknown profile/facet fallback, got: %v", err)
	}
	if unknownDC.Name != "workspace" {
		t.Fatalf("expected fallback name workspace, got: %s", unknownDC.Name)
	}
	if unknownDC.PostCreateCommand != "make verify-all" {
		t.Fatalf("expected fallback postCreateCommand make verify-all, got: %s", unknownDC.PostCreateCommand)
	}

	// 3. Boundary Test: Duplicate facets and whitespace trimming
	boundaryDC, err := SynthesizeFromProfiles("  spaced-name  ", []string{"framework", "framework"}, []string{"security:high", "security:high", "  ", ""})
	if err != nil {
		t.Fatalf("expected nil error on boundary synthesis, got: %v", err)
	}
	if boundaryDC.Name != "spaced-name" {
		t.Fatalf("expected trimmed name spaced-name, got: %s", boundaryDC.Name)
	}
	exts := boundaryDC.Customizations.VSCode.Extensions
	// Assert deduplication
	seen := make(map[string]int)
	for _, ext := range exts {
		seen[ext]++
		if seen[ext] > 1 {
			t.Fatalf("expected deduplicated extensions, found duplicate: %s", ext)
		}
	}
	if len(exts) == 0 {
		t.Fatalf("expected non-empty extensions list")
	}
}

// TestRender_3D verifies Render against positive, negative, and boundary inputs.
func TestRender_3D(t *testing.T) {
	// 1. Positive Test
	dc := &DevContainer{
		Name: "render-test",
		Build: &BuildConfig{
			Dockerfile: DefaultDockerfilePath,
			Context:    DefaultContextDir,
		},
		RemoteUser:        DefaultRemoteUser,
		PostCreateCommand: "make verify-all",
	}
	data, err := Render(dc)
	if err != nil {
		t.Fatalf("expected nil error rendering devcontainer, got: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("expected non-empty rendered bytes")
	}
	if !strings.Contains(string(data), `"name": "render-test"`) {
		t.Fatalf("expected rendered output to contain name field")
	}

	// 2. Negative Test: Nil devcontainer configuration
	nilData, err := Render(nil)
	if err == nil {
		t.Fatalf("expected error rendering nil configuration, got nil")
	}
	if nilData != nil {
		t.Fatalf("expected nil data rendering nil configuration, got: %v", nilData)
	}

	// 3. Boundary Test: Minimal empty devcontainer
	emptyDC := &DevContainer{}
	emptyData, err := Render(emptyDC)
	if err != nil {
		t.Fatalf("expected nil error rendering empty container, got: %v", err)
	}
	if !strings.Contains(string(emptyData), "{\n") {
		t.Fatalf("expected valid json object structure for empty devcontainer")
	}
	if !strings.HasSuffix(string(emptyData), "\n") {
		t.Fatalf("expected trailing newline on rendered json")
	}
}

// TestWriteAndLoadDevContainer_3D verifies WriteDevContainer and LoadDevContainer.
func TestWriteAndLoadDevContainer_3D(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, ".devcontainer", "devcontainer.json")

	dc := &DevContainer{
		Name:              "write-load-test",
		RemoteUser:        "vscode",
		PostCreateCommand: "make verify-all",
	}

	// 1. Positive Test: Write and load back
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := WriteDevContainer(ctx, filePath, dc); err != nil {
		t.Fatalf("expected nil error writing devcontainer, got: %v", err)
	}

	loaded, err := LoadDevContainer(ctx, filePath)
	if err != nil {
		t.Fatalf("expected nil error loading devcontainer, got: %v", err)
	}
	if loaded.Name != dc.Name {
		t.Fatalf("expected name %s, got: %s", dc.Name, loaded.Name)
	}
	if loaded.RemoteUser != dc.RemoteUser {
		t.Fatalf("expected remoteUser %s, got: %s", dc.RemoteUser, loaded.RemoteUser)
	}

	// 2. Negative Test: Nil context or cancelled context
	cancCtx, canc := context.WithCancel(context.Background())
	canc() // Cancel immediately

	errWrite := WriteDevContainer(cancCtx, filePath, dc)
	if errWrite == nil {
		t.Fatalf("expected error writing with cancelled context, got nil")
	}
	_, errLoad := LoadDevContainer(cancCtx, filePath)
	if errLoad == nil {
		t.Fatalf("expected error loading with cancelled context, got nil")
	}

	// 3. Boundary Test: Load non-existent and unparseable file
	_, errMissing := LoadDevContainer(ctx, filepath.Join(tmpDir, "missing.json"))
	if errMissing == nil {
		t.Fatalf("expected error loading missing file, got nil")
	}

	badFile := filepath.Join(tmpDir, "corrupted.json")
	if err := os.WriteFile(badFile, []byte("{corrupted-json-content"), 0644); err != nil {
		t.Fatalf("failed to write corrupted file: %v", err)
	}
	_, errCorrupt := LoadDevContainer(ctx, badFile)
	if errCorrupt == nil {
		t.Fatalf("expected error unmarshaling corrupted json, got nil")
	}
}

// TestVerify_3D validates the Verify interface against positive, negative, and boundary scenarios.
func TestVerify_3D(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "devcontainer.json")

	dc := &DevContainer{
		Name:              "verify-test",
		RemoteUser:        "vscode",
		PostCreateCommand: "make verify-all",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := WriteDevContainer(ctx, filePath, dc); err != nil {
		t.Fatalf("failed to write test devcontainer: %v", err)
	}

	// 1. Positive Test: Matching configuration passes cleanly
	if err := Verify(ctx, filePath, dc); err != nil {
		t.Fatalf("expected verification to pass, got: %v", err)
	}
	// Second assertion
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("expected file to exist after verification")
	}

	// 2. Negative Test: Mismatched configuration fails
	mismatchedDC := &DevContainer{
		Name:              "different-name",
		RemoteUser:        "root",
		PostCreateCommand: "echo bypass",
	}
	errMismatch := Verify(ctx, filePath, mismatchedDC)
	if errMismatch == nil {
		t.Fatalf("expected verification failure for mismatched configuration, got nil")
	}
	if !strings.Contains(errMismatch.Error(), "does not match") {
		t.Fatalf("expected mismatch error message, got: %v", errMismatch)
	}

	// 3. Boundary Test: Non-existent file verification
	missingPath := filepath.Join(tmpDir, "does-not-exist.json")
	errMissing := Verify(ctx, missingPath, dc)
	if errMissing == nil {
		t.Fatalf("expected verification error for missing file, got nil")
	}
	if !strings.Contains(errMissing.Error(), "failed to read") {
		t.Fatalf("expected read failure error message, got: %v", errMissing)
	}
}

// TestDogfoodingSynthesis validates synthesis against canonical repository manifest.
func TestDogfoodingSynthesis(t *testing.T) {
	manifest, err := config.LoadManifest("../../.standards.yaml")
	if err != nil {
		t.Fatalf("failed to load root manifest: %v", err)
	}

	dc, err := Synthesize(manifest)
	if err != nil {
		t.Fatalf("failed to synthesize devcontainer from root manifest: %v", err)
	}

	expectedName := manifest.Repository.Owner + "/" + manifest.Repository.Name
	if dc.Name != expectedName {
		t.Fatalf("expected name %s, got: %s", expectedName, dc.Name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := Verify(ctx, "../../.devcontainer/devcontainer.json", dc); err != nil {
		t.Fatalf("verification of dogfooding .devcontainer/devcontainer.json failed: %v", err)
	}
}

func TestWriteDevContainerRejectsLinkedParentsWithoutWriting(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "linked", "nested", "devcontainer.json")
	if err := WriteDevContainer(t.Context(), target, &DevContainer{Name: "fixture"}); err == nil {
		t.Fatal("linked parent accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("write created directory through external link")
	}
}
