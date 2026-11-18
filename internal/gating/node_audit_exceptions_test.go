// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
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
		{"overflowing severity gaps refused", listed, "GHSA-fc5q-39xc-vjpf", "high or critical findings counted but not named; 0 configured ignores can explain unidentified advisories", math.MaxInt, 2},
		{"pnpm vulnerabilities map listed ignore never counts", `"NPM_VULNS"`, "GHSA-fc5q-39xc-vjpf", "1 high or critical findings counted but not named; 0 configured ignores can explain unidentified advisories", 2, 0},
	}
	for i := 0; i < len(cases) && i < 16; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			report := fmt.Sprintf(`{"advisories":%s,"metadata":{"vulnerabilities":{"high":%d,"critical":%d}}}`, tc.advisories, tc.high, tc.critical)
			if tc.name == "a real ignored shape" {
				report = nodeAuditFixture(t, "pnpm-ignored.json")
			}
			if tc.advisories == `"NPM_VULNS"` {
				report = fmt.Sprintf(`{"vulnerabilities":{"picomatch":{"severity":"high","via":[{"severity":"high","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}]}},"metadata":{"vulnerabilities":{"high":%d,"critical":%d}}}`, tc.high, tc.critical)
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

// nodeAuditStandardsAt anchors a shared manifest fixture to an explicit UTC expiry date.
func nodeAuditStandardsAt(t *testing.T, expires time.Time) string {
	t.Helper()
	text := nodeAuditFixture(t, ".standards.yaml")
	text = strings.ReplaceAll(text, "ENTERED", expires.AddDate(0, 0, -10).Format("2006-01-02"))
	return strings.ReplaceAll(text, "EXPIRES", expires.Format("2006-01-02"))
}

// nodeAuditStandards anchors a shared manifest fixture to a UTC date offset from today.
func nodeAuditStandards(t *testing.T, days int) string {
	t.Helper()
	return nodeAuditStandardsAt(t, time.Now().UTC().AddDate(0, 0, days))
}

// nodeAuditExpiredStandards returns an expired manifest fixture and its expected expiry message from one clock read.
func nodeAuditExpiredStandards(t *testing.T) (string, string) {
	t.Helper()
	expiredDate := time.Now().UTC().AddDate(0, 0, -2)
	return nodeAuditStandardsAt(t, expiredDate), "GHSA-fc5q-39xc-vjpf exception in .standards.yaml expired on " + expiredDate.Format("2006-01-02")
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
		target := filepath.Join(root, name)
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- the fixture writes files into this directory, which needs the owner search bit.
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// applyNodeAuditWorkspace places the workspace or legacy package policy fixture into root.
func applyNodeAuditWorkspace(t *testing.T, root string, files map[string]string, workspace string) {
	t.Helper()
	switch {
	case workspace == "ESCAPING_PACKAGE_SYMLINK":
		delete(files, "package.json")
		stageEscapingNodeAuditSymlink(t, filepath.Join(root, "package.json"), `{"pnpm":{"auditConfig":{"ignoreGhsas":["GHSA-fc5q-39xc-vjpf"]}}}`)
	case workspace == "ESCAPING_INCLUDE_SYMLINK":
		files["pnpm-workspace.yaml"] = "include:\n  ./audit.yaml: [audit]\n"
		stageEscapingNodeAuditSymlink(t, filepath.Join(root, "audit.yaml"), "audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n")
	case strings.HasPrefix(workspace, "FILE:"):
		parts := strings.SplitN(strings.TrimPrefix(workspace, "FILE:"), "|", 3)
		files["pnpm-workspace.yaml"] = parts[0]
		files[parts[1]] = parts[2]
	case strings.HasPrefix(workspace, "FILES:"):
		applyNodeAuditIncludeFiles(t, root, files, workspace)
	default:
		applyPlainNodeAuditWorkspace(files, workspace)
	}
}

// applyPlainNodeAuditWorkspace stages inline workspace or legacy manifest policy.
func applyPlainNodeAuditWorkspace(files map[string]string, workspace string) {
	switch {
	case workspace == "EMPTY_WORKSPACE":
		files["pnpm-workspace.yaml"] = ""
	case strings.HasPrefix(workspace, "{"):
		files["package.json"] = workspace
	case workspace != "":
		files["pnpm-workspace.yaml"] = workspace
	}
}

// applyNodeAuditIncludeFiles stages bounded multiple-file include and escaping symlink fixtures.
func applyNodeAuditIncludeFiles(t *testing.T, root string, files map[string]string, workspace string) {
	t.Helper()
	parts := strings.SplitN(strings.TrimPrefix(workspace, "FILES:"), "|", 131)
	files["pnpm-workspace.yaml"] = parts[0]
	for i := 1; i+1 < len(parts) && i < 130; i += 2 {
		if parts[i+1] == "ESCAPING_SYMLINK" {
			stageEscapingNodeAuditSymlink(t, filepath.Join(root, parts[i]), "audit: {}\n")
			continue
		}
		files[parts[i]] = parts[i+1]
	}
}

// TestNodeAuditIncludeConfiguration exercises documented include forms and named refusals.
func TestNodeAuditIncludeConfiguration(t *testing.T) {
	const ignored = "audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n"
	cases := []struct{ name, report, policy, want string }{
		{"a wildcard unrelated inline ignores", "pnpm-ignored.json", "FILE:include:\n  ./security.yaml: '*'\nauditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n|security.yaml|minimumReleaseAge: 1440\n", ""},
		{"b wildcard audit ignores", "pnpm-ignored.json", "FILE:include:\n  ./audit.yaml: '*'\n|audit.yaml|" + ignored, ""},
		{"b wildcard auditConfig ignores", "pnpm-ignored.json", "FILE:include:\n  ./audit.yaml: '*'\n|audit.yaml|auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", ""},
		{"c wildcard inline conflict", "legacy-clean.json", "FILE:include:\n  ./audit.yaml: '*'\naudit: {}\n|audit.yaml|audit: {}\n", "single owner"},
		{"c wildcard reserved conflict", "legacy-clean.json", "FILES:include:\n  ./a.yaml: '*'\n  ./b.yaml: [audit]\n|a.yaml|audit: {}\n|b.yaml|", "single owner"},
		{"c two wildcards conflict", "legacy-clean.json", "FILES:include:\n  ./a.yaml: '*'\n  ./b.yaml: '*'\n|a.yaml|audit: {}\n|b.yaml|audit: {}\n", "single owner"},
		{"d omitted reserved audit", "legacy-clean.json", "FILE:include:\n  ./audit.yaml: [audit]\n|audit.yaml|", ""},
		{"d inline reserved audit", "legacy-clean.json", "FILE:include:\n  ./audit.yaml: [audit]\naudit: {}\n|audit.yaml|", "single owner"},
		{"e dependency audit", "legacy-clean.json", "configDependencies:\n  '@acme/pnpm-policy': 3.1.0\ninclude:\n  '@acme/pnpm-policy': [audit]\n", "audit policy is delegated to config dependency \"@acme/pnpm-policy\", which the gate does not read"},
		{"e dependency auditConfig", "legacy-clean.json", "configDependencies:\n  '@acme/pnpm-policy': 3.1.0\ninclude:\n  '@acme/pnpm-policy': [auditConfig]\n", "audit policy is delegated to config dependency \"@acme/pnpm-policy\", which the gate does not read"},
		{"e dependency unrelated", "legacy-clean.json", "configDependencies:\n  '@acme/pnpm-policy': 3.1.0\ninclude:\n  '@acme/pnpm-policy': [minimumReleaseAge, trustPolicy]\n", ""},
		{"e dependency wildcard", "legacy-clean.json", "include:\n  '@acme/pnpm-policy': '*'\n", "include \"@acme/pnpm-policy\": '*' requires a local"},
		{"f number", "legacy-clean.json", "include:\n  ./bad.yaml: 42\n", "include \"./bad.yaml\": expected a list of setting names or '*'"},
		{"f mapping", "legacy-clean.json", "include:\n  ./bad.yaml: {audit: true}\n", "include \"./bad.yaml\": expected a list of setting names or '*'"},
		{"f non-string list", "legacy-clean.json", "include:\n  ./bad.yaml: [audit, 42]\n", "include \"./bad.yaml\": setting names must be strings"},
		{"f unrelated missing files", "legacy-clean.json", "include:\n  ./catalog.yaml: [catalog]\n  ./packages.yml: [packages]\n", ""},
		{"g parent escape", "legacy-clean.json", "include:\n  ../audit.yaml: '*'\n", "read included pnpm audit policy ../audit.yaml"},
		{"g wildcard symlink escape", "legacy-clean.json", "FILES:include:\n  ./audit.yaml: '*'\n|audit.yaml|ESCAPING_SYMLINK", "read included pnpm audit policy ./audit.yaml"},
		{"h nested include", "legacy-clean.json", "FILE:include:\n  ./audit.yaml: '*'\n|audit.yaml|include: {}\n", "included pnpm audit policy ./audit.yaml must not define include"},
		{"h second document", "legacy-clean.json", "FILE:include:\n  ./audit.yaml: '*'\n|audit.yaml|{}\n---\n{}\n", "parse included pnpm audit policy ./audit.yaml"},
		{"h oversized file", "legacy-clean.json", "FILE:include:\n  ./audit.yaml: '*'\n|audit.yaml|#" + strings.Repeat("x", 2<<20), "read included pnpm audit policy ./audit.yaml"},
		{"i audit aliases separate owners", "legacy-clean.json", "FILES:include:\n  ./a.yaml: [audit]\n  ./b.yaml: [auditConfig]\n|a.yaml|audit: {}\n|b.yaml|auditConfig: {}\n", "single owner"},
		{"i wildcard alias inline conflict", "legacy-clean.json", "FILE:include:\n  ./audit.yaml: '*'\naudit: {}\n|audit.yaml|auditConfig: {}\n", "single owner"},
		{"c null inline owner", "legacy-clean.json", "FILE:include:\n  ./audit.yaml: '*'\naudit: null\n|audit.yaml|audit: {}\n", "single owner"},
		{"f non-string boolean", "legacy-clean.json", "include:\n  ./bad.yaml: [true]\n", "include \"./bad.yaml\": setting names must be strings"},
		{"f non-wildcard scalar", "legacy-clean.json", "include:\n  ./bad.yaml: audit\n", "include \"./bad.yaml\": expected a list of setting names or '*'"},
		{"f local glob unrelated", "legacy-clean.json", "include:\n  ./*.yaml: [catalog]\n", "include \"./*.yaml\" must name one local file, not a glob"},
		{"h null nested include", "legacy-clean.json", "FILE:include:\n  ./audit.yaml: '*'\n|audit.yaml|include: null\n", "included pnpm audit policy ./audit.yaml must not define include"},
		{"i same file aliases", "pnpm-ignored.json", "FILE:include:\n  ./audit.yml: [audit, auditConfig]\n|audit.yml|audit: {}\nauditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", ""},
		{"j alias wildcard live ignores", "pnpm-ignored.json", "FILES:include:\n  ./empty.yaml: &all '*'\n  ./audit.yaml: *all\n|empty.yaml||audit.yaml|" + ignored, ""},
		{"j alias list live ignores", "pnpm-ignored.json", "FILE:names: &names [audit]\ninclude:\n  ./audit.yaml: *names\n|audit.yaml|" + ignored, ""},
		{"j alias item live ignores", "pnpm-ignored.json", "FILE:name: &name audit\ninclude:\n  ./audit.yaml:\n    - *name\n|audit.yaml|" + ignored, ""},
		{"j alias mapping live ignores", "pnpm-ignored.json", "FILE:policies: &policies\n  ./audit.yaml: [audit]\ninclude: *policies\n|audit.yaml|" + ignored, ""},
		{"j alias number", "legacy-clean.json", "number: &number 42\ninclude:\n  ./bad.yaml: *number\n", "include \"./bad.yaml\": expected a list of setting names or '*'"},
		{"j alias wrong mapping", "legacy-clean.json", "policy: &policy {audit: true}\ninclude:\n  ./bad.yaml: *policy\n", "include \"./bad.yaml\": expected a list of setting names or '*'"},
		{"j explicit string item", "pnpm-ignored.json", "FILE:include:\n  ./audit.yaml: [!!str audit]\n|audit.yaml|" + ignored, ""},
		{"j explicit string wildcard", "pnpm-ignored.json", "FILE:include:\n  ./audit.yaml: !!str '*'\n|audit.yaml|" + ignored, ""},
		{"j number item", "legacy-clean.json", "include:\n  ./bad.yaml: [1]\n", "include \"./bad.yaml\": setting names must be strings"},
		{"j boolean item", "legacy-clean.json", "include:\n  ./bad.yaml: [true]\n", "include \"./bad.yaml\": setting names must be strings"},
		{"j quoted number item", "legacy-clean.json", "include:\n  ./unread.yaml: ['1']\n", ""},
		{"j self referencing alias", "legacy-clean.json", "include:\n  ./bad.yaml: &loop [*loop]\n", "decode pnpm-workspace.yaml include: yaml: anchor 'loop' value contains itself"},
		{"j null value", "legacy-clean.json", "include:\n  ./bad.yaml: null\n", "include \"./bad.yaml\": expected a list of setting names or '*'"},
		{"j boolean value", "legacy-clean.json", "include:\n  ./bad.yaml: true\n", "include \"./bad.yaml\": expected a list of setting names or '*'"},
		{"j nested list item", "legacy-clean.json", "include:\n  ./bad.yaml: [[audit]]\n", "include \"./bad.yaml\": setting names must be strings"},
		{"j null item", "legacy-clean.json", "include:\n  ./bad.yaml: [null]\n", "include \"./bad.yaml\": setting names must be strings"},
		{"j null include mapping", "legacy-clean.json", "include: null\n", "include must be a source mapping"},
	}
	for i := 0; i < len(cases) && i < 64; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditFixture(t, "pnpm", nodeAuditFixture(t, tc.report), nodeAuditStandards(t, 1), tc.policy, false)
			checkNodeAuditExceptionError(t, tc.report, tc.want, err)
		})
	}
}

