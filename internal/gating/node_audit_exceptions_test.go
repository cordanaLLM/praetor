// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNodeAuditUnidentifiedGaps bounds omitted advisories by configured ignores absent from the report.
func TestNodeAuditUnidentifiedGaps(t *testing.T) {
	standards := nodeAuditStandards(t, 1)
	both := standards + strings.TrimPrefix(strings.ReplaceAll(standards, "GHSA-fc5q-39xc-vjpf", "GHSA-42xw-2xvc-qx8m"), "version: 1\nexceptions:\n")
	listed := `{"GHSA-fc5q-39xc-vjpf":{"severity":"high"}}`
	cases := []struct {
		name, advisories, ignores, want string
		high, critical                  int
	}{
		{"a real ignored shape", `{}`, "GHSA-fc5q-39xc-vjpf", "", 1, 0},
		{"c listed ignore counted twice", listed, "GHSA-fc5q-39xc-vjpf", "1 high or critical findings counted but not named; 0 configured ignores can explain unidentified advisories", 2, 0},
		{"d gap equals unlisted ignores", listed, "GHSA-fc5q-39xc-vjpf, GHSA-42xw-2xvc-qx8m", "", 2, 0},
		{"d gap above unlisted ignores", listed, "GHSA-fc5q-39xc-vjpf, GHSA-42xw-2xvc-qx8m", "2 high or critical findings counted but not named; 1 configured ignores can explain unidentified advisories", 3, 0},
		{"e gap no ignores", `{}`, "", "1 high or critical findings counted but not named; 0 configured ignores can explain unidentified advisories", 1, 0},
		// Two omitted advisories: the report names neither, so their IDs cannot be distinguished from other configured IDs.
		{"f two omitted advisories", `{}`, "GHSA-fc5q-39xc-vjpf, GHSA-42xw-2xvc-qx8m", "", 2, 0},
		{"listed excepted no gap", listed, "GHSA-fc5q-39xc-vjpf", "", 1, 0},
		{"zero gap", `{}`, "GHSA-fc5q-39xc-vjpf", "", 0, 0},
		{"critical gap equals unlisted ignores", `{}`, "GHSA-fc5q-39xc-vjpf", "", 0, 1},
		{"combined gap above unlisted ignores", `{}`, "GHSA-fc5q-39xc-vjpf", "2 high or critical findings counted but not named; 1 configured ignores can explain unidentified advisories", 1, 1},
		{"moderate listed ignore never counts", strings.ReplaceAll(listed, "high", "moderate"), "GHSA-fc5q-39xc-vjpf", "1 high or critical findings counted but not named; 0 configured ignores can explain unidentified advisories", 1, 0},
	}
	for i := 0; i < len(cases) && i < 16; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			report := fmt.Sprintf(`{"advisories":%s,"metadata":{"vulnerabilities":{"high":%d,"critical":%d}}}`, tc.advisories, tc.high, tc.critical)
			if tc.name == "a real ignored shape" {
				report = nodeAuditFixture(t, "pnpm-ignored.json")
			}
			policy := "auditConfig:\n  ignoreGhsas: [" + tc.ignores + "]\n"
			err := runNodeAuditFixture(t, "pnpm", report, both, policy, false)
			checkNodeAuditExceptionError(t, "", tc.want, err)
		})
	}
}

// nodeAuditChainID generates distinct valid IDs without a large committed fixture.
func nodeAuditChainID(index int) string {
	const alphabet = "23456789cfghjmpqrvwx"
	var suffix [4]byte
	for i := 0; i < len(suffix) && i < 4; i++ {
		suffix[i] = alphabet[index%len(alphabet)]
		index /= len(alphabet)
	}
	return "GHSA-" + string(suffix[:]) + "-2222-2222"
}

// nodeAuditDistinctChain generates a report within the package and traversal limits.
func nodeAuditDistinctChain(count int) map[string]nodeAuditVulnerability {
	packages := make(map[string]nodeAuditVulnerability, count)
	for i := 0; i < count && i < maxNodeAdvisories; i++ {
		via := []json.RawMessage{json.RawMessage(fmt.Sprintf(`{"severity":"high","github_advisory_id":%q}`, nodeAuditChainID(i)))}
		if i+1 < count {
			via = append(via, json.RawMessage(fmt.Sprintf(`"pkg%05d"`, i+1)))
		}
		packages[fmt.Sprintf("pkg%05d", i)] = nodeAuditVulnerability{Severity: "high", Via: via}
	}
	return packages
}

