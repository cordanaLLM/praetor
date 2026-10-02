package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/util"
)

func cliBootstrapSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module github.com/cordanaLLM/praetor\n\ngo 1.27\n", "go.sum": "", "LICENSE": "Synthetic test license\n",
		"cmd/standardsctl/main.go": "package main\nfunc main() {}\n",
	} {
		writeFixtureFile(t, root, name, content)
	}
	if _, err := util.RunCommandBytes(t.Context(), root, "git", 4096, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	return root
}

func cliBootstrapPaths(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	manifest := writeFixtureFile(t, root, ".standards.yaml", "version: 1\nrepository:\n  owner: adopted\n  name: app\nprofiles: []\nfacets: []\n")
	writeFixtureFile(t, root, ".standards.lock", "version: 1\npinned_version: v1.0.0\ndigest: sha256:01ba4719c80b6fe911b091a7c05124b64eeece964e09c058ef8f9805daca546b\nprofiles: []\nfacets: []\n")
	return manifest, filepath.Join(root, ".devcontainer", "devcontainer.json")
}

func TestDevContainerCLIReadyBundleAndCustomPreservation(t *testing.T) {
	manifest, output := cliBootstrapPaths(t)
	args := []string{"generate", "--config", manifest, "--output", output, "--source-root", cliBootstrapSource(t)}
	message, err := captureStdout(t, func() error { return runDevContainer(args) })
	if err != nil || !strings.Contains(message, "PREPARED, NOT EXECUTED") {
		t.Fatalf("prepare: %v %s", err, message)
	}
	if err := runDevContainer([]string{"verify", "--config", manifest, "--output", output}); err != nil {
		t.Fatal(err)
	}
	custom := []byte("{\"image\":\"owned:tag\"}\n")
	if err := os.WriteFile(output, custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runDevContainer(args); err == nil {
		t.Fatal("custom container silently overwritten")
	}
	got, err := os.ReadFile(output)
	if err != nil || string(got) != string(custom) {
		t.Fatal("custom container changed")
	}
	if err := runDevContainer(append(args, "--force")); err != nil {
		t.Fatalf("explicit reviewed regeneration: %v", err)
	}
}

func TestDevContainerCLIMissingSourceIsVisibleFailure(t *testing.T) {
	manifest, output := cliBootstrapPaths(t)
	err := runDevContainer([]string{"generate", "--config", manifest, "--output", output})
	if !errors.Is(err, devcontainer.ErrBootstrapUnavailable) {
		t.Fatalf("missing source reported ready: %v", err)
	}
	dc, err := devcontainer.LoadDevContainer(t.Context(), output)
	if err != nil || dc.Image == "" || dc.Build != nil || !strings.Contains(dc.PostCreateCommand, "exit 1") {
		t.Fatalf("unavailable configuration unclear: %#v %v", dc, err)
	}
	if err := runDevContainer([]string{"verify", "--config", manifest, "--output", output}); !errors.Is(err, devcontainer.ErrBootstrapUnavailable) {
		t.Fatalf("unavailable verification succeeded: %v", err)
	}
}

func TestDevContainerCLIRemediationRerunReplacesOwnPlaceholder(t *testing.T) {
	manifest, output := cliBootstrapPaths(t)
	base := []string{"generate", "--config", manifest, "--output", output}
	err := runDevContainer(base)
	if !errors.Is(err, devcontainer.ErrBootstrapUnavailable) || !strings.Contains(err.Error(), "--source-root") {
		t.Fatalf("unavailable generation hid the remediation: %v", err)
	}
	withSource := append(append([]string{}, base...), "--source-root", cliBootstrapSource(t))
	if _, err := captureStdout(t, func() error { return runDevContainer(withSource) }); err != nil {
		t.Fatalf("the advised --source-root rerun needed a hidden --force: %v", err)
	}
	if err := runDevContainer([]string{"verify", "--config", manifest, "--output", output}); err != nil {
		t.Fatal(err)
	}
	ready, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := runDevContainer(append(base, "--force")); err == nil || errors.Is(err, devcontainer.ErrBootstrapUnavailable) {
		t.Fatalf("--force without --source-root replaced a ready bootstrap: %v", err)
	}
	got, err := os.ReadFile(output)
	if err != nil || string(got) != string(ready) {
		t.Fatal("ready bootstrap changed")
	}
	if err := runDevContainer([]string{"verify", "--config", manifest, "--output", output}); err != nil {
		t.Fatalf("refused downgrade stranded the ready bundle: %v", err)
	}
}

func TestDevContainerCLIRejectsInvalidManifestBeforeWriting(t *testing.T) {
	for _, version := range []string{"0", "2"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			manifest := writeFixtureFile(t, root, ".standards.yaml", "version: "+version+"\nprofiles: []\nfacets: []\n")
			output := filepath.Join(root, ".devcontainer", "devcontainer.json")
			if err := runDevContainer([]string{"generate", "--config", manifest, "--output", output}); err == nil {
				t.Fatal("invalid manifest version accepted")
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid manifest wrote output: %v", err)
			}
		})
	}
}

