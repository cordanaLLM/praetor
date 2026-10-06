// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package archetypecoverage_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/archetypecoverage"
	"github.com/cordanaLLM/praetor/internal/config"
)

// repositoryRoot is the module root this package sits two levels below.
var repositoryRoot = filepath.Join("..", "..")

// The shipped manifest must match what the engine's source reads, in both directions: every
// schema key bound, every listed consumer reading its key, every reader listed (#353).
func TestShippedManifestMatchesTheSource(t *testing.T) {
	manifest, err := archetypecoverage.Load(t.Context(), repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	report, err := archetypecoverage.Verify(t.Context(), archetypecoverage.Options{
		Root: repositoryRoot, Manifest: manifest, Keys: config.ArchetypeKeys(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed() {
		t.Fatalf("%s does not match the source (%d finding(s)):\n  %s", archetypecoverage.ManifestFile,
			len(report.Findings), strings.Join(report.Findings, "\n  "))
	}
	if report.Keys != len(manifest.Fields) || report.Readers == 0 {
		t.Fatalf("the check read nothing: %+v", report)
	}
}

// linters is the key #353 named as read by nothing. Its one consumer is the audit's clang-tidy
// translation-unit coverage gate (#778), which runs when the list names clang-tidy; the
// manifest must record exactly that consumer acting on it, so a second reader, or the gate
// dropping its read, fails the test above until the entry changes with it.
func TestShippedManifestRecordsLintersConsumedByTheTidyCoverageGate(t *testing.T) {
	manifest, err := archetypecoverage.Load(t.Context(), repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range manifest.Fields {
		if field.Key == "linters" {
			if field.State != archetypecoverage.StateConsumed || len(field.Consumers) != 1 ||
				field.Consumers[0].Func != "cmd/standardsctl.auditTidyCoverage" {
				t.Fatalf("linters entry = %+v, want consumed by cmd/standardsctl.auditTidyCoverage alone", field)
			}
			return
		}
	}
	t.Fatal("the manifest binds no linters entry")
}