// checkNodeAuditWalkStorage stops a planted quadratic walk before it exhausts host memory.
func checkNodeAuditWalkStorage(t *testing.T, walk *npmAdvisoryWalk, ceiling int) {
	t.Helper()
	if err := walk.push("pkg00000"); err != nil {
		t.Fatal(err)
	}
	for step := 0; len(walk.path) > 0 && step < maxNodeAdvisories; step++ {
		frame := walk.path[len(walk.path)-1]
		if frame.next < len(frame.dependencies) {
			dependency := frame.dependencies[frame.next]
			frame.next++
			if err := walk.follow(frame, dependency); err != nil {
				t.Fatal(err)
			}
		} else {
			walk.finish(frame)
		}
		if entries := walk.storedAdvisoryEntries(); entries > ceiling {
			t.Fatalf("retained advisory entries %d exceed linear ceiling %d", entries, ceiling)
		}
	}
	if len(walk.path) != 0 {
		t.Fatal("bounded walk did not finish")
	}
}

// TestNodeAuditDistinctChainStorage proves linear retained IDs and the real report verdict.
func TestNodeAuditDistinctChainStorage(t *testing.T) {
	const count = 20000
	packages := nodeAuditDistinctChain(count)
	budget := maxNodeAdvisories
	walk := newNpmAdvisoryWalk(packages, &budget)
	checkNodeAuditWalkStorage(t, &walk, 4*count)
	if got := walk.storedAdvisoryEntries(); got != count {
		t.Fatalf("stored advisory entries: got %d, want %d", got, count)
	}
	if len(walk.memo) != count || budget != maxNodeAdvisories-(3*count-1) {
		t.Fatalf("completed packages=%d remaining budget=%d", len(walk.memo), budget)
	}
	checkNodeAuditChainVerdict(t, packages, count)
	t.Logf("packages=%d retained IDs=%d charged steps=%d", count, walk.storedAdvisoryEntries(), maxNodeAdvisories-budget)
}

// checkNodeAuditChainVerdict verifies parsing, complete findings and exception refusal.
func checkNodeAuditChainVerdict(t *testing.T, packages map[string]nodeAuditVulnerability, count int) {
	t.Helper()
	report := npmAuditReport{Vulnerabilities: packages}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := structuredNodeAudit(string(encoded))
	if err != nil || parsed == nil {
		t.Fatalf("generated report: %v", err)
	}
	findings, err := listedNodeFindings(*parsed)
	if err != nil || len(findings.ids) != count || findings.high != count {
		t.Fatalf("chain findings: ids=%d high=%d err=%v", len(findings.ids), findings.high, err)
	}
	covered, err := judgeStructuredNodeAdvisories(*parsed, nil, nil)
	if covered || err == nil || !strings.Contains(err.Error(), "has no unexpired exception") {
		t.Fatalf("unexcepted chain verdict: covered=%v err=%v", covered, err)
	}
}

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

// nodeAuditReportText returns inline JSON directly or reads a named fixture file.
func nodeAuditReportText(t *testing.T, report string) string {
	t.Helper()
	if strings.HasPrefix(report, "{") {
		return report
	}
	return nodeAuditFixture(t, report)
}

// stageNodeAuditFiles writes the manifest, lockfile and policy fixtures into root.
func stageNodeAuditFiles(t *testing.T, root, manager, standards, workspace string) {
	t.Helper()
	lock := "package-lock.json"
	if manager == nodeManagerPnpm {
		lock = "pnpm-lock.yaml"
	}
	files := map[string]string{"package.json": `{}`, lock: "", ".standards.yaml": standards}
	if standards == "" {
		delete(files, ".standards.yaml")
	}
	applyNodeAuditWorkspace(t, root, files, workspace)
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// applyNodeAuditWorkspace places the workspace or legacy package policy fixture into root.
func applyNodeAuditWorkspace(t *testing.T, root string, files map[string]string, workspace string) {
	t.Helper()
	switch {
	case workspace == "EMPTY_WORKSPACE":
		files["pnpm-workspace.yaml"] = ""
	case workspace == "ESCAPING_PACKAGE_SYMLINK":
		delete(files, "package.json")
		outside := filepath.Join(t.TempDir(), "outside-package.json")
		body := `{"pnpm":{"auditConfig":{"ignoreGhsas":["GHSA-fc5q-39xc-vjpf"]}}}`
		if err := os.WriteFile(outside, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "package.json")); err != nil {
			t.Fatal(err)
		}
	case strings.HasPrefix(workspace, "{"):
		files["package.json"] = workspace
	case workspace != "":
		files["pnpm-workspace.yaml"] = workspace
	}
}

