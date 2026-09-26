package flavor_test

// The bodies an adopter actually receives. Before this file the only assertion on a
// scaffolded template was os.Stat on the path (flavor_test.go, TestApplyFlavor_NewArchetypes),
// so every version, schema and image reference inside defaultTemplateContent could change,
// or rot, without a test noticing. HISS-20 wants the claim replayable in both directions:
// each case below fails if the value regresses AND fails if the value is merely absent.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// digestPinned matches a FROM line carrying tag plus an explicit sha256 digest.
var digestPinned = regexp.MustCompile(`^FROM \S+:[^@\s]+@sha256:[0-9a-f]{64}$`)

// scaffoldInto applies a flavor to an empty repository and returns the file bodies.
func scaffoldInto(t *testing.T, flavorName string, want ...string) map[string]string {
	t.Helper()
	root := t.TempDir()
	if _, err := flavor.ApplyFlavor(t.Context(), root, flavorName, false); err != nil {
		t.Fatalf("apply %s: %v", flavorName, err)
	}
	bodies := make(map[string]string, len(want))
	for _, rel := range want {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s scaffolded no %s: %v", flavorName, rel, err)
		}
		bodies[rel] = string(data)
	}
	return bodies
}

func TestScaffoldedDockerfileNamesADigestPinnedDebian13Runtime(t *testing.T) {
	body := scaffoldInto(t, "go-service", "Dockerfile")["Dockerfile"]
	from, _, ok := strings.Cut(body, "\n")
	if !ok {
		t.Fatalf("emitted Dockerfile has no FROM line: %q", body)
	}
	if !digestPinned.MatchString(from) {
		t.Errorf("HISS-11: emitted runtime is not tag-plus-digest pinned: %q", from)
	}
	if !strings.Contains(from, "gcr.io/distroless/static-debian13:nonroot") {
		t.Errorf("emitted runtime is not the Debian 13 distroless image: %q", from)
	}
	// Negative: the rolling alias resolves to the same digest today and diverges the
	// day distroless promotes it, so its absence is the property under test.
	if strings.Contains(body, "gcr.io/distroless/static:nonroot") {
		t.Errorf("emitted Dockerfile fell back to the rolling static alias: %q", body)
	}
}

func TestScaffoldedGolangciConfigCarriesTheV2Schema(t *testing.T) {
	body := scaffoldInto(t, "go-service", ".golangci.yml")[".golangci.yml"]
	if !strings.HasPrefix(body, "version: \"2\"\n") {
		t.Errorf("golangci-lint v2 rejects a configuration without a leading version key: %q", body)
	}
	for _, v1Only := range []string{"exclude-use-default", "max-issues-per-linter", "max-same-issues"} {
		if strings.Contains(body, v1Only) {
			t.Errorf("v1-only key %q survived in a v2 configuration", v1Only)
		}
	}
	// The correctness subset the scaffold promises, named one by one so dropping any
	// single linter is a failing test rather than a silent narrowing.
	for _, linter := range []string{"govet", "staticcheck", "errcheck", "errorlint", "nilerr", "unused", "ineffassign", "bodyclose", "noctx"} {
		if !strings.Contains(body, "    - "+linter+"\n") {
			t.Errorf("enabled set lost %q", linter)
		}
	}
}

func TestScaffoldedRustfmtNamesThe2024Edition(t *testing.T) {
	body := scaffoldInto(t, "rust-systems", "rustfmt.toml")["rustfmt.toml"]
	if !strings.Contains(body, "edition = \"2024\"\n") {
		t.Errorf("rustfmt.toml is not on the 2024 edition: %q", body)
	}
	if strings.Contains(body, "edition = \"2021\"") {
		t.Errorf("rustfmt.toml still carries the 2021 edition: %q", body)
	}
}

// Boundary: a required template with no case in defaultTemplateContent degrades to a
// one-line comment. That is issue #336 and it is asserted here rather than left
// undocumented, so closing #336 shows up as a failing test instead of a surprise.
func TestRequiredTemplatesWithoutABodyStillEmitOnlyAStub(t *testing.T) {
	bodies := scaffoldInto(t, "go-service", ".github/workflows/ci.yml", ".github/workflows/security.yml")
	for rel, body := range bodies {
		if strings.Contains(body, "jobs:") {
			t.Errorf("%s now has real content; #336 is closed and this expectation is stale", rel)
		}
		if !strings.HasPrefix(body, "# "+filepath.Base(rel)+" configuration for ") {
			t.Errorf("%s is neither a stub nor a workflow: %q", rel, body)
		}
	}
}
