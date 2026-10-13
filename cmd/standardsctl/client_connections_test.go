package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

var connectionFixture = `{"version":1,"gateway":{"bridge_command":` + quoted(agentBridge) + `,"token_file":` + quoted(notReadToken) + `,"endpoints":[{"name":"shared","url":"https://example.test/mcp/"}]},"memory":{"project_root":` + quoted(memoryProject) + `,"bank_id":"private::project"}}`

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
	if err != nil || (util.ModeIsProtection() && info.Mode().Perm() != 0o600) {
		t.Fatalf("target privacy: %v", err)
	}
}

func TestConnectionCLIFailureDoesNotPublish(t *testing.T) {
	for _, raw := range []string{`{"version":2}`, `{"version":1,"apiToken":"secret"}`, connectionFixture} {
		root := t.TempDir()
		profile := filepath.Join(root, "profile.json")
		writeClientFixture(t, profile, []byte(raw))
		target := filepath.Join(root, "memory.json")
		before := []byte(`{"mapPathToBank":{` + quoted(memoryProject) + `:"another-bank"}}`)
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

func TestConnectionCLILocalOnlyConnectsPreparesApplies(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile.json")
	writeClientFixture(t, profile, []byte(`{"version":1,"gateway":{"local_servers":[{"name":"local-tools","command":`+quoted(trueCommand)+`,"args":[]}]}}`))
	output := filepath.Join(root, "connections")
	if err := runClients([]string{"connect", "--profile", profile, "--out", output}); err != nil {
		t.Fatalf("connect refused a local-only profile: %v", err)
	}
	registry := filepath.Join(output, "registry.json")
	if err := runClients([]string{"prepare", "--registry", registry, "--client", "gemini", "--out", filepath.Join(root, "prepared")}); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	target := filepath.Join(root, "settings.json")
	writeClientFixture(t, target, []byte(`{"preferences":{"theme":"dark"}}`))
	if err := runClients([]string{"apply", "--registry", registry, "--client", "gemini", "--target", target, "--out", filepath.Join(root, "backup")}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Contains(got, []byte(`"local-tools"`)) || !bytes.Contains(got, []byte(`"dark"`)) {
		t.Fatalf("apply lost the local server or existing settings: %s %v", got, err)
	}
}

func TestConnectionCLIRefusesEmptyProfile(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile.json")
	writeClientFixture(t, profile, []byte(`{"version":1,"gateway":{}}`))
	output := filepath.Join(root, "connections")
	err := runClients([]string{"connect", "--profile", profile, "--out", output})
	if err == nil || !strings.Contains(err.Error(), "gateway.endpoints") || !strings.Contains(err.Error(), "gateway.local_servers") {
		t.Fatalf("empty profile not refused by name: %v", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("refused profile created output: %v", statErr)
	}
}
