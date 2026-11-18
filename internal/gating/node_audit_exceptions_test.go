// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nodeAuditFixture reads the frozen package-manager reports without running a manager.
func nodeAuditFixture(t *testing.T, name string) string {
	t.Helper()
	root, err := os.OpenRoot(filepath.Join("testdata", "node-audit"))
	if err != nil {
		t.Fatalf("open the node-audit fixtures: %v", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close the node-audit fixtures: %v", err)
		}
	})
	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

// nodeAuditStandards anchors a shared manifest fixture to the current UTC date.
func nodeAuditStandards(t *testing.T, days int) string {
	t.Helper()
	expires := time.Now().UTC().AddDate(0, 0, days)
	text := nodeAuditFixture(t, ".standards.yaml")
	text = strings.ReplaceAll(text, "ENTERED", expires.AddDate(0, 0, -10).Format("2006-01-02"))
	return strings.ReplaceAll(text, "EXPIRES", expires.Format("2006-01-02"))
}

// runNodeAuditFixture exercises the real security-stage verdict with a frozen command response.
func runNodeAuditFixture(t *testing.T, manager, report, standards, workspace string, failed bool) error {
	t.Helper()
	root := t.TempDir()
	lock := "package-lock.json"
	if manager == nodeManagerPnpm {
		lock = "pnpm-lock.yaml"
	}
	files := map[string]string{"package.json": `{}`, lock: ""}
	if standards != "" {
		files[".standards.yaml"] = standards
	}
	if strings.HasPrefix(workspace, "{") {
		files["package.json"] = workspace
		workspace = ""
	}
	if workspace != "" {
		files["pnpm-workspace.yaml"] = workspace
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &stageConfig{repoDir: root, lookPath: func(name string) (string, error) { return name, nil }}
	cfg.run = func(_ context.Context, _ string, _ string, _ ...string) (string, error) {
		if failed {
			return report, errors.New("exit status 1")
		}
		return report, nil
	}
	_, err := runNodeSecurity(t.Context(), cfg)
	return err
}

// TestNodeAuditReportShapes requires live ignores before falling back to summary counts.
func TestNodeAuditReportShapes(t *testing.T) {
	workspace := "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n"
	legacy := `{"pnpm":{"auditConfig":{"ignoreGhsas":["GHSA-fc5q-39xc-vjpf"]}}}`
	expired := "GHSA-fc5q-39xc-vjpf exception in .standards.yaml expired on " + time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cases := []struct{ name, manager, report, standards, workspace, want string }{
		{"a workspace missing", "pnpm", "legacy-clean.json", "", workspace, "no exception in .standards.yaml covers GHSA-fc5q-39xc-vjpf"},
		{"a package missing", "pnpm", "legacy-clean.json", "", legacy, "no exception in .standards.yaml covers GHSA-fc5q-39xc-vjpf"},
		{"b workspace expired", "pnpm", "legacy-clean.json", nodeAuditStandards(t, -1), workspace, expired},
		{"b package expired", "pnpm", "legacy-clean.json", nodeAuditStandards(t, -1), legacy, expired},
		{"c workspace live zero", "pnpm", "legacy-clean.json", nodeAuditStandards(t, 1), workspace, ""},
		{"c package live zero", "pnpm", "legacy-clean.json", nodeAuditStandards(t, 1), legacy, ""},
		{"c workspace live counts", "pnpm", "legacy.json", nodeAuditStandards(t, 1), workspace, "found 1 high and 0 critical vulnerabilities"},
		{"c package live counts", "pnpm", "legacy.json", nodeAuditStandards(t, 1), legacy, "found 1 high and 0 critical vulnerabilities"},
		{"c npm live zero", "npm", "legacy-clean.json", nodeAuditStandards(t, 1), "", ""},
		{"c npm live counts", "npm", "legacy.json", nodeAuditStandards(t, 1), "", "found 1 high and 0 critical vulnerabilities"},
		{"d pnpm absent manifest", "pnpm", "legacy-clean.json", "", "", ""},
		{"d npm absent manifest", "npm", "legacy-clean.json", "", "", ""},
		{"e workspace text missing", "pnpm", "plain.txt", "", workspace, "no exception in .standards.yaml covers GHSA-fc5q-39xc-vjpf"},
		{"e package text missing", "pnpm", "plain.txt", "", legacy, "no exception in .standards.yaml covers GHSA-fc5q-39xc-vjpf"},
	}
	for i := 0; i < len(cases) && i < 32; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditFixture(t, tc.manager, nodeAuditFixture(t, tc.report), tc.standards, tc.workspace, false)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
		})
	}
}

