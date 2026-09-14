package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestAdoptMCPPlanningSelectionWriteReadback(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServerWithOptions(ServerOptions{RootDir: root, Version: "test", AllowOutsideRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"profile": "planning-artifacts", "facets": "agent:sandboxed", "source_root": source, "record_baseline": false, "dry_run": true}
	expectText(t, "planning preview", callTool(t, srv, "standards_adopt", args), "Archetype: planning-artifacts")
	if _, err := os.Stat(filepath.Join(root, ".standards.yaml")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote manifest or failed observation: %v", err)
	}
	args["dry_run"] = false
	result := callTool(t, srv, "standards_adopt", args)
	expectText(t, "planning apply", result, "[APPLIED]")
	expectText(t, "selected facets", result, "Facets: agent:sandboxed")
	manifest, err := config.LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(manifest.Profiles, ",") != "planning-artifacts" || strings.Join(manifest.Facets, ",") != "agent:sandboxed" {
		t.Fatalf("selection lost in manifest: %+v", manifest)
	}
	if _, err := config.ValidateLockfile(t.Context(), root, manifest); err != nil {
		t.Fatalf("selected profile/facet pins invalid: %v", err)
	}
	for _, name := range []string{"go.mod", "Cargo.toml"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("planning adoption invented runtime manifest %s: %v", name, err)
		}
	}
}

func TestAdoptMCPInvalidSelectionBeforeWrites(t *testing.T) {
	for _, args := range []map[string]any{
		{"profile": true}, {"facets": []any{"agent:sandboxed"}},
		{"profile": strings.Repeat("x", 129)},
		{"facets": strings.Repeat("x", 8193)},
		{"facets": strings.Repeat("x,", 64)},
	} {
		root := t.TempDir()
		initGitRepo(t, root)
		srv, err := NewServer(root, "test")
		if err != nil {
			t.Fatal(err)
		}
		result := callTool(t, srv, "standards_adopt", args)
		if !result.IsError {
			t.Fatalf("invalid selection accepted: %v", args)
		}
		if _, err := os.Stat(filepath.Join(root, ".standards.yaml")); !os.IsNotExist(err) {
			t.Fatalf("invalid selection mutated target: %v", err)
		}
	}
}

func TestAdoptSelectionDefaultsAndBounds(t *testing.T) {
	profile, facets, err := adoptSelection(nil)
	if err != nil || profile != "" || len(facets) != 0 {
		t.Fatalf("omitted selection changed defaults: %q %v %v", profile, facets, err)
	}
	profile, facets, err = adoptSelection(map[string]any{"profile": strings.Repeat("p", 128), "facets": strings.Repeat("x,", 63) + "x"})
	if err != nil || len(profile) != 128 || len(facets) != 64 {
		t.Fatalf("exact entry bound rejected: %q %v %v", profile, facets, err)
	}
	_, facets, err = adoptSelection(map[string]any{"facets": strings.Repeat("x", 8192)})
	if err != nil || len(facets) != 1 {
		t.Fatalf("exact byte bound rejected: %v %v", facets, err)
	}
	_, facets, err = adoptSelection(map[string]any{"facets": " , agent:sandboxed, "})
	if err != nil || strings.Join(facets, ",") != "agent:sandboxed" {
		t.Fatalf("CLI whitespace/empty-entry semantics lost: %v %v", facets, err)
	}
}
