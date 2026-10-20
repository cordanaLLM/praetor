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
		Framework:  "example.com/acme/kit",
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

// DroppedFramework: negative, a committed manifest with framework and builder kits checked
// against a fresh scan without a framework returns ErrFrameworkNotConfigured naming both and
// the remedy; positive, a fresh scan with a framework returns nil; boundary, a committed
// manifest without a framework checked against a fresh scan without one returns nil, and
// unparsable bytes fail with a parse error.
func TestDroppedFramework_3D(t *testing.T) {
	committedWithBoth := writtenManifest(t, &RepoNeeds{
		Version: 1, Repository: "example.com/acme/widget", Language: "go",
		Framework: "example.com/acme/kit", BuilderKits: []string{"acme/kit"},
	})
	freshWithout := &RepoNeeds{Version: 1, Repository: "example.com/acme/widget", Language: "go"}

	// Negative: committed manifest with framework + builder_kits and fresh without -> ErrFrameworkNotConfigured naming both.
	err := DroppedFramework(committedWithBoth, freshWithout)
	if !errors.Is(err, ErrFrameworkNotConfigured) {
		t.Fatalf("expected ErrFrameworkNotConfigured, got %v", err)
	}
	for _, want := range []string{"example.com/acme/kit", "acme/kit", "set framework.targets.<language> in the workstation or fleet settings"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}

	// Positive: fresh with a framework -> nil.
	freshWith := &RepoNeeds{Version: 1, Repository: "example.com/acme/widget", Language: "go", Framework: "example.com/acme/kit"}
	if err := DroppedFramework(committedWithBoth, freshWith); err != nil {
		t.Fatalf("fresh with framework returned error: %v", err)
	}

	// Boundary: committed without framework and fresh without -> nil.
	committedWithout := writtenManifest(t, freshWithout)
	if err := DroppedFramework(committedWithout, freshWithout); err != nil {
		t.Fatalf("committed without framework returned error: %v", err)
	}

	// Boundary: unparsable committed bytes -> parse error; nil fresh manifest -> error.
	if err := DroppedFramework([]byte("invalid: yaml: [\n"), freshWithout); err == nil || errors.Is(err, ErrFrameworkNotConfigured) {
		t.Fatalf("unparsable committed bytes did not return parse error: %v", err)
	}
	if err := DroppedFramework(committedWithBoth, nil); err == nil {
		t.Error("nil fresh manifest accepted")
	}
}