// TestNodeAuditIncludeBounds checks exact and exceeded entry and wildcard-read limits.
func TestNodeAuditIncludeBounds(t *testing.T) {
	cases := []struct {
		name     string
		count    int
		wildcard bool
		want     string
	}{
		{"64 entries", 64, false, ""},
		{"65 entries", 65, false, "include exceeds 64 entries"},
		{"16 wildcard files", 16, true, ""},
		{"17 wildcard files", 17, true, "include exceeds 16 '*' files"},
		{"64 merged entries", 64, false, ""},
		{"65 merged entries", 65, false, "include exceeds 64 entries"},
	}
	for i := 0; i < len(cases) && i < 8; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			policy, files := "FILES:include:\n", ""
			for j := 0; j < tc.count && j < 65; j++ {
				path, value := fmt.Sprintf("part%d.yaml", j), "[catalog]"
				if tc.wildcard {
					value = "'*'"
					files += "|" + path + "|{}\n"
				}
				policy += "  ./" + path + ": " + value + "\n"
			}
			if strings.Contains(tc.name, "merged") {
				policy = strings.Replace(policy, "FILES:include:\n", "FILES:delegations: &delegations\n", 1) + "include:\n  <<: *delegations\n"
			}
			err := runNodeAuditFixture(t, "pnpm", nodeAuditFixture(t, "legacy-clean.json"), nodeAuditStandards(t, 1), policy+files, false)
			checkNodeAuditExceptionError(t, "", tc.want, err)
		})
	}
}