// TestNodeAuditReportFallback preserves the uncovered verdict without an exception manifest.
func TestNodeAuditReportFallback(t *testing.T) {
	cases := []struct{ manager, report string }{
		{"pnpm", "legacy-clean.json"}, {"npm", "legacy-clean.json"},
		{"pnpm", "legacy.json"}, {"npm", "legacy.json"},
	}
	for i := 0; i < len(cases) && i < 4; i++ {
		tc := cases[i]
		t.Run(tc.manager+" "+tc.report, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			covered, err := judgeNodeAdvisories(t.Context(), root, tc.manager, nodeAuditFixture(t, tc.report))
			if covered || err != nil {
				t.Fatalf("want (false, nil) without .standards.yaml; got (%v, %v)", covered, err)
			}
		})
	}
}

// TestNodeAuditExceptions covers ignored, listed, expired and legacy reports through the stage.
func TestNodeAuditExceptions(t *testing.T) {
	both := nodeAuditStandards(t, 1) + strings.TrimPrefix(strings.ReplaceAll(nodeAuditStandards(t, 1), "GHSA-fc5q-39xc-vjpf", "GHSA-42xw-2xvc-qx8m"), "version: 1\nexceptions:\n")
	cases := []struct {
		name, manager, report, standards, workspace, want string
		failed                                            bool
	}{
		{"a ignored live", "pnpm", "pnpm-ignored.json", nodeAuditStandards(t, 1), "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "", false},
		{"b ignored missing", "pnpm", "pnpm-ignored.json", "version: 1\n", "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "configured ignored advisories; no exception in .standards.yaml covers GHSA-fc5q-39xc-vjpf", false},
		{"c ignored expired", "pnpm", "pnpm-ignored.json", nodeAuditStandards(t, -1), "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "GHSA-fc5q-39xc-vjpf exception in .standards.yaml expired on " + time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), false},
		{"d listed missing", "pnpm", "pnpm-listed.json", "version: 1\n", "", "GHSA-fc5q-39xc-vjpf", true},
		{"e listed live", "pnpm", "pnpm-listed.json", nodeAuditStandards(t, 1), "", "", true},
		{"e npm listed live", "npm", "npm-listed.json", nodeAuditStandards(t, 1), "", "", true},
		{"d npm listed missing", "npm", "npm-listed.json", "version: 1\n", "", "GHSA-fc5q-39xc-vjpf", true},
		{"npm listed expired", "npm", "npm-listed.json", nodeAuditStandards(t, -1), "", "GHSA-fc5q-39xc-vjpf exception in .standards.yaml expired on ", true},
		{"npm second advisory missing", "npm", "npm-two-advisories.json", nodeAuditStandards(t, 1), "", "GHSA-42xw-2xvc-qx8m has no unexpired exception", true},
		{"npm both advisories live", "npm", "npm-two-advisories.json", both, "", "", true},
		{"shared live", "npm", "npm-shared.json", nodeAuditStandards(t, 1), "", "", true},
		{"shared missing", "npm", "npm-shared.json", "version: 1\n", "", "GHSA-fc5q-39xc-vjpf", true},
		{"shared second advisory missing", "npm", "npm-shared-two.json", nodeAuditStandards(t, 1), "", "GHSA-42xw-2xvc-qx8m", true},
		{"cycle one live", "npm", "npm-cycle-one.json", nodeAuditStandards(t, 1), "", "", true},
		{"cycle shared live", "npm", "npm-cycle-shared.json", nodeAuditStandards(t, 1), "", "", true},
		{"second start missing reference", "npm", "npm-shared-missing.json", nodeAuditStandards(t, 1), "", "missing package missing-second", true},
		{"listed expired", "pnpm", "pnpm-listed.json", nodeAuditStandards(t, -1), "", "expired on", true},
		{"f legacy", "pnpm", "legacy.json", "version: 1\n", "", "found 1 high and 0 critical vulnerabilities", false},
		{"legacy exception cannot hide counts", "pnpm", "legacy.json", nodeAuditStandards(t, 1), "", "found 1 high and 0 critical vulnerabilities", false},
		{"expiry day", "pnpm", "pnpm-ignored.json", nodeAuditStandards(t, 0), "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "", false},
	}
	for i := 0; i < len(cases) && i < 32; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditFixture(t, tc.manager, nodeAuditFixture(t, tc.report), tc.standards, tc.workspace, tc.failed)
			checkNodeAuditExceptionError(t, tc.report, tc.want, err)
		})
	}
}

// checkNodeAuditExceptionError checks verdicts, advisory deduplication and exception filtering.
func checkNodeAuditExceptionError(t *testing.T, report, want string, err error) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want %q; got %v", want, err)
	}
	if (report == "npm-two-advisories.json" || report == "npm-shared-two.json") && strings.Contains(err.Error(), "GHSA-fc5q-39xc-vjpf") {
		t.Fatalf("excepted advisory reported: %v", err)
	}
	if report == "npm-shared.json" && strings.Count(err.Error(), want) != 1 {
		t.Fatalf("shared advisory must be named once: %v", err)
	}
	t.Log(err)
}

