package needs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// checkedManifest is a manifest as a scan builds it, stamped with when.
func checkedManifest(when time.Time) *RepoNeeds {
	return &RepoNeeds{
		Version:    1,
		Repository: "github.com/acme/widgets",
		Language:   "go",
		Languages:  []string{"go"},
		GoVersion:  "1.27",
		Framework:  "github.com/golusoris/golusoris",
		Capabilities: CapabilityDeclaration{
			Required: []CapabilityKey{"config.yaml"},
		},
		Dependencies: []DependencyDemand{{
			Package: "gopkg.in/yaml.v3", Version: "v3.0.1", Language: "go", Ecosystem: "go",
			Capability: "config.yaml", Status: StatusCovered,
		}},
		Readiness: ReadinessMetrics{Basis: FrameworkCatalogDeclared, Score: 100, TotalThirdPartyDeps: 1, CoveredDeps: 1},
		UpdatedAt: when,
	}
}

// writtenManifest returns the bytes WriteNeedsManifest commits for m.
func writtenManifest(t *testing.T, m *RepoNeeds) []byte {
	t.Helper()
	dir := t.TempDir()
	if err := WriteNeedsManifest(dir, m); err != nil {
		t.Fatalf("WriteNeedsManifest: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, NeedsManifestName))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Positive: a manifest written by the generator matches a later scan of the same tree,
// which stamps a different updated_at.
func TestManifestDriftIgnoresOnlyUpdatedAt(t *testing.T) {
	committed := writtenManifest(t, checkedManifest(time.Date(2026, 9, 11, 0, 47, 27, 837318013, time.UTC)))
	drift, err := ManifestDrift(committed, checkedManifest(time.Now().UTC()))
	if err != nil || drift != "" {
		t.Fatalf("regenerated manifest drifted: %q, %v", drift, err)
	}
}

// Negative: a committed manifest missing a field the generator now writes, or recording an
// outdated dependency status, is drift, and the report names the lines on both sides.
func TestManifestDriftReportsStaleFieldsAndDependencies(t *testing.T) {
	stale := checkedManifest(time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC))
	stale.Readiness.Basis = ""
	stale.Dependencies[0].Status = StatusGap
	committed := writtenManifest(t, stale)

	drift, err := ManifestDrift(committed, checkedManifest(time.Now().UTC()))
	if err != nil {
		t.Fatalf("ManifestDrift: %v", err)
	}
	for _, want := range []string{"- committed:", "status: gap", "+ generated:", "status: covered", "basis: catalog-declared"} {
		if !strings.Contains(drift, want) {
			t.Errorf("drift report lacks %q:\n%s", want, drift)
		}
	}
	if strings.Contains(drift, "updated_at") {
		t.Errorf("drift report names updated_at, which the check ignores:\n%s", drift)
	}
}

// Boundary: CRLF endings are the same manifest, a reordered manifest is still drift with a
// report, a long drift is truncated with a count, and unparseable or absent input fails.
func TestManifestDriftBoundaries(t *testing.T) {
	fresh := checkedManifest(time.Now().UTC())
	committed := writtenManifest(t, checkedManifest(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	crlf := []byte(strings.ReplaceAll(string(committed), "\n", "\r\n"))
	if drift, err := ManifestDrift(crlf, fresh); err != nil || drift != "" {
		t.Errorf("CRLF checkout drifted: %q, %v", drift, err)
	}

	lines := strings.Split(strings.TrimSuffix(string(committed), "\n"), "\n")
	lines[0], lines[1] = lines[1], lines[0]
	if drift, err := ManifestDrift([]byte(strings.Join(lines, "\n")+"\n"), fresh); err != nil || !strings.Contains(drift, "line order") {
		t.Errorf("reordered manifest: %q, %v", drift, err)
	}

	many := checkedManifest(time.Now().UTC())
	for i := 0; i < maxManifestDriftLines; i++ {
		many.Capabilities.Optional = append(many.Capabilities.Optional, CapabilityKey("cap.extra"+strings.Repeat("x", i)))
	}
	drift, err := ManifestDrift(committed, many)
	if err != nil || !strings.Contains(drift, "more line(s)") || strings.Count(drift, "\n") != maxManifestDriftLines {
		t.Errorf("long drift not truncated to %d lines: %v\n%s", maxManifestDriftLines, err, drift)
	}

	if _, err := ManifestDrift([]byte("version: [\n"), fresh); err == nil {
		t.Error("unparseable manifest accepted")
	}
	if _, err := ManifestDrift(committed, nil); err == nil {
		t.Error("nil fresh manifest accepted")
	}
}

// Positive, negative and boundary for the file-level check: a committed manifest is read
// from the repository, an absent one is ErrNeedsManifestMissing, and a nil context fails.
func TestCheckNeedsManifest(t *testing.T) {
	dir := t.TempDir()
	if _, err := CheckNeedsManifest(context.Background(), dir, checkedManifest(time.Now())); !errors.Is(err, ErrNeedsManifestMissing) {
		t.Fatalf("absent manifest: %v, want ErrNeedsManifestMissing", err)
	}
	if err := WriteNeedsManifest(dir, checkedManifest(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	if drift, err := CheckNeedsManifest(context.Background(), dir, checkedManifest(time.Now().UTC())); err != nil || drift != "" {
		t.Fatalf("committed manifest: %q, %v", drift, err)
	}
	var absent context.Context
	if _, err := CheckNeedsManifest(absent, dir, checkedManifest(time.Now())); err == nil {
		t.Error("nil context accepted")
	}
}