// runNodeAuditWithErr exercises the security-stage verdict with an explicit runner error.
func runNodeAuditWithErr(t *testing.T, manager, report, standards, workspace string, runErr error) error {
	t.Helper()
	root := t.TempDir()
	stageNodeAuditFiles(t, root, manager, standards, workspace)
	cfg := &stageConfig{repoDir: root, lookPath: func(name string) (string, error) { return name, nil }}
	cfg.run = func(_ context.Context, _ string, _ string, _ ...string) (string, error) {
		return report, runErr
	}
	_, err := runNodeSecurity(t.Context(), cfg)
	return err
}

type nodeAuditExitError int

// Error formats the exit status like os/exec.ExitError.
func (e nodeAuditExitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// ExitCode returns the numeric process exit status.
func (e nodeAuditExitError) ExitCode() int { return int(e) }

// runNodeAuditFixture exercises the real security-stage verdict with a frozen command response.
func runNodeAuditFixture(t *testing.T, manager, report, standards, workspace string, failed bool) error {
	t.Helper()
	var runErr error
	if failed {
		runErr = nodeAuditExitError(1)
	}
	return runNodeAuditWithErr(t, manager, report, standards, workspace, runErr)
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
	moderateCritVia := `{"auditReportVersion":2,"vulnerabilities":{"p":{"severity":"moderate","via":[{"severity":"critical","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}]}}}`
	missingSecondVia := `{"auditReportVersion":2,"vulnerabilities":{"p":{"severity":"high","via":[{"severity":"high","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"},{"url":"https://github.com/advisories/GHSA-42xw-2xvc-qx8m"}]}}}`
	duplicateDiffSev := `{"advisories":{"111":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"low"},"222":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"high"}}}`
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
		{"diamond live", "npm", "npm-diamond.json", nodeAuditStandards(t, 1), "", "", true},
		{"diamond missing", "npm", "npm-diamond.json", "version: 1\n", "", "GHSA-fc5q-39xc-vjpf", true},
		{"cycle one live", "npm", "npm-cycle-one.json", nodeAuditStandards(t, 1), "", "", true},
		{"cycle shared live", "npm", "npm-cycle-shared.json", nodeAuditStandards(t, 1), "", "", true},
		{"second start missing reference", "npm", "npm-shared-missing.json", nodeAuditStandards(t, 1), "", "missing package missing-second", true},
		{"moderate pkg critical via missing", "npm", moderateCritVia, "version: 1\n", "", "GHSA-fc5q-39xc-vjpf has no unexpired exception", true},
		{"moderate pkg critical via live", "npm", moderateCritVia, nodeAuditStandards(t, 1), "", "", true},
		{"second via missing severity", "npm", missingSecondVia, nodeAuditStandards(t, 1), "", "npm audit via GHSA-42xw-2xvc-qx8m is missing severity", true},
		{"duplicate id conflicting severities", "pnpm", duplicateDiffSev, nodeAuditStandards(t, 1), "", "node audit advisory GHSA-fc5q-39xc-vjpf has conflicting severities", true},
		{"listed expired", "pnpm", "pnpm-listed.json", nodeAuditStandards(t, -1), "", "expired on", true},
		{"f legacy", "pnpm", "legacy.json", "version: 1\n", "", "found 1 high and 0 critical vulnerabilities", false},
		{"legacy exception cannot hide counts", "pnpm", "legacy.json", nodeAuditStandards(t, 1), "", "found 1 high and 0 critical vulnerabilities", false},
		{"expiry day", "pnpm", "pnpm-ignored.json", nodeAuditStandards(t, 0), "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "", false},
	}
	for i := 0; i < len(cases) && i < 32; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditFixture(t, tc.manager, nodeAuditReportText(t, tc.report), tc.standards, tc.workspace, tc.failed)
			checkNodeAuditExceptionError(t, tc.report, tc.want, err)
		})
	}
}

