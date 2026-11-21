// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientjson"
	"github.com/cordanaLLM/praetor/internal/config"
)

// TestRenovateEngineAdoptionReadback adopts the engine rule and reads back which pins it excludes,
// including pins moved to the current forge owner and an unrelated action that stays eligible.
func TestRenovateEngineAdoptionReadback(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "renovate.json"), "{}\n")
	s := &adoptSession{repoPath: root, report: &AdoptReport{}}
	if err := reconcileRenovateIgnore(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	line, err := AuditRenovateEngineBumps(t.Context(), root)
	checkRepositoryVerdict(t, line, err, false, "renovate-engine-bumps")
	first := mustRead(t, filepath.Join(root, "renovate.json"))
	assertRenovateEnginePins(t, first)
	if err := reconcileRenovateIgnore(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if second := mustRead(t, filepath.Join(root, "renovate.json")); second != first {
		t.Fatal("adoption is not idempotent")
	}
}

// assertRenovateEnginePins fails the test unless the rendered rule names every engine package name.
func assertRenovateEnginePins(t *testing.T, data string) {
	t.Helper()
	manifest, err := config.LoadManifest(filepath.Join("..", "..", config.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	repository := manifest.Repository.Source
	if repository == "" {
		repository = manifest.Repository.Owner + "/" + manifest.Repository.Name
	}
	root, err := clientjson.DecodeObject([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := renovatePackageRules(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, pin string
		excluded  bool
	}{
		{"migrated repository", repository, true},
		{"migrated action", repository + "/.github/actions/praetor-adopt", true},
		{"unrelated action", repository + "-other/.github/actions/example", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			excluded := len(uncoveredRenovateValues(rules, []string{tc.pin}, "matchPackageNames")) == 0
			if excluded != tc.excluded {
				t.Errorf("pin %q excluded = %t, want %t", tc.pin, excluded, tc.excluded)
			}
		})
	}
}

func TestRenovateEngineUnsafeLaterEnable(t *testing.T) {
	root := t.TempDir()
	data, _, err := mergeRenovateEngineRule(t.Context(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// Append a real later rule; adoption must report unsafe and preserve every byte.
	text := strings.TrimSuffix(strings.TrimSpace(string(data)), "]\n}")
	text += ", {\"matchPackageNames\":[\"github.com/cordanaLLM/praetor\"],\"enabled\":true}]\n}\n"
	mustWrite(t, filepath.Join(root, "renovate.json"), text)
	line, err := AuditRenovateEngineBumps(t.Context(), root)
	checkRepositoryVerdict(t, line, err, true, "renovate-engine-bumps")
	s := &adoptSession{repoPath: root, report: &AdoptReport{}}
	if err := reconcileRenovateIgnore(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.report.Warnings) == 0 || !strings.Contains(s.report.Warnings[0], "re-enable") {
		t.Fatalf("unsafe not reported: %+v", s.report)
	}
	if got := mustRead(t, filepath.Join(root, "renovate.json")); got != text {
		t.Fatal("unsafe config was rewritten")
	}
	t.Log(s.report.Warnings[0])
}

// pinRenovateEngineClock fixes the renovate-engine-bumps audit clock for one test.
func pinRenovateEngineClock(t *testing.T, at time.Time) {
	t.Helper()
	previous := renovateEngineNow
	renovateEngineNow = func() time.Time { return at }
	t.Cleanup(func() { renovateEngineNow = previous })
}

// formerOnlyEngineRule covers the Go module and the former forge identity but not the moved one.
const formerOnlyEngineRule = `{"packageRules":[{"matchPackageNames":["github.com/cordanaLLM/praetor", "github.com/cordanaLLM/praetor/**", "github.com/cordanaLLM/praetor/cmd/standardsctl", "cordanaLLM/praetor", "cordanaLLM/praetor/**"],"enabled":false}]}`

// TestRenovateEngineMovedNameGrace passes a rule that misses only the moved names with a warning before the grace
// ends and fails it from the grace end on; a rule missing any other engine name fails at once.
func TestRenovateEngineMovedNameGrace(t *testing.T) {
	cases := []struct {
		name string
		data string
		at   time.Time
		fail bool
	}{
		{"former only, before the grace end", formerOnlyEngineRule, renovateEngineGraceUntil.Add(-24 * time.Hour), false},
		{"former only, one second before the end", formerOnlyEngineRule, renovateEngineGraceUntil.Add(-time.Second), false},
		{"former only, at the grace end", formerOnlyEngineRule, renovateEngineGraceUntil, true},
		{"moved only, former names missing", `{"packageRules":[{"matchPackageNames":["github.com/cordanaLLM/praetor", "github.com/cordanaLLM/praetor/**", "github.com/cordanaLLM/praetor/cmd/standardsctl", "tekmora/praetor", "tekmora/praetor/**"],"enabled":false}]}`, renovateEngineGraceUntil.Add(-24 * time.Hour), true},
		{"no engine rule", `{"packageRules":[]}`, renovateEngineGraceUntil.Add(-24 * time.Hour), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pinRenovateEngineClock(t, tc.at)
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "renovate.json"), tc.data)
			line, err := AuditRenovateEngineBumps(t.Context(), root)
			checkRepositoryVerdict(t, line, err, tc.fail, "renovate-engine-bumps")
			if !tc.fail && (!strings.HasPrefix(line, "[WARN] ") || !strings.Contains(line, "tekmora/praetor")) {
				t.Fatalf("grace line %q: want a [WARN] naming the moved identity", line)
			}
		})
	}
}

// TestRenovateEngineUnsafeInputs refuses malformed or incomplete rules, such as one covering only the former
// identity once the moved-name grace has ended.
func TestRenovateEngineUnsafeInputs(t *testing.T) {
	pinRenovateEngineClock(t, renovateEngineGraceUntil)
	cases := []struct{ name, file, data string }{
		{"JSONC", "renovate.jsonc", "// comment\n{}"},
		{"former identity only", "renovate.json", `{"packageRules":[{"matchPackageNames":["github.com/cordanaLLM/praetor", "github.com/cordanaLLM/praetor/**", "cordanaLLM/praetor", "cordanaLLM/praetor/**"],"enabled":false}]}`},
		{"narrow namespace", "renovate.json", `{"packageRules":[{"matchPackageNames":["github.com/cordanaLLM/praetor", "github.com/cordanaLLM/praetor/*", "github.com/cordanaLLM/praetor/cmd/standardsctl", "cordanaLLM/praetor", "cordanaLLM/praetor/*"],"enabled":false}]}`},
		{"narrow matcher", "renovate.json", `{"packageRules":[{"matchPackageNames":["**"],"matchManagers":["gomod"],"enabled":false}]}`},
		{"duplicate keys", "renovate.json", `{"packageRules":[],"packageRules":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, tc.file), tc.data)
			line, err := AuditRenovateEngineBumps(t.Context(), root)
			checkRepositoryVerdict(t, line, err, true, "renovate-engine-bumps")
		})
	}
}

func TestExcludedContextAdoptionAction(t *testing.T) {
	root := t.TempDir()
	initTestGit(t, root)
	mustWrite(t, filepath.Join(root, ".windsurfrules"), "stale\n")
	checkGit(t, root, "add", ".windsurfrules")
	s := &adoptSession{repoPath: root, report: &AdoptReport{}}
	if err := reportExcludedContext(t.Context(), s, []string{"claude"}, []string{".windsurfrules"}); err != nil {
		t.Fatal(err)
	}
	if len(s.report.ActionDetails) != 1 || s.report.ActionDetails[0].Action != actionRemove || !strings.Contains(s.report.ActionDetails[0].Details, "delete tracked .windsurfrules") {
		t.Fatalf("missing deletion action: %+v", s.report)
	}
	if len(s.report.Warnings) != 1 || !strings.Contains(s.report.Warnings[0], "delete tracked .windsurfrules") {
		t.Fatalf("CLI/MCP warning missing deletion action: %+v", s.report.Warnings)
	}
	if got := mustRead(t, filepath.Join(root, ".windsurfrules")); got != "stale\n" {
		t.Fatal("adoption deleted an operator file")
	}
}

// engineFixtureConfig applies engine rule and pin manager merges to fixture configuration for tests.
func engineFixtureConfig(t *testing.T, data string) string {
	t.Helper()
	next, changed, err := mergeRenovateEngineRule(t.Context(), []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		data = string(next)
	}
	next, _, err = mergeRenovatePinManager([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return string(next)
}

// TestRenovateEngineEquivalentRulesTogether accepts the exclusion split over two rules that together name every form.
func TestRenovateEngineEquivalentRulesTogether(t *testing.T) {
	root := t.TempDir()
	data := `{"packageRules":[{"matchPackageNames":["github.com/cordanaLLM/praetor","github.com/cordanaLLM/praetor/**"],"enabled":false},{"matchPackageNames":["cordanaLLM/praetor","cordanaLLM/praetor/**","tekmora/praetor","tekmora/praetor/**"],"enabled":false}]}`
	mustWrite(t, filepath.Join(root, "renovate.json"), data)
	line, err := AuditRenovateEngineBumps(t.Context(), root)
	checkRepositoryVerdict(t, line, err, false, "renovate-engine-bumps")
}
