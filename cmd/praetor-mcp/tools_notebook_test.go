package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/notebook"
)

func TestNotebookMCPMetadataAndConfinement(t *testing.T) {
	srv, root := newFixtureServer(t)
	content := "PRIVATE fixture requirement"
	b := notebook.Bundle{Format: notebook.Format, NotebookID: "fixture", Title: "Fixture", CapturedAt: "2026-09-12T00:00:00Z",
		Connector: "synthetic", Coverage: []string{"fixture only"}, Sources: []notebook.Source{{ID: "s1", Title: "Scope", Role: "source", Locator: "fixture", Content: content, SHA256: notebook.Digest([]byte(content))}}}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notebook.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r := callTool(t, srv, "standards_notebook_prepare", map[string]any{"bundle_path": "notebook.json"})
	if r.IsError || len(r.Content) == 0 || strings.Contains(r.Content[0].Text, content) {
		t.Fatalf("unsafe or failed report %+v", r)
	}
	for _, args := range []map[string]any{{}, {"bundle_path": t.TempDir()}, {"bundle_path": "notebook.json", "output_dir": root}} {
		if r := callTool(t, srv, "standards_notebook_prepare", args); !r.IsError {
			t.Fatal("unsafe arguments accepted")
		}
	}
}
