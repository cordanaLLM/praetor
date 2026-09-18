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
//
// This checkout is not necessarily the canonical repository: internal/operationalsync's owner
// overlay rewrites this exact file, in place, in an operational fork. So the repository.source
// assertion accepts either shape this file legitimately has -- absent (canonical, or a fork
// checkout before the first overlay commit) or a valid "<owner>/<name>" identity that differs
// from the checkout's own owner/name (an overlaid fork) -- rather than requiring the canonical
// shape unconditionally, which fails every overlaid fork's own test suite (#263).
func TestLoadManifestRepositoryManifestParses(t *testing.T) {
	m, err := LoadManifest(filepath.Join("..", "..", ".standards.yaml"))
	if err != nil {
		t.Fatalf("the repository's own manifest must parse strictly: %v", err)
	}
	if m.Receipt == nil || m.Receipt.PublicKey == "" {
		t.Error("the repository manifest must carry its pinned receipt key")
	}
	if m.Repository.Source == "" {
		return
	}
	owner, name, ok := strings.Cut(m.Repository.Source, "/")
	if !ok || owner == "" || name == "" {
		t.Errorf("repository.source %q is not an owner/name identity", m.Repository.Source)
	}
	if m.Repository.Source == m.Repository.Owner+"/"+m.Repository.Name {
		t.Errorf("repository.source %q must record the public source, not this checkout's own owner/name", m.Repository.Source)
	}
}

// TestLoadManifestAcceptsRepositorySource is the positive dimension for #255: the field
// internal/operationalsync's owner overlay writes into a fork's manifest, and
// internal/forge's manifestIdentity reads in preference to owner/name, round-trips.
func TestLoadManifestAcceptsRepositorySource(t *testing.T) {
	path := writeManifest(t, "version: 1\nrepository:\n  owner: \"lusoris\"\n  name: \"praetor\"\n  visibility: \"private\"\n  source: \"cordanaLLM/praetor\"\n")
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if m.Repository.Source != "cordanaLLM/praetor" {
		t.Errorf("repository.source = %q, want cordanaLLM/praetor", m.Repository.Source)
	}
}

// TestLoadManifestRejectsMalformedRepositorySource is the negative dimension: a source that
// is not an "<owner>/<name>" identity must fail closed at load time rather than reach
// manifestIdentity, which would otherwise have to choose between an unusable identity and a
// silent fallback to owner/name -- exactly the ambiguity this field exists to remove.
func TestLoadManifestRejectsMalformedRepositorySource(t *testing.T) {
	for _, source := range []string{"not-an-identity", "cordanaLLM/praetor/extra", "/praetor", "cordanaLLM/", "/"} {
		path := writeManifest(t, "version: 1\nrepository:\n  owner: \"lusoris\"\n  name: \"praetor\"\n  visibility: \"private\"\n  source: \""+source+"\"\n")
		if _, err := LoadManifest(path); err == nil {
			t.Errorf("repository.source %q was accepted", source)
		}
	}
}

// TestLoadManifestRepositorySourceBoundary is the boundary dimension: an omitted source keeps
// today's canonical-repository behaviour (the zero value, not an error), and a source equal
// to the manifest's own owner/name is accepted -- redundant, not malformed.
func TestLoadManifestRepositorySourceBoundary(t *testing.T) {
	omitted, err := LoadManifest(writeManifest(t, "version: 1\nrepository:\n  owner: \"cordanaLLM\"\n  name: \"praetor\"\n  visibility: \"public\"\n"))
	if err != nil || omitted.Repository.Source != "" {
		t.Errorf("omitted source: %+v, %v", omitted, err)
	}
	redundant, err := LoadManifest(writeManifest(t, "version: 1\nrepository:\n  owner: \"cordanaLLM\"\n  name: \"praetor\"\n  visibility: \"public\"\n  source: \"cordanaLLM/praetor\"\n"))
	if err != nil || redundant.Repository.Source != "cordanaLLM/praetor" {
		t.Errorf("source equal to owner/name: %+v, %v", redundant, err)
	}
}
