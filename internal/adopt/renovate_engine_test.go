// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

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
	if err := reconcileRenovateIgnore(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if second := mustRead(t, filepath.Join(root, "renovate.json")); second != first {
		t.Fatal("adoption is not idempotent")
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

func TestRenovateEngineUnsafeInputs(t *testing.T) {
	cases := []struct{ name, file, data string }{
		{"JSONC", "renovate.jsonc", "// comment\n{}"},
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

func engineFixtureConfig(t *testing.T, data string) string {
	t.Helper()
	next, changed, err := mergeRenovateEngineRule(t.Context(), []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		return string(next)
	}
	return data
}

func TestRenovateEngineEquivalentRulesTogether(t *testing.T) {
	root := t.TempDir()
	data := `{"packageRules":[{"matchPackageNames":["github.com/cordanaLLM/praetor","github.com/cordanaLLM/praetor/**"],"enabled":false},{"matchPackageNames":["cordanaLLM/praetor","cordanaLLM/praetor/**"],"enabled":false}]}`
	mustWrite(t, filepath.Join(root, "renovate.json"), data)
	line, err := AuditRenovateEngineBumps(t.Context(), root)
	checkRepositoryVerdict(t, line, err, false, "renovate-engine-bumps")
}
