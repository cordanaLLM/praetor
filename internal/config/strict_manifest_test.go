package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifest places a manifest below a fresh root and returns its path.
func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".standards.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// TestLoadManifestAcceptsTheDeclaredSchema is the positive dimension: every section the
// canonical manifest legitimately carries parses, including the ones consumed elsewhere
// through their own narrow reads of the same file.
func TestLoadManifestAcceptsTheDeclaredSchema(t *testing.T) {
	path := writeManifest(t, `version: 1
repository:
  owner: "exampleOrg"
  name: "example"
  visibility: "public"
receipt:
  public_key: "abc123"
profiles:
  - "framework"
facets:
  - "security:high"
overrides:
  complexity:
    max_func_loc: 75
  ci:
    diff_aware_filtering: true
    skip_heavy_gates_on_docs_or_state: true
needs:
  required:
    - "config.yaml"
register:
  surfaces:
    forge: social
  tasks:
    function_docstrings: docs
  evidence:
    inline_max_lines: 40
`)

	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if m.Register == nil || m.Register.Tasks["function_docstrings"].Register != TextRegisterDocs {
		t.Errorf("register section was dropped: %+v", m.Register)
	}
	if m.Repository.Owner != "exampleOrg" || m.Version != 1 {
		t.Errorf("unexpected manifest head: %+v", m.Repository)
	}
	if m.Receipt == nil || m.Receipt.PublicKey != "abc123" {
		t.Errorf("receipt anchor was dropped: %+v", m.Receipt)
	}
	if m.Overrides.CI == nil || !m.Overrides.CI.DiffAwareFiltering {
		t.Errorf("ci overrides were dropped: %+v", m.Overrides.CI)
	}
	if len(m.Needs) == 0 {
		t.Error("needs declaration was dropped")
	}
}

// TestLoadManifestRejectsMisspelledKey is the negative dimension and the defect this
// closes: a misspelled key used to be discarded in silence, so the repository was governed
// by the built-in defaults while the operator believed the manifest had tightened policy.
func TestLoadManifestRejectsMisspelledKey(t *testing.T) {
	path := writeManifest(t, "version: 1\nprofile:\n  - \"framework\"\n")

	_, err := LoadManifest(path)
	if err == nil {
		t.Fatal("a misspelled top-level key must be an error, not a silent default")
	}
	if !strings.Contains(err.Error(), "profile") {
		t.Errorf("the error must name the offending key, got: %v", err)
	}
}

// TestLoadManifestRejectsMisspelledNestedKey is the negative dimension one level down,
// where a silent drop is most dangerous: a mistyped override silently loosens policy
// rather than tightening it.
func TestLoadManifestRejectsMisspelledNestedKey(t *testing.T) {
	path := writeManifest(t, "version: 1\noverrides:\n  complexity:\n    max_func_lo: 10\n")

	if _, err := LoadManifest(path); err == nil {
		t.Fatal("a misspelled override key must be refused rather than ignored")
	}
}

// TestLoadManifestEmptyDocument is the boundary dimension: an empty manifest is a valid
// zero manifest, not a decode failure, so an operator who truncates the file gets the
// defaults rather than a parse error.
func TestLoadManifestEmptyDocument(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, ""))
	if err != nil {
		t.Fatalf("an empty manifest must decode to the zero value, got: %v", err)
	}
	if m == nil {
		t.Fatal("expected a zero manifest, got nil")
	}
	if m.Version != 0 || len(m.Profiles) != 0 {
		t.Errorf("expected a zero manifest, got %+v", m)
	}
}

// TestLoadManifestRepositoryManifestParses is the boundary dimension against the real
// schema in use: this repository's own manifest must satisfy the strict decoder, which is
// what proves the declared type matches the file the engine actually ships.
func TestLoadManifestRepositoryManifestParses(t *testing.T) {
	m, err := LoadManifest(filepath.Join("..", "..", ".standards.yaml"))
	if err != nil {
		t.Fatalf("the repository's own manifest must parse strictly: %v", err)
	}
	if m.Receipt == nil || m.Receipt.PublicKey == "" {
		t.Error("the repository manifest must carry its pinned receipt key")
	}
}
