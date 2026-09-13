package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dogfood"
)

func writeSuiteMCPFixture(t *testing.T, root, sourcePath string, public bool) {
	t.Helper()
	if err := os.WriteFile(sourcePath, []byte(mcpTranscriptFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(mcpTranscriptFixture))
	config := dogfood.SuiteConfig{Version: 1, PublicRepositories: []string{}, Transcripts: []dogfood.SuiteTranscript{{ID: "fixture", SourcePath: sourcePath, SHA256: hex.EncodeToString(sum[:]), Format: "antigravity-jsonl-v1"}}}
	if public {
		config.PublicRepositories = []string{"https://github.com/spf13/cobra#" + strings.Repeat("a", 40)}
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "suite.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSuiteMCPVerifiesActualPrivateReplay(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeSuiteMCPFixture(t, root, filepath.Join(root, "transcript_full.jsonl"), false)
	result := callTool(t, srv, "standards_dogfood_suite", map[string]any{"config_path": "suite.json", "artifact_dir": "run", "stage": "verify"})
	if result.IsError {
		t.Fatalf("suite failed: %+v", result)
	}
	var envelope struct {
		Report dogfood.SuiteReport `json:"report"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &envelope); err != nil {
		t.Fatal(err)
	}
	report := envelope.Report
	if !report.Verified || report.Cases[0].Replay.Stored != 0 || report.Cases[0].Replay.AlreadyPresent != 1 {
		t.Fatalf("wrong result: %+v", report)
	}
	if strings.Contains(result.Content[0].Text, "private fixture payload") {
		t.Fatal("MCP disclosed payload")
	}
	if _, err := os.Stat(filepath.Join(root, "run", "report.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSuiteMCPChecksEmbeddedConfinementAndRemotePolicy(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeSuiteMCPFixture(t, root, filepath.Join(t.TempDir(), "transcript_full.jsonl"), false)
	args := map[string]any{"config_path": "suite.json", "artifact_dir": "run", "stage": "plan"}
	if result := callTool(t, srv, "standards_dogfood_suite", args); !result.IsError {
		t.Fatal("embedded source escaped confinement")
	}
	writeSuiteMCPFixture(t, root, filepath.Join(root, "transcript_full.jsonl"), true)
	args["stage"] = "verify"
	if result := callTool(t, srv, "standards_dogfood_suite", args); !result.IsError {
		t.Fatal("client enabled remote access")
	}
	if _, err := os.Stat(filepath.Join(root, "run")); !os.IsNotExist(err) {
		t.Fatal("unauthorized case created evidence")
	}
	args["stage"] = "plan"
	if result := callTool(t, srv, "standards_dogfood_suite", args); result.IsError {
		t.Fatal("offline plan required network opt-in")
	}
}

func TestSuiteMCPRejectsInvalidArguments(t *testing.T) {
	srv, _ := newFixtureServer(t)
	for _, args := range []map[string]any{
		{}, {"config_path": true, "artifact_dir": "run"}, {"config_path": "suite.json", "artifact_dir": "run", "stage": nil},
		{"config_path": "suite.json", "artifact_dir": "run", "allow_remote": true},
		{"config_path": t.TempDir(), "artifact_dir": "run"}, {"config_path": "suite.json", "artifact_dir": t.TempDir()},
	} {
		if result := callTool(t, srv, "standards_dogfood_suite", args); !result.IsError {
			t.Fatalf("accepted %+v", args)
		}
	}
}