// TestNodeAuditCommandResults verifies that only the findings exit status is waived for a covered report.
func TestNodeAuditCommandResults(t *testing.T) {
	report := nodeAuditFixture(t, "pnpm-listed.json")
	standards := nodeAuditStandards(t, 1)
	captureErr := errors.Join(nodeAuditExitError(1), errors.New("command output exceeds 16777216 bytes per stream"), context.Canceled)
	cases := []struct {
		name   string
		runErr error
		want   string
	}{
		{"findings exit 1", nodeAuditExitError(1), ""},
		{"exit 2", nodeAuditExitError(2), "pnpm audit --audit-level=high --json failed: exit status 2"},
		{"signal kill", killedBySignalExitError(t), "pnpm audit --audit-level=high --json failed: signal: killed"},
		{"launch error", &os.PathError{Op: "fork/exec", Path: "pnpm", Err: os.ErrPermission}, "pnpm audit --audit-level=high --json failed: fork/exec pnpm: permission denied"},
		{"capture error", captureErr, "pnpm audit --audit-level=high --json failed: exit status 1\ncommand output exceeds 16777216 bytes per stream"},
	}
	for i := 0; i < len(cases) && i < 8; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditWithErr(t, "pnpm", report, standards, "", tc.runErr)
			checkNodeAuditExceptionError(t, "", tc.want, err)
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
	if (report == "npm-shared.json" || report == "npm-diamond.json") && strings.Count(err.Error(), want) != 1 {
		t.Fatalf("shared advisory must be named once: %v", err)
	}
	t.Log(err)
}

// TestNodeAuditIgnoreConfiguration covers pnpm policy locations, precedence and fail-closed gaps.
func TestNodeAuditIgnoreConfiguration(t *testing.T) {
	cases := []struct{ name, report, policy, want string }{
		{"modern workspace", "pnpm-ignored.json", "audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n", ""},
		{"legacy package", "pnpm-ignored.json", `{"pnpm":{"auditConfig":{"ignoreGhsas":["GHSA-fc5q-39xc-vjpf"]}}}`, ""},
		{"escaping package symlink", "pnpm-ignored.json", "ESCAPING_PACKAGE_SYMLINK", "package.json"},
		{"second document malformed", "pnpm-ignored.json", "audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n---\n[\n", "parse pnpm-workspace.yaml"},
		{"second document well-formed", "pnpm-ignored.json", "audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n---\npackages: []\n", "parse pnpm-workspace.yaml"},
		{"single document unrelated keys", "pnpm-ignored.json", "packages:\n  - packages/*\naudit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n", ""},
		{"empty workspace means no policy", "pnpm-ignored.json", "EMPTY_WORKSPACE", "unidentified advisories"},
		{"empty workspace clean report", "legacy-clean.json", "EMPTY_WORKSPACE", ""},
		{"unknown gap", "pnpm-ignored.json", "packages: []\n", "unidentified advisories"},
		{"unknown id", "pnpm-ignored.json", "auditConfig:\n  ignoreGhsas: [CVE-2026-1234]\n", "not a GHSA id"},
		{"wrong id", "pnpm-ignored.json", "auditConfig:\n  ignoreGhsas: [GHSA-42xw-2xvc-qx8m]\n", "no exception in .standards.yaml covers GHSA-42xw-2xvc-qx8m"},
		{"all ignores require entries", "pnpm-ignored.json", "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf, GHSA-42xw-2xvc-qx8m]\n", "no exception in .standards.yaml covers GHSA-42xw-2xvc-qx8m"},
		{"modern empty overrides legacy", "pnpm-ignored.json", "audit:\n  ignore: []\nauditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "unidentified advisories"},
		{"duplicate yaml", "pnpm-ignored.json", "auditConfig: {}\nauditConfig: {}\n", "parse pnpm-workspace.yaml"},
		{"invalid yaml", "pnpm-ignored.json", "auditConfig: [\n", "parse pnpm-workspace.yaml"},
		{"cve ignore", "pnpm-ignored.json", "auditConfig:\n  ignoreCves: [CVE-2026-1234]\n", "cannot be covered by GHSA"},
	}
	for i := 0; i < len(cases) && i < 32; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditFixture(t, "pnpm", nodeAuditFixture(t, tc.report), nodeAuditStandards(t, 1), tc.policy, false)
			checkNodeAuditExceptionError(t, tc.report, tc.want, err)
		})
	}
}