// TestNodeAuditExpiredRunner rejects clean and covered reports returned after the audit deadline.
func TestNodeAuditExpiredRunner(t *testing.T) {
	cases := []struct {
		name, manager, report string
		runErr                error
	}{
		{"clean nil pnpm", "pnpm", "legacy-clean.json", nil},
		{"covered findings pnpm", "pnpm", "pnpm-listed.json", nodeAuditExitError(1)},
		{"clean nil npm", "npm", "legacy-clean.json", nil},
		{"clean nil yarn", "yarn", "legacy-clean.json", nil},
	}
	for i := 0; i < len(cases) && i < 4; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stageNodeAuditFiles(t, root, tc.manager, nodeAuditStandards(t, 1), "")
			aCtx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
			defer cancel()
			runner := func(ctx context.Context) (string, error) {
				<-ctx.Done()
				return nodeAuditFixture(t, tc.report), tc.runErr
			}
			out, runErr := runner(aCtx)
			err := judgeNodeAudit(t.Context(), aCtx, root, tc.manager, tc.manager+" audit", out, runErr)
			checkNodeAuditExceptionError(t, "", "did not finish within its 3m0s bound", err)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline cause lost: %v", err)
			}
		})
	}
}

// nodeAuditSymlinkFixture permits recording staging failures without terminating the outer test.
type nodeAuditSymlinkFixture interface {
	Helper()
	TempDir() string
	Fatal(...any)
	Skipf(string, ...any)
}

