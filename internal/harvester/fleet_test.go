package harvester

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTopology places a topology document below a fresh root and returns that root.
func writeTopology(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(FleetTopologyFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write topology: %v", err)
	}
	return root
}

// TestLoadFleetTopologyPositive is the positive dimension: a well-formed document is
// returned with its orgs and archetype membership, and archetype names sort stably so two
// reports of an unchanged topology are byte-identical.
func TestLoadFleetTopologyPositive(t *testing.T) {
	root := writeTopology(t, "orgs:\n  - exampleOrg\n  - otherOrg\narchetypes:\n  library-client:\n    - example-client\n  framework:\n    - example-framework\n    - second-framework\n")

	topology, err := LoadFleetTopology(t.Context(), root)
	if err != nil {
		t.Fatalf("load topology: %v", err)
	}
	if len(topology.Orgs) != 2 || topology.Orgs[0] != "exampleOrg" {
		t.Errorf("unexpected orgs: %v", topology.Orgs)
	}
	names := topology.ArchetypeNames()
	if len(names) != 2 || names[0] != "framework" || names[1] != "library-client" {
		t.Errorf("archetype names are not sorted: %v", names)
	}
	if got := topology.Archetypes["framework"]; len(got) != 2 || got[1] != "second-framework" {
		t.Errorf("unexpected framework members: %v", got)
	}
}

// TestLoadFleetTopologyAbsentIsNotAFailure is the boundary dimension: an engine checkout
// carries no operational configuration, which callers report as not configured. It must
// not be an error, and it must not yield a substitute fleet.
func TestLoadFleetTopologyAbsentIsNotAFailure(t *testing.T) {
	topology, err := LoadFleetTopology(t.Context(), t.TempDir())
	if !errors.Is(err, ErrFleetTopologyAbsent) {
		t.Fatalf("expected ErrFleetTopologyAbsent, got %v", err)
	}
	if topology != nil {
		t.Errorf("expected no topology, got %+v", topology)
	}
}

// TestLoadFleetTopologyEmptyDocument is the boundary dimension for a file that exists but
// declares nothing: it is reported as absent rather than as an empty fleet, so an operator
// who truncated the file is told to configure it.
func TestLoadFleetTopologyEmptyDocument(t *testing.T) {
	root := writeTopology(t, "")
	if _, err := LoadFleetTopology(t.Context(), root); !errors.Is(err, ErrFleetTopologyAbsent) {
		t.Fatalf("expected ErrFleetTopologyAbsent for an empty document, got %v", err)
	}
}

// TestLoadFleetTopologyRejectsUnknownField is the negative dimension: a misspelled key
// must fail loudly instead of silently producing an empty section.
func TestLoadFleetTopologyRejectsUnknownField(t *testing.T) {
	root := writeTopology(t, "orgs:\n  - exampleOrg\narchetype:\n  framework:\n    - example\n")
	_, err := LoadFleetTopology(t.Context(), root)
	if err == nil {
		t.Fatal("expected an error for an unknown field")
	}
	if !strings.Contains(err.Error(), "decode fleet topology") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestLoadFleetTopologyRejectsSecondDocument is the negative dimension for a stream that
// carries more than the single document the contract allows.
func TestLoadFleetTopologyRejectsSecondDocument(t *testing.T) {
	root := writeTopology(t, "orgs:\n  - exampleOrg\n---\norgs:\n  - shadowOrg\n")
	_, err := LoadFleetTopology(t.Context(), root)
	if err == nil || !strings.Contains(err.Error(), "exactly one document") {
		t.Fatalf("expected a single-document error, got %v", err)
	}
}

// TestLoadFleetTopologyRejectsEmptyNames is the negative dimension: an entry without a
// name would print as a blank line in the report, so it is refused at load time.
func TestLoadFleetTopologyRejectsEmptyNames(t *testing.T) {
	root := writeTopology(t, "orgs:\n  - \"\"\n")
	if _, err := LoadFleetTopology(t.Context(), root); err == nil {
		t.Fatal("expected an error for an empty organisation name")
	}

	root = writeTopology(t, "archetypes:\n  framework:\n    - \"\"\n")
	if _, err := LoadFleetTopology(t.Context(), root); err == nil {
		t.Fatal("expected an error for an empty repository name")
	}
}

// TestLoadFleetTopologyRefusesSymlink is the negative dimension for a hostile checkout: a
// topology that is a link to a file outside the repository is never parsed.
func TestLoadFleetTopologyRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("orgs:\n  - leaked\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(FleetTopologyFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := LoadFleetTopology(t.Context(), root); err == nil {
		t.Fatal("expected a refusal for a symlinked topology")
	}
}
