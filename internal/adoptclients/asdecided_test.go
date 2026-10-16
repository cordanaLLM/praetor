package adoptclients

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

func TestReconcileAsDecidedGoldenAndIdempotent(t *testing.T) {
	repo := t.TempDir()
	writeDecisionTestFile(t, repo, "docs/adr/0001-choice.md", "# Choice\n\nDecision\n")
	writeDecisionTestFile(t, repo, ".mcp.json", "{\n  \"mcpServers\": {\n    \"praetor-dev\": {\"command\": \"praetor-dev\", \"args\": []}\n  }\n}\n")
	writeDecisionTestFile(t, repo, ".gemini/settings.json", "{\n  \"theme\": \"dark\",\n  \"mcpServers\": {\n    \"praetor-dev\": {\"command\": \"praetor-dev\", \"args\": []}\n  }\n}\n")
	writeDecisionTestFile(t, repo, "opencode.json", "{\n  \"mcp\": {\n    \"praetor-dev\": {\"type\": \"local\", \"command\": [\"praetor-dev\"]}\n  }\n}\n")
	writeDecisionTestFile(t, repo, ".continue/mcpServers/praetor.yaml", "name: Praetor MCP\nversion: 1.0.0\nschema: v1\nmcpServers:\n  - name: praetor-dev\n    command: praetor-dev\n    args: []\n")
	first, err := ReconcileAsDecided(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	files := decisionTestFiles(t, repo)
	wants := map[string]string{
		".mcp.json":                         "{\n  \"mcpServers\": {\n    \"asdecided\": {\n      \"args\": [\n        \"decisions\",\n        \"serve\"\n      ],\n      \"command\": \"praetorctl\",\n      \"type\": \"stdio\"\n    },\n    \"praetor-dev\": {\n      \"command\": \"praetor-dev\",\n      \"args\": []\n    }\n  }\n}\n",
		".gemini/settings.json":             "{\n  \"mcpServers\": {\n    \"asdecided\": {\n      \"args\": [\n        \"decisions\",\n        \"serve\"\n      ],\n      \"command\": \"praetorctl\"\n    },\n    \"praetor-dev\": {\n      \"command\": \"praetor-dev\",\n      \"args\": []\n    }\n  },\n  \"theme\": \"dark\"\n}\n",
		"opencode.json":                     "{\n  \"mcp\": {\n    \"asdecided\": {\n      \"command\": [\n        \"praetorctl\",\n        \"decisions\",\n        \"serve\"\n      ],\n      \"type\": \"local\"\n    },\n    \"praetor-dev\": {\n      \"type\": \"local\",\n      \"command\": [\n        \"praetor-dev\"\n      ]\n    }\n  }\n}\n",
		".continue/mcpServers/praetor.yaml": "name: Praetor MCP\nversion: 1.0.0\nschema: v1\nmcpServers:\n  - name: praetor-dev\n    command: praetor-dev\n    args: []\n  - name: asdecided\n    command: praetorctl\n    args:\n      - decisions\n      - serve\n",
	}
	for path, want := range wants {
		if string(files[path]) != want {
			t.Errorf("%s content = %q, want golden %q", path, files[path], want)
		}
	}
	second, err := ReconcileAsDecided(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("actions changed on repeat: %v -> %v", first, second)
	}
	for path, before := range files {
		if got := readDecisionTestFile(t, repo, path); got != string(before) {
			t.Errorf("%s changed on re-adoption", path)
		}
	}
}

func TestReconcileAsDecidedPreservesConflictingEntry(t *testing.T) {
	repo := t.TempDir()
	writeDecisionTestFile(t, repo, "docs/adr/0001-choice.md", "# Choice\n")
	conflict := "{\"mcpServers\":{\"asdecided\":{\"command\":\"user-owned\",\"args\":[]},\"praetor-dev\":{\"command\":\"praetor-dev\",\"args\":[]}}}\n"
	writeDecisionTestFile(t, repo, ".mcp.json", conflict)
	actions, err := ReconcileAsDecided(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := readDecisionTestFile(t, repo, ".mcp.json"); got != conflict {
		t.Fatalf("user-owned entry changed: %q", got)
	}
	if !strings.Contains(actionDetailText(actions, ".mcp.json"), "User-owned") {
		t.Fatalf("conflict not reported: %+v", actions)
	}
}

func TestReconcileAsDecidedSkipsWhenNoRecords(t *testing.T) {
	repo := t.TempDir()
	writeDecisionTestFile(t, repo, "docs/adr/README.md", "# Index\n")
	writeDecisionTestFile(t, repo, "docs/adr/0000-template.md", "# Template\n")
	actions, err := ReconcileAsDecided(t.Context(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".mcp.json", ".gemini/settings.json", "opencode.json", ".continue/mcpServers/praetor.yaml"} {
		if _, err := os.Stat(filepath.Join(repo, path)); !os.IsNotExist(err) {
			t.Errorf("%s created without ADR records", path)
		}
	}
	if !strings.Contains(actionDetailText(actions, "asdecided"), "No ADR records") ||
		!strings.Contains(actionDetailText(actions, "codex"), "Codex CLI") ||
		!strings.Contains(actionDetailText(actions, "agy"), "host-local") {
		t.Fatalf("skip reasons absent: %+v", actions)
	}
}

func TestReconcileAsDecidedDryRunAndInvalidInput(t *testing.T) {
	repo := t.TempDir()
	if actions, err := ReconcileAsDecided(t.Context(), repo, true); err != nil || len(actions) != 3 {
		t.Fatalf("dry run actions=%v err=%v", actions, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReconcileAsDecided(ctx, repo, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context = %v", err)
	}
}

func actionDetailText(actions []adopt.ActionDetail, path string) string {
	for _, action := range actions {
		if action.Path == path {
			return action.Details
		}
	}
	return ""
}

func writeDecisionTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readDecisionTestFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func decisionTestFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, path := range []string{".mcp.json", ".gemini/settings.json", "opencode.json", ".continue/mcpServers/praetor.yaml"} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		files[path] = data
	}
	return files
}
