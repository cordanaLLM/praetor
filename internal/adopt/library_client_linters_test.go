// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestLibraryClientLintersAreFilteredByLanguage(t *testing.T) {
	declared := []config.DeclaredLinter{
		{Name: "govet"}, {Name: "golangci-lint"}, {Name: "clippy"}, {Name: "semgrep"},
	}
	applicable, skipped := filterApplicableLinters(declared, repoLinterInputs{hasCargoToml: true})
	if !slices.Equal(linterNames(applicable), []string{"clippy", "semgrep"}) {
		t.Fatalf("Rust linter requirements = %v, want clippy and semgrep", linterNames(applicable))
	}
	if len(skipped) != 2 {
		t.Fatalf("Go-only linters were not recorded as not applicable: %v", skipped)
	}
	applicable, skipped = filterApplicableLinters(declared, repoLinterInputs{hasGoSources: true})
	if !slices.Equal(linterNames(applicable), []string{"golangci-lint", "govet", "semgrep"}) {
		t.Fatalf("Go linter requirements = %v, want govet, golangci-lint, and semgrep", linterNames(applicable))
	}
	if len(skipped) != 1 || skipped[0] != "[SKIP] clippy: not applicable, no Cargo.toml" {
		t.Fatalf("Rust-only linter was not recorded as not applicable: %v", skipped)
	}
}

func linterNames(linters []config.DeclaredLinter) []string {
	names := make([]string, 0, len(linters))
	for _, linter := range linters {
		names = append(names, linter.Name)
	}
	slices.Sort(names)
	return names
}
