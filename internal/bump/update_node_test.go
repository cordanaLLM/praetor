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
// byte for byte, and each range keeps its ~ operator. The ">=" comparator beside them is
// not a range bump raises, so raising it is refused and leaves the manifest as it was.
func TestUpdatePackageManifest_Positive_PreservesLayoutAndOperators(t *testing.T) {
	dir := writePackageJSON(t, layoutManifest)
	if err := ApplyUpdate(t.Context(), dir, nodeCandidate("typescript", "5.7.3")); err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`"typescript": "~5.0.0"`+"\n    },", `"typescript": "~5.7.3"`+"\n    },",
		`"typescript":    "~5.0.0"`, `"typescript":    "~5.7.3"`,
	).Replace(layoutManifest)
	if got := readPackageJSON(t, dir); got != want {
		t.Fatalf("package.json =\n%s\nwant\n%s", got, want)
	}
	if err := ApplyUpdate(t.Context(), dir, nodeCandidate("zod", "3.23.8")); err == nil || !strings.Contains(err.Error(), `">=3.0.0"`) {
		t.Fatalf("raising the >= comparator: %v; want a refusal naming the range", err)
	}
	if got := readPackageJSON(t, dir); got != want {
		t.Fatalf("refused comparator raise changed package.json:\n%s", got)
	}
}