// TestNodeAuditAdvisorySeverities checks advisory-level severity, transitive npm via and unknown IDs.
func TestNodeAuditAdvisorySeverities(t *testing.T) {
	cases := []struct{ name, manager, report, want string }{
		{"zero summary listed high", "pnpm", `{"advisories":{"GHSA-fc5q-39xc-vjpf":{"severity":"high"}}}`, "GHSA-fc5q-39xc-vjpf"},
		{"critical", "pnpm", `{"advisories":{"x":{"severity":"critical","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}}}`, "GHSA-fc5q-39xc-vjpf"},
		{"info", "pnpm", `{"advisories":{"x":{"severity":"info"}}}`, ""},
		{"low", "pnpm", `{"advisories":{"x":{"severity":"low"}}}`, ""},
		{"moderate", "npm", `{"vulnerabilities":{"x":{"severity":"moderate"}}}`, ""},
		{"pnpm unknown severity", "pnpm", `{"advisories":{"111":{"severity":"unknown"}}}`, `node audit advisory 111 has unknown severity "unknown"`},
		{"pnpm empty severity", "pnpm", `{"advisories":{"111":{"severity":""}}}`, "node audit advisory 111 has empty severity"},
		{"pnpm missing severity", "pnpm", `{"advisories":{"111":{}}}`, "node audit advisory 111 is missing severity"},
		{"pnpm null advisory", "pnpm", `{"advisories":{"111":null}}`, "node audit advisory 111 is null"},
		{"npm unknown package severity", "npm", `{"vulnerabilities":{"pkg":{"severity":"unknown"}}}`, `npm audit package pkg has unknown severity "unknown"`},
		{"npm empty package severity", "npm", `{"vulnerabilities":{"pkg":{"severity":""}}}`, "npm audit package pkg has empty severity"},
		{"npm missing package severity", "npm", `{"vulnerabilities":{"pkg":{}}}`, "npm audit package pkg is missing severity"},
		{"npm null package", "npm", `{"vulnerabilities":{"pkg":null}}`, "npm audit package pkg is null"},
		{"npm moderate pkg critical via", "npm", `{"vulnerabilities":{"p":{"severity":"moderate","via":[{"severity":"critical","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}]}}}`, "GHSA-fc5q-39xc-vjpf has no unexpired exception"},
		{"pnpm duplicate id conflicting severities", "pnpm", `{"advisories":{"111":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"low"},"222":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"high"}}}`, `node audit advisory GHSA-fc5q-39xc-vjpf has conflicting severities "low" and "high"`},
		{"unknown GHSA", "pnpm", `{"advisories":{"123":{"severity":"high"}}}`, "123 has no unexpired exception"},
		{"npm transitive", "npm", `{"vulnerabilities":{"parent":{"severity":"high","via":["child"]},"child":{"severity":"high","via":[{"severity":"high","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}]}}}`, "GHSA-fc5q-39xc-vjpf"},
		{"npm missing reference", "npm", `{"vulnerabilities":{"parent":{"severity":"high","via":["child"]}}}`, "missing package child"},
		{"npm cycle", "npm", `{"vulnerabilities":{"parent":{"severity":"high","via":["parent"]}}}`, "no bounded GHSA"},
		{"npm disconnected package", "npm", `{"vulnerabilities":{"a":{"severity":"high","via":[{"severity":"high","github_advisory_id":"GHSA-fc5q-39xc-vjpf"}]},"z":{"severity":"high","via":[]}}}`, "no bounded GHSA"},
		{"npm disconnected cycle", "npm", `{"vulnerabilities":{"a":{"severity":"high","via":[{"severity":"high","github_advisory_id":"GHSA-fc5q-39xc-vjpf"}]},"z":{"severity":"high","via":["z"]}}}`, "no bounded GHSA"},
		{"npm shared high", "npm", nodeAuditFixture(t, "npm-shared.json"), "GHSA-fc5q-39xc-vjpf"},
		{"npm diamond high", "npm", nodeAuditFixture(t, "npm-diamond.json"), "GHSA-fc5q-39xc-vjpf"},
		{"npm shared cycle", "npm", nodeAuditFixture(t, "npm-cycle-shared.json"), "GHSA-fc5q-39xc-vjpf"},
		{"npm shared unresolved", "npm", nodeAuditFixture(t, "npm-shared-missing.json"), "missing package missing-second"},
		{"npm unknown GHSA", "npm", `{"vulnerabilities":{"x":{"severity":"high","via":[{"severity":"high"}]}}}`, "unknown GHSA"},
		{"duplicate report", "pnpm", `{"advisories":{},"advisories":{}}`, "parse Node audit"},
		{"malformed map", "pnpm", `{"advisories":[]}`, "parse Node audit"},
		{"mixed maps", "pnpm", `{"advisories":{},"vulnerabilities":{}}`, "mixes advisory and vulnerability"},
		{"negative counts", "pnpm", `{"advisories":{},"metadata":{"vulnerabilities":{"high":-1}}}`, "negative vulnerability counts"},
		{"command error", "pnpm", `{"advisories":{},"error":{"code":"EAUDIT"}}`, "contains a command error"},
	}
	for i := 0; i < len(cases) && i < 64; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditFixture(t, tc.manager, tc.report, "version: 1\n", "", false)
			checkNodeAuditExceptionError(t, tc.report, tc.want, err)
		})
	}
}

