package main

import (
	"os"
	"strings"
	"testing"
)

// manualEdgeConfig is the dogfood shape: edge is declared manual, bleeding follows main.
const manualEdgeConfig = `version: 1
flavors:
  bleeding: {source_ref: "refs/heads/main", update_frequency: on_push, stability: experimental}
  edge:     {source_ref: "refs/heads/main", update_frequency: manual, stability: pre-release}
`

func newManualEdgeFixture(t *testing.T) flavorFixture {
	t.Helper()
	f := newFlavorFixture(t)
	if err := os.WriteFile(f.config, []byte(manualEdgeConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRunFlavorsSync_Positive_ManualFlavorMovesOnlyWhenNamed(t *testing.T) {
	f := newManualEdgeFixture(t)

	out, err := f.sync(t)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if got := f.tagAt(t, f.repo, "bleeding"); got != f.head {
		t.Errorf("on_push bleeding = %q, want main %q", got, f.head)
	}
	if got := f.tagAt(t, f.repo, "edge"); got != "" {
		t.Errorf("manual edge must stay absent until named, got %q", got)
	}
	mustContain(t, out, "Held edge: update_frequency manual", "1 tag(s) moved, 0 already current, 0 pending, 1 held")

	out, err = f.sync(t, "--flavor=edge")
	if err != nil {
		t.Fatalf("sync --flavor=edge: %v\n%s", err, out)
	}
	if got := f.tagAt(t, f.repo, "edge"); got != f.head {
		t.Errorf("named edge = %q, want main %q", got, f.head)
	}
	mustContain(t, out, "Updated tag edge", "1 tag(s) moved, 0 already current, 0 pending, 0 held")
	if strings.Contains(out, "bleeding") {
		t.Errorf("a named sync plans only the named flavors:\n%s", out)
	}
}

func TestRunFlavorsSync_Negative_UndeclaredFlavorAndFrequencyRefused(t *testing.T) {
	f := newManualEdgeFixture(t)
	if out, err := f.sync(t, "--flavor=egde"); err == nil || !strings.Contains(err.Error(), "egde") {
		t.Fatalf("a misspelled --flavor must fail naming it, got %v\n%s", err, out)
	}
	if got := f.tagAt(t, f.repo, "bleeding"); got != "" {
		t.Errorf("a refused sync moved bleeding to %q", got)
	}
	if err := os.WriteFile(f.config, []byte("version: 1\nflavors:\n  edge: {update_frequency: hourly}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := f.sync(t); err == nil || !strings.Contains(err.Error(), "unknown update_frequency") {
		t.Fatalf("an unknown frequency must fail at load, got %v\n%s", err, out)
	}
}

func TestRunFlavorsPlan_Boundary_ReportsFrequencyAndStability(t *testing.T) {
	f := newManualEdgeFixture(t)
	out, err := captureStdout(t, func() error { return runFlavors([]string{"plan", "--config=" + f.config, "--dir=" + f.repo}) })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "[HELD] edge", "update: manual; stability: pre-release", "update: on_push; stability: experimental")

	undeclared := "version: 1\nflavors:\n  bare: {source_ref: \"refs/heads/main\"}\n"
	if err := os.WriteFile(f.config, []byte(undeclared), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return runFlavors([]string{"plan", "--config=" + f.config, "--dir=" + f.repo}) })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "update: automatic; stability: undeclared")
}
