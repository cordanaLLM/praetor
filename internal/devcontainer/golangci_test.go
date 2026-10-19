// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func assertContains(t *testing.T, text, sub, desc string) {
	t.Helper()
	if !strings.Contains(text, sub) {
		t.Fatalf("%s missing %q", desc, sub)
	}
}

// Positive: generated bundle for a Go archetype contains the pinned golangci-lint install step
// with the tools/lint/go.mod version.
func TestGolangciLintPinnedInstallPositive(t *testing.T) {
	root := bootstrapSourceFixture(t)
	wantVersion, err := readPinnedGolangciLint(root)
	if err != nil {
		t.Fatalf("read pinned golangci-lint: %v", err)
	}

	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{SourceRoot: root})
	if err != nil {
		t.Fatalf("PrepareBundle failed: %v", err)
	}
	spec := bundle.Spec()
	if spec == nil || spec.GolangciLintVersion != wantVersion {
		t.Fatalf("spec.GolangciLintVersion = %v, want %q", spec, wantVersion)
	}

	dockerfile := findArtifact(bundle.Artifacts, bootstrapDockerfile)
	if dockerfile == nil {
		t.Fatal("bootstrap Dockerfile not found in bundle artifacts")
	}
	dockerText := string(dockerfile.Content)
	wantInstall := "RUN GOBIN=/out /usr/local/go/bin/go install -trimpath " + golangciModulePath + "/cmd/golangci-lint@" + wantVersion
	assertContains(t, dockerText, wantInstall, "Dockerfile")
	assertContains(t, dockerText, "COPY --from=praetor_build --chmod=0555 /out/golangci-lint /usr/local/bin/golangci-lint", "Dockerfile")
}

// Positive: with installGoTools: false in the feature options, the pinned install step is still present.
func TestGolangciLintInstallGoToolsFalseStillInstalls(t *testing.T) {
	root := bootstrapSourceFixture(t)
	wantVersion, err := readPinnedGolangciLint(root)
	if err != nil {
		t.Fatalf("read pinned golangci-lint: %v", err)
	}

	features := []config.DevContainerFeature{
		{
			Ref:     GoFeatureRef,
			Options: map[string]interface{}{"installGoTools": false},
		},
	}
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{
		SourceRoot: root,
		Features:   features,
	})
	if err != nil {
		t.Fatalf("PrepareBundle with installGoTools:false failed: %v", err)
	}
	dockerfile := findArtifact(bundle.Artifacts, bootstrapDockerfile)
	if dockerfile == nil {
		t.Fatal("bootstrap Dockerfile not found in bundle artifacts")
	}
	dockerText := string(dockerfile.Content)
	wantInstall := "RUN GOBIN=/out /usr/local/go/bin/go install -trimpath " + golangciModulePath + "/cmd/golangci-lint@" + wantVersion
	assertContains(t, dockerText, wantInstall, "Dockerfile with installGoTools:false")
	assertContains(t, dockerText, "COPY --from=praetor_build --chmod=0555 /out/golangci-lint /usr/local/bin/golangci-lint", "Dockerfile with installGoTools:false")
}

// Negative (planted): a bundle whose settings name golangci-lint but whose Dockerfile installs none
// is refused; an install step with an unpinned version (latest) is refused.
func TestGolangciLintMissingOrUnpinnedRefused(t *testing.T) {
	root := bootstrapSourceFixture(t)
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{SourceRoot: root})
	if err != nil {
		t.Fatalf("PrepareBundle failed: %v", err)
	}

	dc := bundle.Config
	dockerfile := findArtifact(bundle.Artifacts, bootstrapDockerfile)
	if dockerfile == nil {
		t.Fatal("bootstrap Dockerfile not found")
	}

	// 1. Planted missing install step: settings name golangci-lint, but Dockerfile has no golangci-lint
	strippedDocker := strings.ReplaceAll(string(dockerfile.Content), "golangci-lint", "other-tool")
	err = verifyLintToolInstall(dc, []byte(strippedDocker), bundle.Spec())
	if err == nil || !strings.Contains(err.Error(), "does not install golangci-lint") {
		t.Fatalf("verifyLintToolInstall accepted missing install step: %v", err)
	}

	// 2. Planted unpinned latest in Dockerfile
	unpinnedDocker := strings.ReplaceAll(string(dockerfile.Content), bundle.Spec().GolangciLintVersion, "latest")
	err = verifyLintToolInstall(dc, []byte(unpinnedDocker), bundle.Spec())
	if err == nil || !strings.Contains(err.Error(), "unpinned golangci-lint@latest") {
		t.Fatalf("verifyLintToolInstall accepted unpinned @latest: %v", err)
	}

	// 3. Planted unpinned latest in spec
	tamperedSpec := *bundle.Spec()
	tamperedSpec.GolangciLintVersion = "latest"
	err = validateBootstrapSpecFrom(&tamperedSpec, dockerfileCurrent)
	if err == nil || !strings.Contains(err.Error(), "unpinned or invalid golangci-lint version") {
		t.Fatalf("validateBootstrapSpecFrom accepted spec with latest: %v", err)
	}
}

// Boundary: a non-Go archetype (no Go toolchain) gets no lint step and no go.lintTool setting.
func TestGolangciLintBoundaryNonGoArchetype(t *testing.T) {
	root := bootstrapSourceFixture(t)
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{NativeGPUProfile}, nil, BootstrapOptions{SourceRoot: root})
	if err != nil {
		t.Fatalf("PrepareBundle failed: %v", err)
	}

	if hasGoLintTool(bundle.Config) {
		t.Fatal("non-Go archetype unexpectedly configured go.lintTool")
	}
	if bundle.Spec() != nil && bundle.Spec().GolangciLintVersion != "" {
		t.Fatalf("non-Go archetype set GolangciLintVersion: %q", bundle.Spec().GolangciLintVersion)
	}

	dockerfile := findArtifact(bundle.Artifacts, bootstrapDockerfile)
	if dockerfile == nil {
		t.Fatal("bootstrap Dockerfile not found in bundle artifacts")
	}
	if strings.Contains(string(dockerfile.Content), "golangci-lint") {
		t.Fatal("non-Go archetype Dockerfile unexpectedly installed golangci-lint")
	}
}

func findArtifact(artifacts []BootstrapArtifact, name string) *BootstrapArtifact {
	for i := 0; i < len(artifacts) && i < MaxLoopLimit; i++ {
		if artifacts[i].Name == name {
			return &artifacts[i]
		}
	}
	return nil
}
