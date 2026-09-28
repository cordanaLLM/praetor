// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// TestPlanLanguagesFollowsDetectedRuntimes: Positive: Go, Cargo, Python and native builds map
// to their languages, several at once. Negative: a runtime no clause names (Node, .NET) is the
// known "other" set, never Go. Boundary: no runtime, or no plan, is the unknown set.
func TestPlanLanguagesFollowsDetectedRuntimes(t *testing.T) {
	cases := []struct {
		runtimes []string
		want     hisscatalog.Language
	}{
		{[]string{"go", "cargo"}, hisscatalog.LanguageGo | hisscatalog.LanguageRust},
		{[]string{"python", "meson.build"}, hisscatalog.LanguagePython | hisscatalog.LanguageC},
		{[]string{"CMakeLists.txt"}, hisscatalog.LanguageC},
		{[]string{"node", "dotnet"}, hisscatalog.LanguageOther},
		{[]string{}, 0},
	}
	for _, tc := range cases {
		if got := planLanguages(&VerificationPlan{Runtimes: tc.runtimes}); got != tc.want {
			t.Errorf("planLanguages(%q) = %b, want %b", tc.runtimes, got, tc.want)
		}
	}
	if got := planLanguages(nil); got != 0 {
		t.Errorf("planLanguages(nil) = %b", got)
	}
}

// TestHarnessComplexityRowStatesTheEnforcedLimit: the HISS-04 row names the function length the
// repository's audit enforces once the policy is resolved (a stricter policy included), and the
// audit ceiling with its note before that (#68).
func TestHarnessComplexityRowStatesTheEnforcedLimit(t *testing.T) {
	s := &adoptSession{verification: truthPlan}
	if facts := s.hissFacts(); facts.MaxFuncLOC != 0 || facts.Languages != hisscatalog.LanguageGo {
		t.Fatalf("unresolved facts = %+v", facts)
	}
	s.policy = &config.EffectivePolicy{Policy: config.ResolvedPolicy{Complexity: config.ComplexityPolicy{MaxFuncLOC: 50}}}
	facts := adoptedFacts("", "fixture", "framework", truthPlan)
	facts.hiss = s.hissFacts()
	harness, err := buildAgentHarness(facts)
	if err != nil {
		t.Fatal(err)
	}
	row := invariantRows(harness)[3]
	if !strings.HasPrefix(row, "| **HISS-04** ") || !strings.Contains(row, "; func LOC <= 50 |") {
		t.Errorf("HISS-04 row does not state the enforced limit: %s", row)
	}
	unresolved := adoptedFacts("", "fixture", "framework", truthPlan)
	harness, err = buildAgentHarness(unresolved)
	if err != nil {
		t.Fatal(err)
	}
	if row := invariantRows(harness)[3]; !strings.Contains(row, "func LOC <= 60 (audit ceiling; stricter repository policy wins)") {
		t.Errorf("unresolved HISS-04 row: %s", row)
	}
}

// TestRepositoryLanguages reads languages from project markers as adoption does. Positive: a
// Go module beside a Cargo crate is Go + Rust. Negative: a Node project is the known "other"
// set, never Go. Boundary: a repository without any marker is the unknown set, and an
// unreadable root is an error, not a guessed set. Every set comes with the audit ceiling and no
// resolved function length, since none of these repositories carries a manifest.
func TestRepositoryLanguages(t *testing.T) {
	both := t.TempDir()
	mustWrite(t, filepath.Join(both, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	mustWrite(t, filepath.Join(both, "Cargo.toml"), "[package]\nname = \"widget\"\nversion = \"0.1.0\"\n")
	node := t.TempDir()
	mustWrite(t, filepath.Join(node, "package.json"), "{\"name\": \"widget\", \"scripts\": {\"build\": \"tsc\", \"test\": \"vitest\"}}\n")
	cases := map[string]struct {
		root string
		want hisscatalog.Language
	}{
		"go and rust": {both, hisscatalog.LanguageGo | hisscatalog.LanguageRust},
		"node":        {node, hisscatalog.LanguageOther},
		"no marker":   {t.TempDir(), 0},
	}
	for name, tc := range cases {
		want := hisscatalog.Facts{Languages: tc.want, CeilingFuncLOC: config.AuditMaxFuncLOC}
		if got, warnings, err := RepositoryHISSFacts(t.Context(), tc.root, nil); err != nil || got != want || len(warnings) != 0 {
			t.Errorf("%s: RepositoryHISSFacts = %+v, %q, %v; want %+v", name, got, warnings, err, want)
		}
	}
	if _, _, err := RepositoryHISSFacts(t.Context(), filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Error("missing root read as a language set")
	}
}

// cleanupGotoManifest declares acme/widget on native-gpu-systems with the cleanup-goto exception
// recorded in cleanupGotoDocument at docs/cleanup-goto.md.
const (
	cleanupGotoManifest = "version: 1\nrepository:\n  owner: acme\n  name: widget\nprofiles:\n  - native-gpu-systems\n" +
		"hiss:\n  exceptions:\n    c_goto_cleanup: docs/cleanup-goto.md\n"
	cleanupGotoDocument = "# Cleanup goto\n\nOne forward jump to the cleanup label.\n"
)

// cleanupGotoRepo writes markers and cleanupGotoManifest into repo, and cleanupGotoDocument too
// when documented, and returns repo.
func cleanupGotoRepo(t *testing.T, repo string, markers map[string]string, documented bool) string {
	t.Helper()
	for rel, body := range markers {
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(rel)), body)
	}
	mustWrite(t, filepath.Join(repo, manifestFile), cleanupGotoManifest)
	if documented {
		mustWrite(t, filepath.Join(repo, "docs", "cleanup-goto.md"), cleanupGotoDocument)
	}
	return repo
}

// cMarkers mark a native C build.
var cMarkers = map[string]string{"meson.build": "project('widget', 'c')\n"}

// TestRepositoryHISSFactsReadsDeclaredExceptions: Positive: a C repository whose manifest names
// an existing document for its cleanup-goto exception declares it. Negative: a declaration
// whose document is missing is not honoured and a warning names it; a malformed manifest is an
// error, never an empty declaration. Boundary: a manifest without a lock states the audit
// ceiling, as the audit cannot resolve its policy yet.
func TestRepositoryHISSFactsReadsDeclaredExceptions(t *testing.T) {
	facts, warnings, err := RepositoryHISSFacts(t.Context(), cleanupGotoRepo(t, t.TempDir(), cMarkers, true), nil)
	want := hisscatalog.Facts{Languages: hisscatalog.LanguageC, CeilingFuncLOC: config.AuditMaxFuncLOC, Exceptions: hisscatalog.ExceptionCleanupGoto}
	if err != nil || facts != want || len(warnings) != 0 {
		t.Fatalf("documented exception: %+v, %q, %v; want %+v", facts, warnings, err, want)
	}
	facts, warnings, err = RepositoryHISSFacts(t.Context(), cleanupGotoRepo(t, t.TempDir(), cMarkers, false), nil)
	if err != nil || facts.Exceptions != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "docs/cleanup-goto.md") {
		t.Fatalf("undocumented exception: %+v, %q, %v; want no exception and one warning naming the document", facts, warnings, err)
	}
	broken := cleanupGotoRepo(t, t.TempDir(), cMarkers, true)
	mustWrite(t, filepath.Join(broken, manifestFile), "version: 1\nhiss:\n  exceptions:\n    unknown_exception: docs/x.md\n")
	if _, _, err := RepositoryHISSFacts(t.Context(), broken, nil); err == nil {
		t.Fatal("malformed manifest read as no exception")
	}
}
