package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientsetup"
)

func TestClientApplyPreservesSettingsAndBackup(t *testing.T) {
	root := t.TempDir()
	registry := filepath.Join(root, "registry.json")
	target := filepath.Join(root, "settings.json")
	before := []byte(`{"preferences":{"theme":"dark"},"mcpServers":{"other":{"command":"/existing"}}}`)
	writeClientFixture(t, registry, []byte(`{"version":1,"servers":[{"name":"shared","command":`+quoted(trueCommand)+`,"args":[]}]}`))
	writeClientFixture(t, target, before)
	output := filepath.Join(root, "backup")
	if err := runClients([]string{"apply", "--registry", registry, "--client", "gemini", "--target", target, "--out", output}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Contains(got, []byte(`"shared"`)) || !bytes.Contains(got, []byte(`"dark"`)) {
		t.Fatalf("merge lost configuration: %s %v", got, err)
	}
	backup, err := os.ReadFile(filepath.Join(output, "config.before"))
	if err != nil || !bytes.Equal(backup, before) {
		t.Fatalf("exact backup missing: %v", err)
	}
}

func TestClientApplyRejectsChangedSnapshotAndSymlink(t *testing.T) {
	registry := clientsetup.Registry{Version: 1, Servers: []clientsetup.Server{{Name: "shared", Command: trueCommand}}}
	before := []byte(`{}`)
	plan, err := clientsetup.BuildPlan(t.Context(), registry, clientsetup.Gemini, before)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range []bool{false, true} {
		root := t.TempDir()
		target := filepath.Join(root, "settings.json")
		changed := []byte(`{"concurrent":"preserve"}`)
		actual := target
		if link {
			actual = filepath.Join(root, "outside.json")
			if err := os.Symlink(actual, target); err != nil {
				t.Fatal(err)
			}
		}
		writeClientFixture(t, actual, changed)
		if err := publishClientPlan(t.Context(), plan, target, filepath.Join(root, "backup"), before, true); err == nil {
			t.Fatal("stale or symlink destination was accepted")
		}
		got, err := os.ReadFile(actual)
		if err != nil || !bytes.Equal(got, changed) {
			t.Fatalf("concurrent target changed: %v", err)
		}
	}
}

func TestClientApplyRejectsArtifactOverlapBeforeWriting(t *testing.T) {
	for _, suffix := range []string{".", "plan.json", "nested/settings.json"} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			registry := filepath.Join(root, "registry.json")
			writeClientFixture(t, registry, []byte(`{"version":1,"servers":[{"name":"shared","command":`+quoted(trueCommand)+`}]}`))
			output := filepath.Join(root, "backup")
			target := filepath.Join(output, filepath.FromSlash(suffix))
			if err := runClients([]string{"apply", "--registry", registry, "--client", "gemini", "--target", target, "--out", output}); err == nil {
				t.Fatal("target overlapping backup was accepted")
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("rejected operation created output or target: %v", err)
			}
		})
	}
}

func TestClientPublicationBindsPlanToBackup(t *testing.T) {
	registry := clientsetup.Registry{Version: 1, Servers: []clientsetup.Server{{Name: "shared", Command: trueCommand}}}
	plan, err := clientsetup.BuildPlan(t.Context(), registry, clientsetup.Gemini, []byte(`{"wrong":"snapshot"}`))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "settings.json")
	output := filepath.Join(root, "backup")
	before := []byte(`{"restored":"preserve"}`)
	writeClientFixture(t, target, before)
	if err := publishClientPlan(t.Context(), plan, target, output, before, true); err == nil {
		t.Fatal("plan built from another snapshot was accepted")
	}
	actual, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(actual, before) {
		t.Fatalf("target was changed: %v", err)
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("rejected plan created artifacts: %v", err)
	}
}

func writeClientFixture(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