func TestDevContainerCLIUsesPinnedCatalogFeatures(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nrepository:\n  owner: cordanaLLM\n  name: praetor\nprofiles: [framework]\nfacets: [security:high]\n")
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, rel := range []string{".standards.lock", ".config/archetypes/framework.yaml", ".config/archetypes/facets/security-high.yaml", ".config/archetypes/facets/api-public.yaml", ".config/archetypes/facets/docs-seoportal.yaml", ".config/archetypes/facets/agent-sandboxed.yaml"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, root, rel, string(data))
	}
	output := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if err := runDevContainer([]string{"generate", "--config", filepath.Join(root, ".standards.yaml"), "--output", output, "--source-root", cliBootstrapSource(t)}); err != nil {
		t.Fatal(err)
	}
	dc, err := devcontainer.LoadDevContainer(t.Context(), output)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := dc.Features[devcontainer.GoFeatureRef]; !ok {
		t.Fatal("pinned framework feature was not selected")
	}
	if _, ok := dc.Features[devcontainer.CommonUtilsFeature]; !ok {
		t.Fatal("pinned security facet feature was not selected")
	}
}

func TestDevContainerCLIGenerationBoundaries(t *testing.T) {
	for _, args := range [][]string{{"delete"}, {"verify", "--force"}, {"verify", "--source-root", "/selected"}, {"verify", "--base-image", "mutable"}} {
		if _, err := parseDevContainerOptions(args); err == nil {
			t.Fatalf("invalid or ignored argument: %v", args)
		}
	}
	manifest, output := cliBootstrapPaths(t)
	if err := runDevContainer([]string{"generate", "--config", manifest, "--output", output, "--builder-image", "mutable:tag"}); err == nil {
		t.Fatal("mutable builder accepted")
	}
	if _, err := os.Stat(filepath.Dir(output)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid input wrote companions: %v", err)
	}
}

// recordedBaseImage returns the base image the bootstrap specification at output records.
func recordedBaseImage(t *testing.T, output string) string {
	t.Helper()
	dc, err := devcontainer.LoadDevContainer(t.Context(), output)
	if err != nil || dc.Customizations == nil || dc.Customizations.Praetor == nil || dc.Customizations.Praetor.Bootstrap == nil {
		t.Fatalf("no recorded bootstrap at %s: %v", output, err)
	}
	return dc.Customizations.Praetor.Bootstrap.BaseImage
}

// generateCLI runs devcontainer generate with base plus extra and returns its output.
func generateCLI(t *testing.T, base []string, extra ...string) (string, error) {
	t.Helper()
	args := append(append([]string{}, base...), extra...)
	return captureStdout(t, func() error { return runDevContainer(args) })
}

// TestDevContainerCLIForceKeepsRecordedBaseImage pins issue #536: a --force regeneration
// from a new source keeps the adopter's recorded base image and reports it, whether it
// names another repository or another tag of the reviewed default repository; an explicit
// --base-image still replaces it, and a first generation takes the reviewed default.
func TestDevContainerCLIForceKeepsRecordedBaseImage(t *testing.T) {
	for name, adopterBase := range map[string]string{
		"other repository":               "registry.example/team/dev-toolchains@sha256:" + strings.Repeat("c", 64),
		"reviewed repository, other tag": "mcr.microsoft.com/devcontainers/base:debian-12@sha256:" + strings.Repeat("e", 64),
	} {
		t.Run(name, func(t *testing.T) { assertCLIForceKeepsRecordedBase(t, adopterBase) })
	}
}

func assertCLIForceKeepsRecordedBase(t *testing.T, adopterBase string) {
	t.Helper()
	manifest, output := cliBootstrapPaths(t)
	base := []string{"generate", "--config", manifest, "--output", output}
	// Boundary: no recorded specification, so the reviewed default applies silently.
	first, err := generateCLI(t, base, "--source-root", cliBootstrapSource(t))
	if err != nil || strings.Contains(first, "RECORDED IMAGE") || recordedBaseImage(t, output) != devcontainer.DefaultBaseImage {
		t.Fatalf("first generation: %v %q", err, first)
	}
	if _, err := generateCLI(t, base, "--force", "--source-root", cliBootstrapSource(t), "--base-image", adopterBase); err != nil {
		t.Fatal(err)
	}
	// Positive: the vendored source moves on; --force without --base-image keeps the image.
	moved := cliBootstrapSource(t)
	writeFixtureFile(t, moved, "cmd/standardsctl/main.go", "package main\nfunc main() { println() }\n")
	kept, err := generateCLI(t, base, "--force", "--source-root", moved)
	if err != nil || recordedBaseImage(t, output) != adopterBase {
		t.Fatalf("--force replaced the recorded base image: %v %q", err, kept)
	}
	if !strings.Contains(kept, "[RECORDED IMAGE KEPT] base image "+adopterBase+"; reviewed default "+devcontainer.DefaultBaseImage) {
		t.Fatalf("kept image not reported: %q", kept)
	}
	dockerfile, err := os.ReadFile(filepath.Join(filepath.Dir(output), "Dockerfile.praetor"))
	if err != nil || !strings.Contains(string(dockerfile), "\nFROM "+adopterBase+"\n") {
		t.Fatalf("Dockerfile does not build on the recorded base: %v", err)
	}
	if err := runDevContainer([]string{"verify", "--config", manifest, "--output", output}); err != nil {
		t.Fatal(err)
	}
	// Negative: an explicit --base-image overrides the recorded image and says so.
	replaced, err := generateCLI(t, base, "--force", "--source-root", moved, "--base-image", devcontainer.DefaultBaseImage)
	if err != nil || recordedBaseImage(t, output) != devcontainer.DefaultBaseImage {
		t.Fatalf("explicit --base-image ignored: %v %q", err, replaced)
	}
	if !strings.Contains(replaced, "[RECORDED IMAGE REPLACED] base image "+adopterBase+" -> "+devcontainer.DefaultBaseImage+" (--base-image)") {
		t.Fatalf("replacement not reported: %q", replaced)
	}
}