// nodeAuditSymlinkRecorder records staging outcomes while delegating fixture storage to the test.
type nodeAuditSymlinkRecorder struct {
	*testing.T
	fatal, skipped bool
	err            error
}

// Fatal records a fatal fixture error without stopping the outer regression test.
func (r *nodeAuditSymlinkRecorder) Fatal(args ...any) {
	r.fatal = true
	if len(args) == 1 {
		if err, ok := args[0].(error); ok {
			r.err = err
		}
	}
}

// Skipf records a fixture skip without skipping the outer regression test.
func (r *nodeAuditSymlinkRecorder) Skipf(string, ...any) { r.skipped = true }

// nodeAuditSymlinkPermissionError recognizes permission denial through wrapped OS errors.
// Windows ERROR_PRIVILEGE_NOT_HELD (syscall/types_windows.go) is 1314 but does not match fs.ErrPermission;
// its numeric value permits constructing and checking it on Unix hosts too.
func nodeAuditSymlinkPermissionError(err error) bool {
	const windowsPrivilegeNotHeld = syscall.Errno(1314)
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, windowsPrivilegeNotHeld)
}

// TestNodeAuditSymlinkPermissionError permits skips only for permission failures, including Windows privilege denial.
func TestNodeAuditSymlinkPermissionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"windows privilege not held", &os.LinkError{Op: "symlink", Err: syscall.Errno(1314)}, true},
		{"permission denied", &os.LinkError{Op: "symlink", Err: syscall.EACCES}, true},
		{"file exists", &os.LinkError{Op: "symlink", Err: syscall.EEXIST}, false},
		{"missing parent", &os.LinkError{Op: "symlink", Err: syscall.ENOENT}, false},
		{"plain error", errors.New("symlink creation failed"), false},
		{"operation not permitted", &os.LinkError{Op: "symlink", Err: syscall.EPERM}, true},
		{"permission sentinel", fmt.Errorf("symlink: %w", fs.ErrPermission), true},
		{"disk full", &os.LinkError{Op: "symlink", Err: syscall.ENOSPC}, false},
		{"privilege text is insufficient", errors.New("privilege: access is denied"), false},
		{"success", nil, false},
	}
	for i := 0; i < len(cases) && i < 10; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeAuditSymlinkPermissionError(tc.err); got != tc.want {
				t.Fatalf("permission error = %v, want %v: %v", got, tc.want, tc.err)
			}
		})
	}
}

