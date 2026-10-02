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

// linters is the key #353 names: the manifest must keep saying nothing runs it until a
// consumer exists, and the test above then fails until the entry changes state.
func TestShippedManifestRecordsLintersUnconsumed(t *testing.T) {
	manifest, err := archetypecoverage.Load(t.Context(), repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range manifest.Fields {
		if field.Key == "linters" {
			if field.State != archetypecoverage.StateUnconsumed || field.Reason == "" {
				t.Fatalf("linters entry = %+v, want unconsumed with a reason", field)
			}
			return
		}
	}
	t.Fatal("the manifest binds no linters entry")
}
