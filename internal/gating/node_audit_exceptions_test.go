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
	files := map[string]string{"package.json": `{}`, lock: "", ".standards.yaml": standards}
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

// TestNodeAuditExceptions covers ignored, listed, expired and legacy reports through the stage.
func TestNodeAuditExceptions(t *testing.T) {
	both := nodeAuditStandards(t, 1) + strings.TrimPrefix(strings.ReplaceAll(nodeAuditStandards(t, 1), "GHSA-fc5q-39xc-vjpf", "GHSA-42xw-2xvc-qx8m"), "version: 1\nexceptions:\n")
	cases := []struct {
		name, manager, report, standards, workspace, want string
		failed                                            bool
	}{
		{"a ignored live", "pnpm", "pnpm-ignored.json", nodeAuditStandards(t, 1), "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "", false},
		{"b ignored missing", "pnpm", "pnpm-ignored.json", "version: 1\n", "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "1 ignored high or critical findings; no exception in .standards.yaml covers GHSA-fc5q-39xc-vjpf", false},
		{"c ignored expired", "pnpm", "pnpm-ignored.json", nodeAuditStandards(t, -1), "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "GHSA-fc5q-39xc-vjpf exception in .standards.yaml expired on " + time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), false},
		{"d listed missing", "pnpm", "pnpm-listed.json", "version: 1\n", "", "GHSA-fc5q-39xc-vjpf", true},
		{"e listed live", "pnpm", "pnpm-listed.json", nodeAuditStandards(t, 1), "", "", true},
		{"e npm listed live", "npm", "npm-listed.json", nodeAuditStandards(t, 1), "", "", true},
		{"d npm listed missing", "npm", "npm-listed.json", "version: 1\n", "", "GHSA-fc5q-39xc-vjpf", true},
		{"npm listed expired", "npm", "npm-listed.json", nodeAuditStandards(t, -1), "", "GHSA-fc5q-39xc-vjpf exception in .standards.yaml expired on ", true},
		{"npm second advisory missing", "npm", "npm-two-advisories.json", nodeAuditStandards(t, 1), "", "GHSA-42xw-2xvc-qx8m has no unexpired exception", true},
		{"npm both advisories live", "npm", "npm-two-advisories.json", both, "", "", true},
		{"listed expired", "pnpm", "pnpm-listed.json", nodeAuditStandards(t, -1), "", "expired on", true},
		{"f legacy", "pnpm", "legacy.json", "version: 1\n", "", "found 1 high and 0 critical vulnerabilities", false},
		{"legacy exception cannot hide counts", "pnpm", "legacy.json", nodeAuditStandards(t, 1), "", "found 1 high and 0 critical vulnerabilities", false},
		{"expiry day", "pnpm", "pnpm-ignored.json", nodeAuditStandards(t, 0), "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditFixture(t, tc.manager, nodeAuditFixture(t, tc.report), tc.standards, tc.workspace, tc.failed)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
			if tc.report == "npm-two-advisories.json" && strings.Contains(err.Error(), "GHSA-fc5q-39xc-vjpf") {
				t.Fatalf("excepted advisory reported: %v", err)
			}
			t.Log(err)
		})
	}
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
		{"npm unknown GHSA", "npm", `{"vulnerabilities":{"x":{"severity":"high","via":[{"severity":"high"}]}}}`, "unknown GHSA"},
		{"duplicate report", "pnpm", `{"advisories":{},"advisories":{}}`, "parse Node audit"},
		{"malformed map", "pnpm", `{"advisories":[]}`, "parse Node audit"},
		{"mixed maps", "pnpm", `{"advisories":{},"vulnerabilities":{}}`, "mixes advisory and vulnerability"},
		{"negative counts", "pnpm", `{"advisories":{},"metadata":{"vulnerabilities":{"high":-1}}}`, "negative vulnerability counts"},
		{"command error", "pnpm", `{"advisories":{},"error":{"code":"EAUDIT"}}`, "contains a command error"},
	}
	for _, tc := range cases {
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
	if _, err := uniqueNodeIgnoreIDs(make([]string, maxNodeAdvisories+1)); err == nil {
		t.Fatal("oversized ignore list accepted")
	}
	packages := map[string]nodeAuditVulnerability{"parent": {Severity: "high", Via: []json.RawMessage{json.RawMessage(`"parent"`)}}}
	budget := 0
	if _, err := npmPackageAdvisories(packages, "parent", &budget); err == nil {
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
