package config

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeToolingManifest(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ManifestFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// Positive: both keys reach the caller verbatim; resolving ids belongs to their owners.
func TestLoadDeclaredTooling_Positive_ReadsBothKeys(t *testing.T) {
	root := writeToolingManifest(t, "version: 1\neditors: [vscode, nvim]\nagent_clients: [claude]\n")
	got, err := LoadDeclaredTooling(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := DeclaredTooling{Editors: []string{"vscode", "nvim"}, AgentClients: []string{"claude"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// Negative: a misspelled key and a scalar where a list belongs are errors, never an empty
// selection, and a nil context is refused.
func TestLoadDeclaredTooling_Negative_MalformedManifestFails(t *testing.T) {
	for _, body := range []string{"version: 1\neditor: [vscode]\n", "version: 1\neditors: vscode\n"} {
		if _, err := LoadDeclaredTooling(context.Background(), writeToolingManifest(t, body)); err == nil {
			t.Errorf("manifest %q accepted", body)
		}
	}
	//nolint:staticcheck // exercising the nil-context contract
	if _, err := LoadDeclaredTooling(nil, t.TempDir()); err == nil {
		t.Error("nil context accepted")
	}
}

// Boundary: no manifest and an absent key declare nothing (nil); an explicit empty list stays
// a non-nil selection of no ids, which is what separates "none" from "every".
func TestLoadDeclaredTooling_Boundary_AbsentVersusEmpty(t *testing.T) {
	ctx := context.Background()
	for name, root := range map[string]string{
		"no manifest": t.TempDir(),
		"no key":      writeToolingManifest(t, "version: 1\n"),
		"null key":    writeToolingManifest(t, "version: 1\neditors:\n"),
	} {
		got, err := LoadDeclaredTooling(ctx, root)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Editors != nil || got.AgentClients != nil {
			t.Errorf("%s: got %+v, want nil lists", name, got)
		}
	}
	got, err := LoadDeclaredTooling(ctx, writeToolingManifest(t, "version: 1\neditors: []\nagent_clients: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Editors == nil || len(got.Editors) != 0 || got.AgentClients == nil || len(got.AgentClients) != 0 {
		t.Fatalf("explicit empty lists lost: %+v", got)
	}
}
