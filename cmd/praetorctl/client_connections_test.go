package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const connectionFixture = `{"version":1,"gateway":{"bridge_command":"/opt/agent/bridge","token_file":"/private/not-read","endpoints":[{"name":"shared","url":"https://example.test/mcp/"}]},"memory":{"project_root":"/work/project","bank_id":"private::project"}}`

func TestConnectionCLIExportsAndBindsMemory(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile.json")
	writeClientFixture(t, profile, []byte(connectionFixture))
	output := filepath.Join(root, "connections")
	if err := runClients([]string{"connect", "--profile", profile, "--out", output}); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(output, "registry.json")
	if _, err := prepareClient(t.Context(), registry, "gemini", ""); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "memory.json")
	before := []byte(`{"apiToken":"secret-fixture","other":true}`)
	writeClientFixture(t, target, before)
	backup := filepath.Join(root, "backup")
	if err := runClients([]string{"bind-memory", "--profile", profile, "--target", target, "--out", backup}); err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(filepath.Join(backup, "config.before"))
	if err != nil || !bytes.Equal(retained, before) {
		t.Fatalf("missing exact private backup: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("target privacy: %v", err)
	}
}

func TestConnectionCLIFailureDoesNotPublish(t *testing.T) {
	for _, raw := range []string{`{"version":2}`, `{"version":1,"apiToken":"secret"}`, connectionFixture} {
		root := t.TempDir()
		profile := filepath.Join(root, "profile.json")
		writeClientFixture(t, profile, []byte(raw))
		target := filepath.Join(root, "memory.json")
		before := []byte(`{"mapPathToBank":{"/work/project":"another-bank"}}`)
		writeClientFixture(t, target, before)
		output := filepath.Join(root, "backup")
		if err := runClients([]string{"bind-memory", "--profile", profile, "--target", target, "--out", output}); err == nil {
			t.Fatal("invalid or conflicting profile accepted")
		}
		actual, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(actual, before) {
			t.Fatalf("rejected operation changed target: %v", err)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatalf("rejected operation created backup: %v", err)
		}
	}
}
