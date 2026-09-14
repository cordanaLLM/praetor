package devcontainer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapMissingSourceNeverReferencesAdopterPraetorFiles(t *testing.T) {
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Spec().State != BootstrapUnavailable {
		t.Fatal("missing selected source was called available")
	}
	if bundle.Config.Build != nil || strings.Contains(bundle.Config.PostCreateCommand, "./cmd/praetorctl") || strings.Contains(bundle.Config.PostCreateCommand, "standardsctl") {
		t.Fatal("missing bootstrap still references adopter-local Praetor files")
	}
	if !strings.Contains(bundle.Config.PostCreateCommand, "exit 1") {
		t.Fatal("unavailable bootstrap can silently succeed")
	}
}

func TestBootstrapWritesCompanionsAndPreservesCustomConfig(t *testing.T) {
	source := bootstrapSourceFixture(t)
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{SourceRoot: source})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), target, bundle, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), bundle.Config.Build.Dockerfile)); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), target, mustBaseContainer(t)); err != nil {
		t.Fatal(err)
	}
	custom := []byte("{\"name\":\"operator owned\",\"image\":\"custom:tag\"}\n")
	if err := os.WriteFile(target, custom, 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteBundle(t.Context(), target, bundle, false); err == nil {
		t.Fatal("custom container overwritten")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(custom) {
		t.Fatal("custom container changed")
	}
}

func mustBaseContainer(t *testing.T) *DevContainer {
	t.Helper()
	dc, err := SynthesizeFromProfiles("adopted/app", []string{"framework"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return dc
}

func bootstrapSourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeBootstrapFile(t, root, "go.mod", "module github.com/cordanaLLM/praetor\n\ngo 1.27\n")
	writeBootstrapFile(t, root, "go.sum", "")
	writeBootstrapFile(t, root, "LICENSE", "Synthetic test license\n")
	writeBootstrapFile(t, root, "cmd/praetorctl/main.go", "package main\nfunc main() {}\n")
	if _, err := runSourceGit(t.Context(), root, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeBootstrapFile(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapCancellationAndNilContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PrepareBundle(ctx, "app", nil, nil, BootstrapOptions{}); err == nil {
		t.Fatal("cancelled preparation succeeded")
	}
	var absent context.Context
	if _, err := PrepareBundle(absent, "app", nil, nil, BootstrapOptions{}); err == nil {
		t.Fatal("nil context succeeded")
	}
}