// TestNodeAuditSymlinkExistingPath requires the real staging helper to fail rather than skip a path collision.
func TestNodeAuditSymlinkExistingPath(t *testing.T) {
	// Establish host symlink permission before expecting a path-collision error.
	stageEscapingNodeAuditSymlink(t, filepath.Join(t.TempDir(), "available.yaml"), "audit: {}\n")
	linkPath := filepath.Join(t.TempDir(), "audit.yaml")
	if err := os.WriteFile(linkPath, []byte("occupied\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := &nodeAuditSymlinkRecorder{T: t}
	stageEscapingNodeAuditSymlink(recorder, linkPath, "audit: {}\n")
	if !recorder.fatal || recorder.skipped || !errors.Is(recorder.err, fs.ErrExist) {
		t.Fatalf("existing path: fatal=%v skipped=%v err=%v; want fatal file-exists error without skip", recorder.fatal, recorder.skipped, recorder.err)
	}
}

// stageEscapingNodeAuditSymlink creates a symlink pointing outside root, skipping only when the host denies symlinks.
func stageEscapingNodeAuditSymlink(t nodeAuditSymlinkFixture, linkPath, body string) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), filepath.Base(linkPath))
	if err := os.WriteFile(outside, []byte(body), 0o600); err != nil {
		t.Fatal(err)
		return
	}
	if err := os.Symlink(outside, linkPath); err != nil {
		if nodeAuditSymlinkPermissionError(err) {
			t.Skipf("symlink creation denied by host permissions: %v", err)
			return
		}
		t.Fatal(err)
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
	liveStandards := nodeAuditStandards(t, 2)
	expiredStandards, expired := nodeAuditExpiredStandards(t)
	cases := []struct{ name, manager, report, standards, workspace, want string }{
		{"a workspace missing", "pnpm", "legacy-clean.json", "", workspace, "no exception in .standards.yaml covers GHSA-fc5q-39xc-vjpf"},
		{"a package missing", "pnpm", "legacy-clean.json", "", legacy, "no exception in .standards.yaml covers GHSA-fc5q-39xc-vjpf"},
		{"b workspace expired", "pnpm", "legacy-clean.json", expiredStandards, workspace, expired},
		{"b package expired", "pnpm", "legacy-clean.json", expiredStandards, legacy, expired},
		{"c workspace live zero", "pnpm", "legacy-clean.json", liveStandards, workspace, ""},
		{"c package live zero", "pnpm", "legacy-clean.json", liveStandards, legacy, ""},
		{"c workspace live counts", "pnpm", "legacy.json", liveStandards, workspace, "found 1 high and 0 critical vulnerabilities"},
		{"c package live counts", "pnpm", "legacy.json", liveStandards, legacy, "found 1 high and 0 critical vulnerabilities"},
		{"c npm live zero", "npm", "legacy-clean.json", liveStandards, "", ""},
		{"c npm live counts", "npm", "legacy.json", liveStandards, "", "found 1 high and 0 critical vulnerabilities"},
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

// TestNodeAuditExpiryBoundary checks exact UTC midnight expiry boundaries without reading the wall clock.
func TestNodeAuditExpiryBoundary(t *testing.T) {
	entries := []config.Exception{{Rule: config.ExceptionRuleNodeAudit, Path: "package.json", GHSA: "GHSA-fc5q-39xc-vjpf", Expires: "2026-11-17"}}
	if live, expired := config.NodeAuditExceptionFor(entries, "GHSA-fc5q-39xc-vjpf", time.Date(2026, 11, 17, 23, 59, 59, 0, time.UTC)); live == nil || expired != nil {
		t.Fatalf("on expiry day before UTC midnight: live=%v expired=%v", live, expired)
	}
	if live, expired := config.NodeAuditExceptionFor(entries, "GHSA-fc5q-39xc-vjpf", time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)); live != nil || expired == nil || expired.Expires != "2026-11-17" {
		t.Fatalf("at UTC midnight after expiry day: live=%v expired=%v", live, expired)
	}
}

// TestNodeAuditExceptions covers ignored, listed, expired and legacy reports through the stage.
func TestNodeAuditExceptions(t *testing.T) {
	liveStandards := nodeAuditStandards(t, 2)
	expiredStandards, expiredWant := nodeAuditExpiredStandards(t)
	both := liveStandards + strings.TrimPrefix(strings.ReplaceAll(liveStandards, "GHSA-fc5q-39xc-vjpf", "GHSA-42xw-2xvc-qx8m"), "version: 1\nexceptions:\n")
	moderateCritVia := `{"auditReportVersion":2,"vulnerabilities":{"p":{"severity":"moderate","via":[{"severity":"critical","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}]}}}`
	missingSecondVia := `{"auditReportVersion":2,"vulnerabilities":{"p":{"severity":"high","via":[{"severity":"high","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"},{"url":"https://github.com/advisories/GHSA-42xw-2xvc-qx8m"}]}}}`
	duplicateDiffSev := `{"advisories":{"111":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"low"},"222":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"high"}}}`
	duplicateSameSevGap := `{"advisories":{"111":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"high"},"222":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"high"}},"metadata":{"vulnerabilities":{"high":2,"critical":0}}}`
	duplicateSameSevOne := `{"advisories":{"111":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"high"},"222":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"high"}},"metadata":{"vulnerabilities":{"high":1,"critical":0}}}`
	npmDiffSevHigh := `{"auditReportVersion":2,"vulnerabilities":{"p":{"severity":"high","via":[{"severity":"low","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"},{"severity":"high","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}]}}}`
	npmCritPkgHighVia := `{"auditReportVersion":2,"vulnerabilities":{"p":{"severity":"critical","via":[{"severity":"high","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}]}},"metadata":{"vulnerabilities":{"high":0,"critical":1}}}`
	networkWordTitle := `{"advisories":{"111":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf","severity":"high","title":"getaddrinfo ENOTFOUND in DNS lookup"}},"metadata":{"vulnerabilities":{"high":1,"critical":0}}}`
	cases := []struct {
		name, manager, report, standards, workspace, want string
		failed                                            bool
	}{
		{"a ignored live", "pnpm", "pnpm-ignored.json", liveStandards, "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "", false},
		{"b ignored missing", "pnpm", "pnpm-ignored.json", "version: 1\n", "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "configured ignored advisories; no exception in .standards.yaml covers GHSA-fc5q-39xc-vjpf", false},
		{"c ignored expired", "pnpm", "pnpm-ignored.json", expiredStandards, "auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", expiredWant, false},
		{"d listed missing", "pnpm", "pnpm-listed.json", "version: 1\n", "", "GHSA-fc5q-39xc-vjpf", true},
		{"e listed live", "pnpm", "pnpm-listed.json", liveStandards, "", "", true},
		{"e npm listed live", "npm", "npm-listed.json", liveStandards, "", "", true},
		{"d npm listed missing", "npm", "npm-listed.json", "version: 1\n", "", "GHSA-fc5q-39xc-vjpf", true},
		{"npm listed expired", "npm", "npm-listed.json", expiredStandards, "", expiredWant, true},
		{"npm second advisory missing", "npm", "npm-two-advisories.json", liveStandards, "", "GHSA-42xw-2xvc-qx8m has no unexpired exception", true},
		{"npm both advisories live", "npm", "npm-two-advisories.json", both, "", "", true},
		{"shared live", "npm", "npm-shared.json", liveStandards, "", "", true},
		{"shared missing", "npm", "npm-shared.json", "version: 1\n", "", "GHSA-fc5q-39xc-vjpf", true},
		{"shared second advisory missing", "npm", "npm-shared-two.json", liveStandards, "", "GHSA-42xw-2xvc-qx8m", true},
		{"diamond live", "npm", "npm-diamond.json", liveStandards, "", "", true},
		{"diamond missing", "npm", "npm-diamond.json", "version: 1\n", "", "GHSA-fc5q-39xc-vjpf", true},
		{"cycle one live", "npm", "npm-cycle-one.json", liveStandards, "", "", true},
		{"cycle shared live", "npm", "npm-cycle-shared.json", liveStandards, "", "", true},
		{"second start missing reference", "npm", "npm-shared-missing.json", liveStandards, "", "missing package missing-second", true},
		{"moderate pkg critical via missing", "npm", moderateCritVia, "version: 1\n", "", "GHSA-fc5q-39xc-vjpf has no unexpired exception", true},
		{"moderate pkg critical via live", "npm", moderateCritVia, liveStandards, "", "", true},
		{"second via missing severity", "npm", missingSecondVia, liveStandards, "", "npm audit via GHSA-42xw-2xvc-qx8m is missing severity", true},
		{"duplicate id conflicting severities", "pnpm", duplicateDiffSev, liveStandards, "", "node audit advisory GHSA-fc5q-39xc-vjpf has conflicting severities", true},
		{"duplicate id same severity counted once refuses high 2", "pnpm", duplicateSameSevGap, liveStandards, "", "1 high or critical findings counted but not named; 0 configured ignores can explain unidentified advisories", true},
		{"duplicate id same severity counted once passes high 1", "pnpm", duplicateSameSevOne, liveStandards, "", "", true},
		{"npm duplicate id conflicting severities with live exception", "npm", npmDiffSevHigh, liveStandards, "", "node audit advisory GHSA-fc5q-39xc-vjpf has conflicting severities", true},
		{"npm critical pkg high via only refused", "npm", npmCritPkgHighVia, liveStandards, "", "npm audit high or critical package p has no bounded GHSA advisory", true},
		{"advisory title with network words passes when excepted", "pnpm", networkWordTitle, liveStandards, "", "", true},
		{"advisory title with network words fails as finding when unexcepted", "pnpm", networkWordTitle, "version: 1\n", "", "GHSA-fc5q-39xc-vjpf has no unexpired exception", true},
		{"listed expired", "pnpm", "pnpm-listed.json", expiredStandards, "", expiredWant, true},
		{"f legacy", "pnpm", "legacy.json", "version: 1\n", "", "found 1 high and 0 critical vulnerabilities", false},
		{"legacy exception cannot hide counts", "pnpm", "legacy.json", liveStandards, "", "found 1 high and 0 critical vulnerabilities", false},
	}
	for i := 0; i < len(cases) && i < 64; i++ {
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
		name, out string
		runErr    error
		want      string
	}{
		{"findings exit 1", report, nodeAuditExitError(1), ""},
		{"exit 2", report, nodeAuditExitError(2), "pnpm audit --audit-level=high --json failed: exit status 2"},
		{"launch error", report, &os.PathError{Op: "fork/exec", Path: "pnpm", Err: os.ErrPermission}, "pnpm audit --audit-level=high --json failed: fork/exec pnpm: permission denied"},
		{"capture error", report, captureErr, "pnpm audit --audit-level=high --json failed: exit status 1\ncommand output exceeds 16777216 bytes per stream"},
		{"capture error truncated mid-report", `{"advisories":{"111":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf"`, captureErr, "pnpm audit --audit-level=high --json failed: exit status 1\ncommand output exceeds 16777216 bytes per stream"},
		{"findings exit 1 truncated mid-report keeps runner error", `{"advisories":{"111":{"github_advisory_id":"GHSA-fc5q-39xc-vjpf"`, nodeAuditExitError(1), "exit status 1"},
	}
	for i := 0; i < len(cases) && i < 8; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			err := runNodeAuditWithErr(t, "pnpm", tc.out, standards, "", tc.runErr)
			checkNodeAuditExceptionError(t, "", tc.want, err)
		})
	}
	t.Run("signal kill", func(t *testing.T) {
		err := runNodeAuditWithErr(t, "pnpm", report, standards, "", killedBySignalExitError(t))
		checkNodeAuditExceptionError(t, "", "pnpm audit --audit-level=high --json failed: signal: killed", err)
	})
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
	checkNodeAuditReportError(t, report, want, err)
	t.Log(err)
}