// TestDevContainerCLIDigestOnlyImages pins issue #333 through the command: a first
// generation records the digest-only reviewed defaults and verifies; a bundle recorded with
// their earlier tag@digest form is refreshed to digest-only by a --force regeneration that
// names each refresh; digest-only flags are accepted; tag-only and malformed digests are
// refused before anything is written.
func TestDevContainerCLIDigestOnlyImages(t *testing.T) {
	manifest, output := cliBootstrapPaths(t)
	base := []string{"generate", "--config", manifest, "--output", output, "--source-root", cliBootstrapSource(t)}
	verify := []string{"verify", "--config", manifest, "--output", output}
	// Positive: the reviewed defaults are recorded digest-only and the bundle verifies.
	if first, err := generateCLI(t, base); err != nil || recordedBaseImage(t, output) != devcontainer.DefaultBaseImage || strings.Contains(first, "RECORDED IMAGE") {
		t.Fatalf("first generation: %v %q", err, first)
	}
	if err := runDevContainer(verify); err != nil {
		t.Fatal(err)
	}
	// Boundary: the tagged form of the same defaults, as recorded before #333, is refreshed.
	// The tags are the ones the committed pins were reviewed at, read from the pin source.
	reviewed := reviewedReferences(t, filepath.Join("..", ".."))
	taggedBase, taggedBuilder := reviewed["base"], reviewed["builder"]
	if digestOnly(taggedBase) != devcontainer.DefaultBaseImage || digestOnly(taggedBuilder) != devcontainer.DefaultBuilderImage {
		t.Fatalf("the committed pins %v are not the compiled defaults", reviewed)
	}
	if _, err := generateCLI(t, base, "--force", "--base-image", taggedBase, "--builder-image", taggedBuilder); err != nil || recordedBaseImage(t, output) != taggedBase {
		t.Fatalf("tagged images not recorded: %v", err)
	}
	refreshed, err := generateCLI(t, base, "--force")
	if err != nil || recordedBaseImage(t, output) != devcontainer.DefaultBaseImage {
		t.Fatalf("tagged default not refreshed: %v %q", err, refreshed)
	}
	for _, line := range []string{
		"[RECORDED IMAGE REFRESHED] base image " + taggedBase + " -> reviewed default " + devcontainer.DefaultBaseImage,
		"[RECORDED IMAGE REFRESHED] builder image " + taggedBuilder + " -> reviewed default " + devcontainer.DefaultBuilderImage,
	} {
		if !strings.Contains(refreshed, line) {
			t.Fatalf("refresh not reported, want %q in %q", line, refreshed)
		}
	}
	if err := runDevContainer(verify); err != nil {
		t.Fatalf("refreshed bundle does not verify: %v", err)
	}
	// Positive: explicit digest-only flags naming operator images are accepted and kept.
	operatorBase := "registry.example/team/dev-toolchains@sha256:" + strings.Repeat("c", 64)
	operatorBuilder := "registry.example/mirror/golang@sha256:" + strings.Repeat("d", 64)
	if _, err := generateCLI(t, base, "--force", "--base-image", operatorBase, "--builder-image", operatorBuilder); err != nil || recordedBaseImage(t, output) != operatorBase {
		t.Fatalf("digest-only flags refused: %v", err)
	}
	// Negative: a tag-only reference and a malformed digest are refused before writing.
	for _, image := range []string{"docker.io/library/golang:1.27-alpine", "docker.io/library/golang@sha256:" + strings.Repeat("a", 63)} {
		fresh, target := cliBootstrapPaths(t)
		if err := runDevContainer([]string{"generate", "--config", fresh, "--output", target, "--builder-image", image}); err == nil {
			t.Fatalf("builder image %q accepted", image)
		}
		if _, err := os.Stat(filepath.Dir(target)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("refused image %q wrote companions: %v", image, err)
		}
	}
}