// checkOversizedNodeAuditMaps verifies that maps over maxNodeAdvisories are refused, never judged on a prefix.
func checkOversizedNodeAuditMaps(t *testing.T) {
	t.Helper()
	advisories := make(map[string]nodeAuditAdvisory, maxNodeAdvisories+1)
	packages := make(map[string]nodeAuditVulnerability, maxNodeAdvisories+1)
	for i := 0; i < maxNodeAdvisories; i++ {
		key := fmt.Sprintf("k%05d", i)
		advisories[key] = nodeAuditAdvisory{Severity: "low"}
		packages[key] = nodeAuditVulnerability{Severity: "low"}
	}
	advisories["zzzzz"] = nodeAuditAdvisory{Severity: "high", GHSA: "GHSA-fc5q-39xc-vjpf"}
	packages["zzzzz"] = nodeAuditVulnerability{Severity: "high"}
	if _, err := listedNodeFindings(npmAuditReport{Advisories: advisories}); err == nil || !strings.Contains(err.Error(), "exceeds 65536 listed entries") {
		t.Fatalf("oversized advisories map: got %v", err)
	}
	if _, err := listedNpmFindings(packages); err == nil || !strings.Contains(err.Error(), "exceeds 65536 listed entries") {
		t.Fatalf("oversized packages map in listedNpmFindings: got %v", err)
	}
}

// TestNodeAuditLimits proves bounded report, ignore-list and npm traversal failures.
func TestNodeAuditLimits(t *testing.T) {
	if _, err := structuredNodeAudit(strings.Repeat(" ", (16<<20)+1)); err == nil {
		t.Fatal("oversized audit accepted")
	}
	checkOversizedNodeAuditMaps(t)
	ids := strings.Split(strings.Repeat("GHSA-fc5q-39xc-vjpf,", maxNodeAdvisories-1)+"GHSA-fc5q-39xc-vjpf", ",")
	if _, err := uniqueNodeIgnoreIDs(append(ids, "GHSA-fc5q-39xc-vjpf")); err == nil || !strings.Contains(err.Error(), "ignore list exceeds") {
		t.Fatalf("oversized valid ignore list: want entry-bound error; got %v", err)
	}
	if got, err := uniqueNodeIgnoreIDs(ids); err != nil || len(got) != 1 {
		t.Fatalf("ignore list at bound: want one deduplicated id; got %v, %v", got, err)
	}
	packages := map[string]nodeAuditVulnerability{"parent": {Severity: "high", Via: []json.RawMessage{json.RawMessage(`"parent"`)}}}
	budget := 0
	walk := newNpmAdvisoryWalk(packages, &budget)
	if _, err := walk.advisories("parent"); err == nil {
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
			report := strings.ReplaceAll(nodeAuditFixture(t, "npm-shared-budget.json"), `"EXTRA_VIA"`, strings.TrimPrefix(strings.Repeat(`,{"severity":"low"}`, maxNodeAdvisories-10+tc.extra), ","))
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
