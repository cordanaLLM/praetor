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