// TestNodeAuditIgnoreConfiguration covers pnpm policy locations, precedence and fail-closed gaps.
func TestNodeAuditIgnoreConfiguration(t *testing.T) {
	cases := []struct{ name, policy, want string }{
		{"modern workspace", "audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n", ""},
		{"legacy package", `{"pnpm":{"auditConfig":{"ignoreGhsas":["GHSA-fc5q-39xc-vjpf"]}}}`, ""},
		{"unknown gap", "packages: []\n", "unidentified advisories"},
		{"unknown id", "auditConfig:\n  ignoreGhsas: [CVE-2026-1234]\n", "not a GHSA id"},
		{"wrong id", "auditConfig:\n  ignoreGhsas: [GHSA-42xw-2xvc-qx8m]\n", "no exception in .standards.yaml covers GHSA-42xw-2xvc-qx8m"},
		{"all ignores require entries", "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf, GHSA-42xw-2xvc-qx8m]\n", "no exception in .standards.yaml covers GHSA-42xw-2xvc-qx8m"},
		{"modern empty overrides legacy", "audit:\n  ignore: []\nauditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "unidentified advisories"},
		{"duplicate yaml", "auditConfig: {}\nauditConfig: {}\n", "parse pnpm-workspace.yaml"},
		{"invalid yaml", "auditConfig: [\n", "parse pnpm-workspace.yaml"},
		{"cve ignore", "auditConfig:\n  ignoreCves: [CVE-2026-1234]\n", "cannot be covered by GHSA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditFixture(t, "pnpm", nodeAuditFixture(t, "pnpm-ignored.json"), nodeAuditStandards(t, 1), tc.policy, false)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
		})
	}
}

