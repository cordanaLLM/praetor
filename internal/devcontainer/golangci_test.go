// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"path/filepath"
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

// Negative: a source root lacking tools/lint/go.mod fails immediately, naming the path,
// and never falls back to the process working directory.
func TestGolangciLintMissingModFailsNamingPath(t *testing.T) {
	emptyRoot := t.TempDir()
	_, err := readPinnedGolangciLint(emptyRoot)
	if err == nil {
		t.Fatal("readPinnedGolangciLint unexpectedly succeeded without tools/lint/go.mod in source root")
	}
	wantPath := filepath.Join(emptyRoot, "tools/lint/go.mod")
	if !strings.Contains(err.Error(), wantPath) {
		t.Fatalf("error %q does not name missing path %q", err.Error(), wantPath)
	}
}

// Positive: a recorded bundle matching an earlier rendering (dockerfileUnpiped) without the
// lint step verifies as before without special-casing its digest list.
func TestEarlierRenderingWithoutLintStepVerifiesAsBefore(t *testing.T) {
	root := bootstrapSourceFixture(t)
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{SourceRoot: root})
	if err != nil {
		t.Fatalf("PrepareBundle failed: %v", err)
	}
	// Simulate an earlier recorded bundle: rendered with dockerfileUnpiped, no GolangciLintVersion in spec.
	spec := *bundle.Spec()
	spec.GolangciLintVersion = ""
	spec.ArchiveSHA256 = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	unpipedDocker := renderBootstrapDockerfileAs(&spec, dockerfileUnpiped)
	spec.DockerfileSHA256 = bootstrapDigest([]byte(unpipedDocker))

	err = verifyLintToolInstall(bundle.Config, []byte(unpipedDocker), &spec)
	if err != nil {
		t.Fatalf("verifyLintToolInstall refused earlier rendering: %v", err)
	}
}

// Negative (planted): verifyLintToolInstall requires the exact pinned install line rendered for
// spec.GolangciLintVersion and the copy line into /usr/local/bin. A Dockerfile that contains
// the tool words but another version, or that lacks the copy line, must be refused.
func TestGolangciLintPlantedDockerfileRefused(t *testing.T) {
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

	// 1. Planted wrong version in Dockerfile (still contains "golangci-lint" and "/usr/local/bin/golangci-lint")
	wrongVersionDocker := strings.ReplaceAll(string(dockerfile.Content), bundle.Spec().GolangciLintVersion, "v1.99.0")
	err = verifyLintToolInstall(dc, []byte(wrongVersionDocker), bundle.Spec())
	if err == nil || !strings.Contains(err.Error(), "does not install golangci-lint") {
		t.Fatalf("verifyLintToolInstall accepted planted wrong version: %v", err)
	}

	// 2. Planted missing copy line (remove COPY line, but keep "/usr/local/bin/golangci-lint" in comment)
	noCopyDocker := strings.ReplaceAll(string(dockerfile.Content),
		"COPY --from=praetor_build --chmod=0555 /out/golangci-lint /usr/local/bin/golangci-lint",
		"# comment mentions /usr/local/bin/golangci-lint but does not copy")
	err = verifyLintToolInstall(dc, []byte(noCopyDocker), bundle.Spec())
	if err == nil || !strings.Contains(err.Error(), "does not install golangci-lint") {
		t.Fatalf("verifyLintToolInstall accepted planted missing copy line: %v", err)
	}
}
