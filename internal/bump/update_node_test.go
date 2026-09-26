// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// layoutManifest has a key order no JSON encoder would produce, four-space indentation,
// an escaped string and a peer range that is not a bump target.
const layoutManifest = `{
    "name": "app",
    "version": "1.0.0",
    "description": "café <b>&</b>",
    "scripts": {"test": "node test.js"},
    "devDependencies": {
        "zod": ">=3.0.0",
        "typescript": "~5.0.0"
    },
    "peerDependencies": {"typescript": "~5.0.0"},
    "dependencies": {
        "typescript":    "~5.0.0"
    }
}
`

func writePackageJSON(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readPackageJSON(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func nodeCandidate(pkg, target string) UpgradeCandidate {
	return UpgradeCandidate{Package: pkg, CurrentVersion: "0.0.0", TargetVersion: target, ManifestType: "package.json"}
}

// Only the range strings change: key order, indentation, escapes and the peer range stay
// byte for byte, and each range keeps its ~ or >= operator.
func TestUpdatePackageManifest_Positive_PreservesLayoutAndOperators(t *testing.T) {
	dir := writePackageJSON(t, layoutManifest)
	if err := ApplyUpdate(t.Context(), dir, nodeCandidate("typescript", "5.7.3")); err != nil {
		t.Fatal(err)
	}
	if err := ApplyUpdate(t.Context(), dir, nodeCandidate("zod", "3.23.8")); err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`"zod": ">=3.0.0"`, `"zod": ">=3.23.8"`,
		`"typescript": "~5.0.0"`+"\n    },", `"typescript": "~5.7.3"`+"\n    },",
		`"typescript":    "~5.0.0"`, `"typescript":    "~5.7.3"`,
	).Replace(layoutManifest)
	if got := readPackageJSON(t, dir); got != want {
		t.Fatalf("package.json =\n%s\nwant\n%s", got, want)
	}
}

// A target that names its own operator, as a fleet catalog pin does, sets it.
func TestUpdatePackageManifest_Positive_TargetOperatorWins(t *testing.T) {
	dir := writePackageJSON(t, `{"dependencies":{"typescript":"~5.0.0"}}`+"\n")
	if err := ApplyUpdate(t.Context(), dir, nodeCandidate("typescript", "^5.7.3")); err != nil {
		t.Fatal(err)
	}
	if got := readPackageJSON(t, dir); got != `{"dependencies":{"typescript":"^5.7.3"}}`+"\n" {
		t.Fatalf("package.json = %s", got)
	}
}

// A pnpm update that fails after printing output is an error, and under a lockfile neither
// package.json nor pnpm-lock.yaml is touched.
func TestApplyNodeUpdate_Negative_PnpmFailureWithOutputIsAnError(t *testing.T) {
	dir := writePackageJSON(t, `{"dependencies":{"typescript":"~5.0.0"}}`+"\n")
	lock := filepath.Join(dir, "pnpm-lock.yaml")
	if err := os.WriteFile(lock, []byte("lockfileVersion: '9.0'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	failing := "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tfmt.Println(\"Progress: resolved 12\")\n\tfmt.Fprintln(os.Stderr, \"ERR_PNPM_FETCH_404\")\n\tos.Exit(1)\n}\n"
	testsupport.BuildExecutable(t, bin, "pnpm", failing)
	t.Setenv("PATH", bin)

	err := ApplyUpdate(t.Context(), dir, nodeCandidate("typescript", "5.7.3"))
	if err == nil || !strings.Contains(err.Error(), "ERR_PNPM_FETCH_404") {
		t.Fatalf("failed pnpm update not reported: %v", err)
	}
	if got := readPackageJSON(t, dir); got != `{"dependencies":{"typescript":"~5.0.0"}}`+"\n" {
		t.Fatalf("package.json edited after pnpm failed: %s", got)
	}
	if data, readErr := os.ReadFile(lock); readErr != nil || string(data) != "lockfileVersion: '9.0'\n" {
		t.Fatalf("lockfile changed: %q, %v", data, readErr)
	}
}

// A range that is not a single version, a strict comparator that would exclude its own
// target, an undeclared package and a target that is not a version are refused, and the
// manifest is left as it was.
func TestUpdatePackageManifest_Negative_RefusesWhatItCannotRaise(t *testing.T) {
	cases := map[string]struct{ body, pkg, target string }{
		"workspace protocol":  {`{"dependencies":{"lib":"workspace:^1.0.0"}}`, "lib", "2.0.0"},
		"x-range":             {`{"dependencies":{"lib":"1.x"}}`, "lib", "2.0.0"},
		"compound range":      {`{"dependencies":{"lib":">=1.0.0 <2.0.0"}}`, "lib", "2.0.0"},
		"strict greater than": {`{"dependencies":{"lib":">1.0.0"}}`, "lib", "2.1.0"},
		"strict less than":    {`{"dependencies":{"lib":"<2.0.0"}}`, "lib", "2.1.0"},
		"not declared":        {`{"dependencies":{"lib":"^1.0.0"}}`, "other", "2.0.0"},
		"peer only":           {`{"peerDependencies":{"lib":"^1.0.0"}}`, "lib", "2.0.0"},
		"target is a tag":     {`{"dependencies":{"lib":"^1.0.0"}}`, "lib", "latest"},
		"invalid json":        {`{"dependencies":{"lib":"^1.0.0"}`, "lib", "2.0.0"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writePackageJSON(t, tc.body)
			if err := ApplyUpdate(t.Context(), dir, nodeCandidate(tc.pkg, tc.target)); err == nil {
				t.Fatal("update accepted")
			}
			if got := readPackageJSON(t, dir); got != tc.body {
				t.Fatalf("refused update changed package.json: %s", got)
			}
		})
	}
}

// A dependency declared only under devDependencies is raised there.
func TestUpdatePackageManifest_Boundary_DevDependencyOnly(t *testing.T) {
	body := "{\n  \"devDependencies\": {\n    \"vitest\": \"^1.0.0\"\n  }\n}\n"
	dir := writePackageJSON(t, body)
	if err := ApplyUpdate(t.Context(), dir, nodeCandidate("vitest", "2.1.0")); err != nil {
		t.Fatal(err)
	}
	if got := readPackageJSON(t, dir); got != strings.Replace(body, "^1.0.0", "^2.1.0", 1) {
		t.Fatalf("package.json = %s", got)
	}
}

// Inclusive comparators keep their operator, and a target operator replaces a strict one,
// so every written range still contains the version it was raised to.
func TestUpdatePackageManifest_Boundary_InclusiveAndReplacedStrictOperators(t *testing.T) {
	cases := map[string]struct{ spec, target, want string }{
		"less than or equal":       {"<=1.0.0", "2.1.0", "<=2.1.0"},
		"exact":                    {"=1.0.0", "2.1.0", "=2.1.0"},
		"strict replaced by caret": {">1.0.0", "^2.1.0", "^2.1.0"},
		"strict replaced by tilde": {"<2.0.0", "~2.1.0", "~2.1.0"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writePackageJSON(t, `{"dependencies":{"lib":"`+tc.spec+`"}}`)
			if err := ApplyUpdate(t.Context(), dir, nodeCandidate("lib", tc.target)); err != nil {
				t.Fatal(err)
			}
			if got, want := readPackageJSON(t, dir), `{"dependencies":{"lib":"`+tc.want+`"}}`; got != want {
				t.Fatalf("package.json = %s, want %s", got, want)
			}
		})
	}
}

// A target operator decides the written range, so a strict one is refused even over an
// inclusive current range, and an inclusive one is written. ApplyUpdate already rejects
// '<' and '>' in a target as exec-argument metacharacters; this pins raisedRange on its own.
func TestRaisedRange_Boundary_TargetOperator(t *testing.T) {
	for target, want := range map[string]string{">=2.1.0": `">=2.1.0"`, "<=2.1.0": `"<=2.1.0"`, "=2.1.0": `"=2.1.0"`} {
		if got, err := raisedRange(json.RawMessage(`"^1.0.0"`), target); err != nil || got != want {
			t.Errorf("raisedRange(^1.0.0, %s) = %s, %v; want %s", target, got, err, want)
		}
	}
	for _, target := range []string{">2.1.0", "<2.1.0"} {
		if got, err := raisedRange(json.RawMessage(`"^1.0.0"`), target); err == nil {
			t.Errorf("raisedRange(^1.0.0, %s) = %s; want refused", target, got)
		}
	}
}

// splitRangeOperator only parses: it reads strict ">" and "<" so a scan can report the
// version they name. raisedRange, not the parser, refuses to raise them.
func TestSplitRangeOperator_Boundaries(t *testing.T) {
	valid := map[string][2]string{
		"1.2.3": {"", "1.2.3"}, "^1.2.3": {"^", "1.2.3"}, "~1.2.3": {"~", "1.2.3"},
		">=1.2.3": {">=", "1.2.3"}, "<=1.2.3": {"<=", "1.2.3"}, ">1.2.3": {">", "1.2.3"},
		"<2.0.0": {"<", "2.0.0"}, "=1.2.3": {"=", "1.2.3"}, "^2.0.0-rc.1": {"^", "2.0.0-rc.1"},
	}
	for spec, want := range valid {
		operator, version, ok := splitRangeOperator(spec)
		if !ok || operator != want[0] || version != want[1] {
			t.Errorf("splitRangeOperator(%q) = %q, %q, %v; want %q, %q", spec, operator, version, ok, want[0], want[1])
		}
	}
	for _, spec := range []string{"", "*", "latest", "1.x", "^1", ">= 1.2.3", "^1.2.3 ", "^1.2.3 || ^2.0.0", "workspace:*", "file:../x", "npm:lib@1.0.0"} {
		if operator, version, ok := splitRangeOperator(spec); ok {
			t.Errorf("splitRangeOperator(%q) = %q, %q, true; want refused", spec, operator, version)
		}
	}
}
