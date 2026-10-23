// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/readmepresentation"
)

const separatedReadmeFile = ".readme-baseline.json"

func TestPlantedMixedBaselineRefusal(t *testing.T) {
	f := newAuditFixture(t)
	f.writeBaseline(t, []baseline.Infraction{{RuleID: "HISS-01"}, {RuleID: "README-01"}}, "")
	_, loadErr := baseline.LoadBaseline(f.baselinePath)
	_, auditErr := f.audit(t, "--offline")
	for _, result := range []struct {
		name string
		err  error
	}{{"loader", loadErr}, {"audit", auditErr}} {
		if result.err == nil {
			t.Errorf("%s accepted mixed HISS/README baseline; want refusal naming praetorctl adopt", result.name)
			continue
		}
		for _, want := range []string{f.baselinePath, "HISS", "README", "README-01", "praetorctl adopt"} {
			if !strings.Contains(result.err.Error(), want) {
				t.Errorf("%s refusal lacks %q: %v", result.name, want, result.err)
			}
		}
	}
}

func plantMigrationBaseline(t *testing.T, repo string) string {
	t.Helper()
	raw := `{"version":2,"repository":"example/adopted","commit_sha":"abc","generated_at":"2026-01-01T00:00:00Z","total_infractions":7,"infractions":[{"rule_id":"HISS-01","anchor":"fn:a","fingerprint":"h1"},{"rule_id":"HISS-07","fingerprint":"h2"},{"rule_id":"G101","fingerprint":"h3"},{"rule_id":"README-01","anchor":"file:x","fingerprint":"r1"},{"rule_id":"README-02","fingerprint":"r2"}]}`
	writeFixtureFile(t, repo, cliBaseline, raw)
	return raw
}

func TestPlantedReadmeMigration(t *testing.T) {
	repo, source := adoptSuccessLineFixture(t, map[string]string{"README.md": "# Widgets\n"})
	plantMigrationBaseline(t, repo)
	out, err := adoptWithFlags(t, repo, source, "--record-baseline=false")
	if err != nil {
		t.Fatalf("migration: %v\n%s", err, out)
	}
	checkMigratedBaseline(t, repo, cliBaseline, 5, 3)
	checkMigratedBaseline(t, repo, separatedReadmeFile, 2, 2)
	mustContain(t, out, "Moved 2 README entries from .standards-baseline.json to .readme-baseline.json")
	hiss, readme := readFixtureFile(t, repo, cliBaseline), readFixtureFile(t, repo, separatedReadmeFile)
	if _, err := adoptWithFlags(t, repo, source, "--record-baseline=false"); err != nil {
		t.Fatal(err)
	}
	if hiss != readFixtureFile(t, repo, cliBaseline) || readme != readFixtureFile(t, repo, separatedReadmeFile) {
		t.Fatal("second migration changed bytes")
	}
}

func TestPlantedReadmeMigrationDryRun(t *testing.T) {
	repo, source := adoptSuccessLineFixture(t, map[string]string{"README.md": "# Widgets\n"})
	raw := plantMigrationBaseline(t, repo)
	out, err := adoptWithFlags(t, repo, source, "--dry-run", "--record-baseline=false")
	if err != nil {
		t.Fatalf("dry migration: %v\n%s", err, out)
	}
	if readFixtureFile(t, repo, cliBaseline) != raw {
		t.Fatal("dry run changed source baseline")
	}
	if _, err := os.Stat(filepath.Join(repo, separatedReadmeFile)); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote README baseline: %v", err)
	}
	mustContain(t, out, "Would move 2 README entries from .standards-baseline.json to .readme-baseline.json")
}

func checkMigratedBaseline(t *testing.T, repo, file string, total, entries int) {
	t.Helper()
	b, err := baseline.LoadBaseline(filepath.Join(repo, file))
	if err != nil {
		t.Fatal(err)
	}
	if b.Absent || b.TotalInfractions != total || len(b.Infractions) != entries {
		t.Fatalf("%s migration: %+v %v; want total=%d entries=%d", file, b, err, total, entries)
	}
	want := []baseline.Infraction{{RuleID: "HISS-01", Anchor: "fn:a", Fingerprint: "h1"}, {RuleID: "HISS-07", Fingerprint: "h2"}, {RuleID: "G101", Fingerprint: "h3"}}
	if file == separatedReadmeFile {
		want = []baseline.Infraction{{RuleID: "README-01", Anchor: "file:x", Fingerprint: "r1"}, {RuleID: "README-02", Fingerprint: "r2"}}
	}
	if !reflect.DeepEqual(b.Infractions, want) {
		t.Fatalf("entry identity changed in %s: %+v", file, b.Infractions)
	}
	checkMigratedMetadata(t, b)
}

func TestPlantedReadmeMigrationConflict(t *testing.T) {
	repo, source := adoptSuccessLineFixture(t, map[string]string{"README.md": "# Widgets\n"})
	raw := plantMigrationBaseline(t, repo)
	existing := `{"version":1,"total_infractions":0,"infractions":[]}`
	writeFixtureFile(t, repo, separatedReadmeFile, existing)
	out, err := adoptWithFlags(t, repo, source, "--record-baseline=false")
	if err == nil {
		t.Fatalf("adopt accepted conflicting baselines: %s", out)
	}
	mustContain(t, err.Error(), cliBaseline, separatedReadmeFile)
	if readFixtureFile(t, repo, cliBaseline) != raw || readFixtureFile(t, repo, separatedReadmeFile) != existing {
		t.Fatal("conflict changed baselines")
	}
}

