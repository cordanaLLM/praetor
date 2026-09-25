package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// The language server diagnoses against the ceilings the opened workspace resolves to, the
// ones `praetorctl audit` enforces there, not a literal of its own (#360).

func policyWorkspace(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, config.ManifestFileName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

const tightManifest = "version: 1\nrepository:\n  owner: example\n  name: demo\noverrides:\n  complexity:\n    max_func_loc: 40\n    max_statements: 3\n"

func fileURI(path string) string {
	slashed := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

func initialize(t *testing.T, srv *Server, params any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := mustHandle(t, srv, context.Background(), string(raw)); resp == nil || resp.Error != nil {
		t.Fatalf("initialize failed: %+v", resp)
	}
}

func lengthDiagnostics(t *testing.T, srv *Server, loc int) int {
	t.Helper()
	diags, err := srv.AnalyzeGoSource("file:///w.go", wideFunc("Wide", loc))
	if err != nil {
		t.Fatal(err)
	}
	return countDiagnostics(diags, "HISS-04", "LOC")
}

func TestLSP_Positive_InitializeAdoptsWorkspacePolicy(t *testing.T) {
	root := policyWorkspace(t, tightManifest)
	for name, params := range map[string]any{
		"workspaceFolders": map[string]any{"workspaceFolders": []map[string]string{{"uri": fileURI(root), "name": "w"}}},
		"rootUri":          map[string]any{"rootUri": fileURI(root)},
		"rootPath":         map[string]any{"rootPath": root},
	} {
		srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")
		initialize(t, srv, params)
		if got := srv.complexityPolicy(); got.MaxFuncLOC != 40 || got.MaxStatements != 3 {
			t.Fatalf("%s: adopted %+v, want the workspace's 40/3", name, got)
		}
		if lengthDiagnostics(t, srv, 40) != 0 || lengthDiagnostics(t, srv, 41) != 1 {
			t.Errorf("%s: the workspace's 40-line ceiling is not enforced", name)
		}
		diags, err := srv.AnalyzeGoSource("file:///s.go", goFunc("Four", 2))
		if err != nil || countDiagnostics(diags, "HISS-04", "statement count") != 1 {
			t.Errorf("%s: the workspace statement ceiling is not enforced: %+v err=%v", name, diags, err)
		}
	}
}

func TestLSP_Negative_UnresolvableWorkspaceKeepsCeiling(t *testing.T) {
	corrupt := policyWorkspace(t, "version: [\n")
	for name, params := range map[string]any{
		"corrupt manifest":  map[string]any{"rootUri": fileURI(corrupt)},
		"remote authority":  map[string]any{"rootUri": "file://remote-host/srv/repo"},
		"non-file scheme":   map[string]any{"rootUri": "https://example.invalid/repo"},
		"malformed params":  "not an object",
		"no workspace":      map[string]any{},
		"unparseable URI":   map[string]any{"rootUri": "file://%zz"},
		"empty folder list": map[string]any{"workspaceFolders": []any{}},
	} {
		srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")
		initialize(t, srv, params)
		if got := srv.complexityPolicy(); got != config.HISSComplexityCeiling() {
			t.Errorf("%s: adopted %+v, want the fallback ceiling", name, got)
		}
	}
}

func TestLSP_Boundary_WorkspaceRootSelection(t *testing.T) {
	folder := t.TempDir()
	other := t.TempDir()
	params := map[string]any{
		"workspaceFolders": []map[string]string{{"uri": fileURI(folder)}},
		"rootUri":          fileURI(other),
		"rootPath":         other,
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if got := initializeWorkspaceRoot(raw); got != folder {
		t.Errorf("workspace folder must win over rootUri/rootPath: %q", got)
	}
	// A folder whose URI is unusable falls through to rootUri.
	raw, err = json.Marshal(map[string]any{"workspaceFolders": []map[string]string{{"uri": "http://x/"}}, "rootUri": fileURI(other)})
	if err != nil {
		t.Fatal(err)
	}
	if got := initializeWorkspaceRoot(raw); got != other {
		t.Errorf("unusable folder must fall through to rootUri: %q", got)
	}
	if initializeWorkspaceRoot(nil) != "" {
		t.Error("absent params must name no workspace")
	}

	for raw, want := range map[string]string{
		"":                              "",
		"file:///srv/my%20repo":         filepath.FromSlash("/srv/my repo"),
		"file://localhost/srv/repo":     filepath.FromSlash("/srv/repo"),
		"file:///C:/Users/dev/repo":     filepath.FromSlash("C:/Users/dev/repo"),
		"file:///1:/not-a-drive":        filepath.FromSlash("/1:/not-a-drive"),
		"file://build-host/srv/repo":    "",
		"vscode-remote://ssh/srv/repo":  "",
		"file:///":                      filepath.FromSlash("/"),
		"file:///C:":                    "C:",
		"file:///c:/lower/drive/letter": filepath.FromSlash("c:/lower/drive/letter"),
	} {
		if got := fileURIPath(raw); got != want {
			t.Errorf("fileURIPath(%q) = %q, want %q", raw, got, want)
		}
	}
}
