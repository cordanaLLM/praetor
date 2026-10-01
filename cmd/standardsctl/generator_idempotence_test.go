// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/needs"
)

// plantStamp rewrites the first match of pattern in path to stamp and returns the new bytes.
func plantStamp(t *testing.T, path string, pattern *regexp.Regexp, stamp string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	planted := pattern.ReplaceAll(data, []byte(stamp))
	if string(planted) == string(data) {
		t.Fatalf("%s carries no %s to plant", path, pattern)
	}
	if err := os.WriteFile(path, planted, 0o600); err != nil {
		t.Fatal(err)
	}
	return planted
}

// requireBytes fails unless path holds exactly want.
func requireBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s was rewritten:\n%s\nwant:\n%s", path, got, want)
	}
}

// The debt baseline as a generated artefact (#696): positive, a rescan finding the same debt
// keeps the file byte for byte, its earlier generated_at and increase rationale included;
// negative, a rescan finding other debt rewrites it; boundary, the first record of an absent
// baseline writes it.
func TestBaselineRecord_3D_UnchangedDebtKeepsTheFile(t *testing.T) {
	f := newAuditFixture(t)
	f.addViolation(t)
	if out, err := runBaselineCmd(t, f, "--record", "--allow-increase", "--reason=legacy inventory"); err != nil {
		t.Fatalf("first record: %v\n%s", err, out)
	}
	planted := plantStamp(t, f.baselinePath, regexp.MustCompile(`"generated_at": "[^"]*"`), `"generated_at": "2000-01-01T00:00:00Z"`)
	out, err := runBaselineCmd(t, f, "--record")
	if err != nil {
		t.Fatalf("rescan of the same debt: %v\n%s", err, out)
	}
	mustContain(t, out, "Baseline unchanged:", "kept the recorded file")
	requireBytes(t, f.baselinePath, planted)
	if !strings.Contains(string(planted), `"increase_rationale": "legacy inventory"`) {
		t.Fatalf("the kept baseline must keep its rationale:\n%s", planted)
	}
	if err := os.Remove(filepath.Join(f.dir, "legacy.go")); err != nil {
		t.Fatal(err)
	}
	if out, err = runBaselineCmd(t, f, "--record"); err != nil || !strings.Contains(out, "Total: 0 infractions, previously 1") {
		t.Fatalf("a changed debt must be recorded: %v\n%s", err, out)
	}
	if err := os.Remove(f.baselinePath); err != nil {
		t.Fatal(err)
	}
	if out, err = runBaselineCmd(t, f, "--record"); err != nil || !strings.Contains(out, "Baseline successfully updated") {
		t.Fatalf("an absent baseline must be written: %v\n%s", err, out)
	}
}

// .needs.yaml as a generated artefact (#696): positive, a rescan that matches the committed
// manifest apart from updated_at keeps it byte for byte; negative, a manifest that drifted is
// rewritten; boundary, an absent manifest is written.
func TestNeedsScanWrite_3D_UnchangedManifestKeepsTheFile(t *testing.T) {
	repo := newNeedsRepo(t)
	write := func() (string, error) {
		return captureStdout(t, func() error { return dispatchCommand("needs", []string{"scan", "--write", "--path=" + repo}) })
	}
	if out, err := write(); err != nil || !strings.Contains(out, "Wrote") {
		t.Fatalf("an absent manifest must be written: %v\n%s", err, out)
	}
	manifest := filepath.Join(repo, needs.NeedsManifestName)
	planted := plantStamp(t, manifest, regexp.MustCompile(`updated_at: .*`), "updated_at: 2000-01-01T00:00:00Z")
	out, err := write()
	if err != nil || !strings.Contains(out, "kept the recorded file") {
		t.Fatalf("an unchanged scan: %v\n%s", err, out)
	}
	requireBytes(t, manifest, planted)
	drifted := strings.Replace(string(planted), "    basis: not-configured\n", "", 1)
	if err := os.WriteFile(manifest, []byte(drifted), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err = write(); err != nil || !strings.Contains(out, "Wrote") {
		t.Fatalf("a drifted manifest must be rewritten: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(manifest); !strings.Contains(string(got), "basis: not-configured") {
		t.Fatalf("the rewrite lost the scan:\n%s", got)
	}
}