type parityVerdict struct {
	File     string `json:"file"`
	Recorded int    `json:"recorded"`
	Active   int    `json:"active"`
	Passed   bool   `json:"passed"`
}

func TestReadmeBaselineFileParity(t *testing.T) {
	for _, name := range []string{"HISS only", "README only", "both", "legacy migrated"} {
		t.Run(name, func(t *testing.T) { checkBaselineFileParity(t, name) })
	}
}

func checkBaselineFileParity(t *testing.T, name string) {
	t.Helper()
	f := newAuditFixture(t)
	source := f.dir
	readme := "# Widgets\n"
	if name == "HISS only" {
		readme = "# Widgets\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n**Tag**\n"
	}
	writeFixtureFile(t, f.dir, "README.md", readme)
	writeFixtureFile(t, f.dir, ".standards.yaml", readFixtureFile(t, f.dir, ".standards.yaml")+"\nadoption:\n  decline: [dev-container, readme]\ngate:\n  readme_presentation: true\n")
	if name != "README only" {
		f.addViolation(t)
	}
	if out, err := runBaselineCmd(t, f, "--record", "--allow-increase", "--reason=fixture debt"); err != nil {
		t.Fatalf("record: %v\n%s", err, out)
	}
	if name == "legacy migrated" {
		plantParityMixedFile(t, f)
	}
	rep, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{Path: f.dir, LockSourceRoot: source, RecordBaseline: true, SkipHookActivation: true})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	verdicts := parityReportVerdicts(t, rep)
	assertExpectedParityCounts(t, name, verdicts)
	assertFixtureParity(t, f, verdicts)
}

func assertFixtureParity(t *testing.T, f *auditFixture, verdicts []parityVerdict) {
	t.Helper()
	auditOut, err := f.audit(t, "--offline")
	if err != nil {
		t.Fatalf("audit: %v\n%s", err, auditOut)
	}
	verify, err := runBaselineCmd(t, f, "--verify")
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, verify)
	}
	assertParityCounts(t, verdicts, auditOut, verify)
}

func plantParityMixedFile(t *testing.T, f *auditFixture) {
	t.Helper()
	h, err := baseline.LoadBaseline(f.baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := readmepresentation.CheckFile(t.Context(), f.dir, "README.md")
	if err != nil {
		t.Fatal(err)
	}
	h.Infractions = append(h.Infractions, readmepresentation.ToBaseline(findings)...)
	if err := baseline.SaveBaseline(f.baselinePath, h); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.dir, separatedReadmeFile)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func assertParityCounts(t *testing.T, verdicts []parityVerdict, audit, verify string) {
	t.Helper()
	for _, v := range verdicts {
		if !v.Passed || v.Active != v.Recorded {
			t.Errorf("adopt per-file counts: %+v", v)
		}
		for _, surface := range []struct{ name, text string }{{"audit", audit}, {"verify", verify}} {
			assertParityLine(t, v, surface.name, surface.text)
		}
	}
}

func checkMigratedMetadata(t *testing.T, b *baseline.Baseline) {
	t.Helper()
	if b.Version != 2 || b.CommitSHA != "abc" || b.Repository != "example/adopted" || b.GeneratedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("metadata lost: %+v", b)
	}
	if b.Infractions[0].Fingerprint == "" || b.Infractions[0].Anchor == "" {
		t.Fatal("migration lost fingerprint or anchor")
	}
}

func parityReportVerdicts(t *testing.T, rep *adopt.AdoptReport) []parityVerdict {
	t.Helper()
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Baselines []parityVerdict `json:"baseline_ratchets"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Baselines) != 2 {
		t.Fatalf("adopt JSON lacks two per-file verdicts; got %d", len(report.Baselines))
	}
	return report.Baselines
}

func assertParityLine(t *testing.T, v parityVerdict, name, text string) {
	t.Helper()
	line := ""
	for _, candidate := range strings.Split(text, "\n") {
		if strings.Contains(candidate, v.File) && strings.Contains(candidate, "active") && strings.Contains(candidate, " recorded") {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Errorf("%s lacks per-file active count for %s: %s", name, v.File, text)
		return
	}
	wantActive := fmt.Sprintf("%d active", v.Active)
	wantRecorded := fmt.Sprintf("%d recorded", v.Recorded)
	if !strings.Contains(line, wantActive) || !strings.Contains(line, wantRecorded) {
		t.Errorf("%s %s disagrees with adopt %+v: %s", name, v.File, v, line)
	}
}

func assertExpectedParityCounts(t *testing.T, name string, verdicts []parityVerdict) {
	t.Helper()
	wanted := map[string]int{cliBaseline: 1, separatedReadmeFile: 1}
	switch name {
	case "HISS only":
		wanted[separatedReadmeFile] = 0
	case "README only":
		wanted[cliBaseline] = 0
	}
	for _, v := range verdicts {
		expected, ok := wanted[v.File]
		if !ok {
			t.Fatalf("unexpected or duplicate baseline verdict: %+v", v)
		}
		if v.Recorded != expected || v.Active != expected {
			t.Fatalf("%s: %+v; want recorded=active=%d", name, v, expected)
		}
		delete(wanted, v.File)
	}
}