// The declared operator is kept even when the target names another, as a fleet catalog pin
// does: the pin contributes only its version.
func TestUpdatePackageManifest_Positive_DeclaredOperatorWins(t *testing.T) {
	dir := writePackageJSON(t, `{"dependencies":{"typescript":"~5.0.0"}}`+"\n")
	if err := ApplyUpdate(t.Context(), dir, nodeCandidate("typescript", "^5.7.3")); err != nil {
		t.Fatal(err)
	}
	if got := readPackageJSON(t, dir); got != `{"dependencies":{"typescript":"~5.7.3"}}`+"\n" {
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

// A range that is not a single version, every comparator (strict or inclusive, whose
// version bounds the range instead of naming it), an undeclared package and a target that
// is not a bare, caret or tilde version are refused, and the manifest is left as it was.
func TestUpdatePackageManifest_Negative_RefusesWhatItCannotRaise(t *testing.T) {
	cases := map[string]struct{ body, pkg, target string }{
		"workspace protocol":       {`{"dependencies":{"lib":"workspace:^1.0.0"}}`, "lib", "2.0.0"},
		"x-range":                  {`{"dependencies":{"lib":"1.x"}}`, "lib", "2.0.0"},
		"compound range":           {`{"dependencies":{"lib":">=1.0.0 <2.0.0"}}`, "lib", "2.0.0"},
		"strict greater than":      {`{"dependencies":{"lib":">1.0.0"}}`, "lib", "2.1.0"},
		"strict less than":         {`{"dependencies":{"lib":"<2.0.0"}}`, "lib", "2.1.0"},
		"inclusive upper cap":      {`{"dependencies":{"lib":"<=1.0.0"}}`, "lib", "2.1.0"},
		"inclusive lower bound":    {`{"dependencies":{"lib":">=1.0.0"}}`, "lib", "2.1.0"},
		"exact comparator":         {`{"dependencies":{"lib":"=1.0.0"}}`, "lib", "2.1.0"},
		"caret target over a cap":  {`{"dependencies":{"lib":"<2.0.0"}}`, "lib", "^2.1.0"},
		"not declared":             {`{"dependencies":{"lib":"^1.0.0"}}`, "other", "2.0.0"},
		"peer only":                {`{"peerDependencies":{"lib":"^1.0.0"}}`, "lib", "2.0.0"},
		"target is a tag":          {`{"dependencies":{"lib":"^1.0.0"}}`, "lib", "latest"},
		"target is a comparator":   {`{"dependencies":{"lib":"^1.0.0"}}`, "lib", "=2.0.0"},
		"target doubles operators": {`{"dependencies":{"lib":"^1.0.0"}}`, "lib", "^^2.0.0"},
		"invalid json":             {`{"dependencies":{"lib":"^1.0.0"}`, "lib", "2.0.0"},
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

// Every pairing of a bare, caret or tilde range with a bare, caret or tilde target keeps
// the declared operator: a bare version stays bare under a caret pin, a tilde range stays
// tilde, and a prerelease target keeps its prerelease.
func TestUpdatePackageManifest_Boundary_DeclaredOperatorForms(t *testing.T) {
	cases := map[string]struct{ spec, target, want string }{
		"caret under a bare target":  {"^1.0.0", "2.1.0", "^2.1.0"},
		"caret under a tilde target": {"^1.0.0", "~2.1.0", "^2.1.0"},
		"tilde under a caret pin":    {"~1.0.0", "^2.1.0", "~2.1.0"},
		"bare under a caret pin":     {"1.0.0", "^2.1.0", "2.1.0"},
		"bare under a tilde target":  {"1.0.0", "~2.1.0", "2.1.0"},
		"prerelease target":          {"^1.0.0", "2.0.0-rc.1", "^2.0.0-rc.1"},
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

// A target contributes only its version, so no target operator reaches the written range,
// and a target that is itself a comparator is refused. ApplyUpdate already rejects '<' and
// '>' in a target as exec-argument metacharacters; this pins raisedRange on its own.
func TestRaisedRange_Boundary_TargetOperator(t *testing.T) {
	for _, target := range []string{"2.1.0", "^2.1.0", "~2.1.0"} {
		if got, err := raisedRange(json.RawMessage(`"~1.0.0"`), target); err != nil || got != "~2.1.0" {
			t.Errorf("raisedRange(~1.0.0, %s) = %s, %v; want ~2.1.0", target, got, err)
		}
	}
	for _, target := range []string{">=2.1.0", "<=2.1.0", "=2.1.0", ">2.1.0", "<2.1.0", "latest", ""} {
		if got, err := raisedRange(json.RawMessage(`"^1.0.0"`), target); err == nil {
			t.Errorf("raisedRange(^1.0.0, %q) = %s; want refused", target, got)
		}
	}
}

// lockedPackageJSON writes body as package.json beside a pnpm-lock.yaml, so updates go
// through pnpm instead of the manifest edit.
func lockedPackageJSON(t *testing.T, body string) string {
	t.Helper()
	dir := writePackageJSON(t, body)
	if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Under a lockfile pnpm is asked for the range the manifest edit would write: the declared
// operator over the target's version, never the operator a catalog pin names.
func TestApplyNodeUpdate_Positive_PnpmUpdateKeepsDeclaredOperator(t *testing.T) {
	cases := []struct{ spec, target, call string }{
		{"~5.0.0", "^5.7.3", "pnpm update lib@~5.7.3"},
		{"5.0.0", "^5.7.3", "pnpm update lib@5.7.3"},
		{"^5.0.0", "5.7.3", "pnpm update lib@^5.7.3"},
	}
	replies := map[string]standInReply{}
	for _, tc := range cases {
		replies[tc.call] = standInReply{}
	}
	bin, log := standInToolchain(t, replies, "pnpm")
	t.Setenv("PATH", bin)
	for i, tc := range cases {
		dir := lockedPackageJSON(t, `{"dependencies":{"lib":"`+tc.spec+`"}}`)
		if err := ApplyUpdate(testDeadline(t), dir, nodeCandidate("lib", tc.target)); err != nil {
			t.Fatalf("%s raised to %s: %v", tc.spec, tc.target, err)
		}
		if got := callNames(standInCalls(t, log)); len(got) != i+1 || got[i] != tc.call {
			t.Fatalf("%s raised to %s: calls = %q, want %q last", tc.spec, tc.target, got, tc.call)
		}
	}
}

// Under a lockfile a range bump does not raise is refused before pnpm runs, so pnpm can
// neither rewrite a capped range nor move the lockfile under it.
func TestApplyNodeUpdate_Negative_PnpmNeverAskedForAnUnrankedRange(t *testing.T) {
	bin, log := standInToolchain(t, map[string]standInReply{}, "pnpm")
	t.Setenv("PATH", bin)
	for _, spec := range []string{"<9.0.0", "<=9.0.0", ">=8.0.0", ">8.0.0", "=8.0.0", "8.x", "workspace:*"} {
		body := `{"dependencies":{"eslint":"` + spec + `"}}`
		dir := lockedPackageJSON(t, body)
		if err := ApplyUpdate(testDeadline(t), dir, nodeCandidate("eslint", "^10.11.0")); err == nil {
			t.Errorf("%s: update accepted", spec)
		}
		if got := readPackageJSON(t, dir); got != body {
			t.Errorf("%s: package.json = %s", spec, got)
		}
	}
	if calls := standInCalls(t, log); len(calls) != 0 {
		t.Fatalf("pnpm ran for an unranked range: %q", callNames(calls))
	}
}

// pnpm update takes one range per package. Two declarations that raise to the same range
// are one call; two that would raise to different ranges are refused before pnpm runs.
func TestApplyNodeUpdate_Boundary_PnpmUpdateTakesOneRange(t *testing.T) {
	const call = "pnpm update lib@^2.0.0"
	bin, log := standInToolchain(t, map[string]standInReply{call: {}}, "pnpm")
	t.Setenv("PATH", bin)
	agreeing := lockedPackageJSON(t, `{"dependencies":{"lib":"^1.0.0"},"devDependencies":{"lib":"^1.2.0"}}`)
	if err := ApplyUpdate(testDeadline(t), agreeing, nodeCandidate("lib", "2.0.0")); err != nil {
		t.Fatal(err)
	}
	disagreeing := lockedPackageJSON(t, `{"dependencies":{"lib":"^1.0.0"},"devDependencies":{"lib":"~1.0.0"}}`)
	if err := ApplyUpdate(testDeadline(t), disagreeing, nodeCandidate("lib", "2.0.0")); err == nil || !strings.Contains(err.Error(), "one range") {
		t.Fatalf("disagreeing declarations: %v; want a refusal", err)
	}
	if got := callNames(standInCalls(t, log)); len(got) != 1 || got[0] != call {
		t.Fatalf("calls = %q, want only %q", got, call)
	}
}

// splitRangeOperator only parses: it reads every comparator, strict ">" and "<" included.
// rankedVersion admits only the bare, caret and tilde forms for catalog ranking, and
// raisedRange raises only those; neither decision belongs to the parser.
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