// checkNodeAuditReportError preserves filtering and deduplication assertions for shared reports.
func checkNodeAuditReportError(t *testing.T, report, want string, err error) {
	t.Helper()
	if (report == "npm-two-advisories.json" || report == "npm-shared-two.json") && strings.Contains(err.Error(), "GHSA-fc5q-39xc-vjpf") {
		t.Fatalf("excepted advisory reported: %v", err)
	}
	if (report == "npm-shared.json" || report == "npm-diamond.json") && strings.Count(err.Error(), want) != 1 {
		t.Fatalf("shared advisory must be named once: %v", err)
	}
}

// TestNodeAuditIgnoreConfiguration covers pnpm policy locations, precedence and fail-closed gaps.
func TestNodeAuditIgnoreConfiguration(t *testing.T) {
	cases := []struct{ name, report, policy, want string }{
		{"modern workspace", "pnpm-ignored.json", "audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n", ""},
		{"legacy package", "pnpm-ignored.json", `{"pnpm":{"auditConfig":{"ignoreGhsas":["GHSA-fc5q-39xc-vjpf"]}}}`, ""},
		{"delegated audit include", "pnpm-ignored.json", "FILE:include:\n  ./configs/audit.yaml: [audit]\n|configs/audit.yaml|audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n", ""},
		{"delegated auditConfig include", "pnpm-ignored.json", "FILE:include:\n  ./audit.yml: [auditConfig]\n|audit.yml|auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", ""},
		{"delegated audit wrong id", "pnpm-ignored.json", "FILE:include:\n  ./audit.yaml: [audit]\n|audit.yaml|audit:\n  ignore: [GHSA-42xw-2xvc-qx8m]\n", "no exception in .standards.yaml covers GHSA-42xw-2xvc-qx8m"},
		{"delegated audit inline conflict", "pnpm-ignored.json", "FILE:include:\n  ./audit.yaml: [audit]\naudit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n|audit.yaml|audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n", "single owner"},
		{"delegated auditConfig inline conflict", "pnpm-ignored.json", "FILE:include:\n  ./audit.yaml: [auditConfig]\nauditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n|audit.yaml|auditConfig:\n  ignoreGhsas: [GHSA-fc5q-39xc-vjpf]\n", "single owner"},
		{"delegated audit duplicate owner", "pnpm-ignored.json", "FILE:include:\n  ./a.yaml: [audit]\n  ./b.yaml: [audit]\n|a.yaml|audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n", "single owner"},
		{"delegated audit nested include", "pnpm-ignored.json", "FILE:include:\n  ./audit.yaml: [audit]\n|audit.yaml|include:\n  ./other.yaml: [audit]\naudit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n", "include"},
		{"delegated audit second document", "pnpm-ignored.json", "FILE:include:\n  ./audit.yaml: [audit]\n|audit.yaml|audit:\n  ignore: [GHSA-fc5q-39xc-vjpf]\n---\n{}\n", "parse included pnpm audit policy"},
		{"delegated audit non-local path", "pnpm-ignored.json", "include:\n  '@scope/cfg/audit.yaml': [audit]\n", "local"},
		{"delegated audit bad extension", "pnpm-ignored.json", "include:\n  ./audit.json: [audit]\n", ".yaml or .yml"},
		{"delegated audit escaping parent", "pnpm-ignored.json", "include:\n  ../audit.yaml: [audit]\n", "read included pnpm audit policy ../audit.yaml"},
		{"escaping include symlink", "pnpm-ignored.json", "ESCAPING_INCLUDE_SYMLINK", "audit.yaml"},
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
		{"npm moderate pkg duplicate id conflicting severities", "npm", `{"vulnerabilities":{"p":{"severity":"moderate","via":[{"severity":"low","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"},{"severity":"moderate","url":"https://github.com/advisories/GHSA-fc5q-39xc-vjpf"}]}}}`, `node audit advisory GHSA-fc5q-39xc-vjpf has conflicting severities "low" and "moderate"`},
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
