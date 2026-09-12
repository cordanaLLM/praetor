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
	manifest := writeFixtureFile(t, root, ".standards.yaml", "repository:\n  owner: adopted\n  name: app\nprofiles: [framework]\n")
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