// TestNodeAuditAdvisorySeverities checks advisory-level severity, transitive npm via and unknown IDs.
func TestNodeAuditAdvisorySeverities(t *testing.T) {
	cases := []struct{ name, manager, report, want string }{
		{"zero summary listed high", "pnpm", `{"advisories":{"GHSA-fc5q-39xc-vjpf":{"severity":"high"}}}`, "GHSA-fc5q-39xc-vjpf"},
		{"critical", "pnpm", `{"advisories":{"x":{"severity":"critical","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}}}`, "GHSA-fc5q-39xc-vjpf"},
		{"low", "pnpm", `{"advisories":{"x":{"severity":"low"}}}`, ""},
		{"moderate", "npm", `{"vulnerabilities":{"x":{"severity":"moderate"}}}`, ""},
		{"unknown GHSA", "pnpm", `{"advisories":{"123":{"severity":"high"}}}`, "123 has no unexpired exception"},
		{"npm transitive", "npm", `{"vulnerabilities":{"parent":{"severity":"high","via":["child"]},"child":{"severity":"high","via":[{"severity":"high","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}]}}}`, "GHSA-fc5q-39xc-vjpf"},
		{"npm missing reference", "npm", `{"vulnerabilities":{"parent":{"severity":"high","via":["child"]}}}`, "missing package child"},
		{"npm cycle", "npm", `{"vulnerabilities":{"parent":{"severity":"high","via":["parent"]}}}`, "no bounded GHSA"},
		{"npm shared high", "npm", nodeAuditFixture(t, "npm-shared.json"), "GHSA-fc5q-39xc-vjpf"},
		{"npm shared cycle", "npm", nodeAuditFixture(t, "npm-cycle-shared.json"), "GHSA-fc5q-39xc-vjpf"},
		{"npm shared unresolved", "npm", nodeAuditFixture(t, "npm-shared-missing.json"), "missing package missing-second"},
		{"npm unknown GHSA", "npm", `{"vulnerabilities":{"x":{"severity":"high","via":[{"severity":"high"}]}}}`, "unknown GHSA"},
		{"duplicate report", "pnpm", `{"advisories":{},"advisories":{}}`, "parse Node audit"},
		{"malformed map", "pnpm", `{"advisories":[]}`, "parse Node audit"},
		{"mixed maps", "pnpm", `{"advisories":{},"vulnerabilities":{}}`, "mixes advisory and vulnerability"},
		{"negative counts", "pnpm", `{"advisories":{},"metadata":{"vulnerabilities":{"high":-1}}}`, "negative vulnerability counts"},
		{"command error", "pnpm", `{"advisories":{},"error":{"code":"EAUDIT"}}`, "contains a command error"},
	}
	for i := 0; i < len(cases) && i < 32; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditFixture(t, tc.manager, tc.report, "version: 1\n", "", false)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
		})
	}
}

// TestNodeAuditLimits proves bounded report, ignore-list and npm traversal failures.
func TestNodeAuditLimits(t *testing.T) {
	if _, err := structuredNodeAudit(strings.Repeat(" ", (16<<20)+1)); err == nil {
		t.Fatal("oversized audit accepted")
	}
	ids := strings.Split(strings.Repeat("GHSA-fc5q-39xc-vjpf,", maxNodeAdvisories-1)+"GHSA-fc5q-39xc-vjpf", ",")
	if _, err := uniqueNodeIgnoreIDs(append(ids, "GHSA-fc5q-39xc-vjpf")); err == nil || !strings.Contains(err.Error(), "ignore list exceeds") {
		t.Fatalf("oversized valid ignore list: want entry-bound error; got %v", err)
	}
	if got, err := uniqueNodeIgnoreIDs(ids); err != nil || len(got) != 1 {
		t.Fatalf("ignore list at bound: want one deduplicated id; got %v, %v", got, err)
	}
	packages := map[string]nodeAuditVulnerability{"parent": {Severity: "high", Via: []json.RawMessage{json.RawMessage(`"parent"`)}}}
	budget := 0
	if _, err := npmPackageAdvisories(packages, "parent", &budget, make(map[string][]string)); err == nil {
		t.Fatal("exhausted traversal budget accepted")
	}
	budget = 0
	if _, _, err := npmViaAdvisories(packages["parent"].Via, nil, nil, &budget); err == nil {
		t.Fatal("exhausted via budget accepted")
	}
	if _, _, err := npmViaEntry(json.RawMessage(`[]`)); err == nil {
		t.Fatal("invalid via entry accepted")
	}
}

// TestNodeAuditSharedBudget counts each package and via entry once across starting packages.
func TestNodeAuditSharedBudget(t *testing.T) {
	cases := []struct {
		name  string
		extra int
		want  string
	}{
		{"at traversal budget", 0, ""},
		{"one over traversal budget", 1, "npm audit via exceeds"},
	}
	for i := 0; i < len(cases) && i < 2; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			// Five packages, four references and one high advisory cost ten steps.
			report := strings.ReplaceAll(nodeAuditFixture(t, "npm-shared-budget.json"), `"EXTRA_VIA"`, strings.TrimPrefix(strings.Repeat(",{}", maxNodeAdvisories-10+tc.extra), ","))
			err := runNodeAuditFixture(t, "npm", report, nodeAuditStandards(t, 1), "", true)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
		})
	}
}
