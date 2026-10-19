// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package govuln

// The streams under testdata/scan are govulncheck v1.8.0's own JSON output (-scan symbol
// -format json) for three scratch modules requiring golang.org/x/text v0.3.7, scanned against
// testdata/vulndb, which holds GO-2022-1059 (CVE-2022-32149) as the Go vulnerability database
// publishes its affected range and symbols: symbol.stream calls
// language.ParseAcceptLanguage, package.stream imports golang.org/x/text/language and calls
// language.Make only, module.stream imports golang.org/x/text/width only. incomplete.stream is
// what the same scanner printed, before exiting 1, for a module whose package does not compile.
// The tests replay them through a stub runner, so they run offline.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testNow is the clock every statement is judged against.
var testNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// call is one command the stub runner was asked to run.
type call struct {
	name string
	args []string
}

// stubRunner answers every command with out and err and records it.
func stubRunner(out string, err error, calls *[]call) Runner {
	return func(_ context.Context, _, name string, args ...string) (string, error) {
		*calls = append(*calls, call{name: name, args: args})
		return out, err
	}
}

// fixture reads one recorded stream.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "scan", name+".stream"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// vexDocument renders an OpenVEX v0.2.0 document holding statements, issued at testNow less a day.
func vexDoc(statements ...string) string {
	return `{"@context": "https://openvex.dev/ns/v0.2.0", "@id": "https://example.com/vex/go", ` +
		`"author": "Example maintainers", "timestamp": "2026-10-05T12:00:00Z", "version": 1, ` +
		`"statements": [` + strings.Join(statements, ", ") + `]}`
}

// notAffected renders a not_affected statement for name with justification, reviewed at
// lastUpdated (RFC 3339; empty inherits the document's timestamp).
func notAffected(name, justification, lastUpdated string) string {
	updated := ""
	if lastUpdated != "" {
		updated = `"last_updated": "` + lastUpdated + `", `
	}
	return `{"vulnerability": {"name": "` + name + `"}, ` + updated + `"status": "not_affected", ` +
		`"justification": "` + justification + `", "impact_statement": "No build compiles the vulnerable package."}`
}

// module writes a module root holding vex at the default document path, when vex is not empty.
func module(t *testing.T, vex string) string {
	t.Helper()
	dir := t.TempDir()
	if vex != "" {
		path := filepath.Join(dir, "security", "vex", "go.openvex.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(vex), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// check runs Check on dir against the recorded stream named by stream.
func check(t *testing.T, dir, stream string) (*Report, error) {
	t.Helper()
	var calls []call
	return Check(t.Context(), Options{Dir: dir, Run: stubRunner(fixture(t, stream), nil, &calls), Now: func() time.Time { return testNow }})
}

// mustFail asserts the report fails with one verdict whose reason holds want.
func mustFail(t *testing.T, report *Report, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	lines := report.Lines(false)
	if !report.Failed() || len(lines) != 1 || !strings.Contains(lines[0], want) {
		t.Fatalf("failing lines = %q, want one holding %q", lines, want)
	}
}

// mustPass asserts the report passes with exactly covered verdicts.
func mustPass(t *testing.T, report *Report, err error, covered int) {
	t.Helper()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Failed() || len(report.Lines(true)) != covered {
		t.Fatalf("failing %q, covered %q; want %d covered and none failing", report.Lines(false), report.Lines(true), covered)
	}
}

// Negative (planted defect): a module calling a known-vulnerable symbol fails, and no statement
// can cover the call, not even a current not_affected one.
func TestCheck_Negative_CalledSymbolFails(t *testing.T) {
	want := "GO-2022-1059: golang.org/x/text/language.ParseAcceptLanguage is called (fixed in v0.3.8)"
	report, err := check(t, module(t, ""), "symbol")
	mustFail(t, report, err, want)
	covered := module(t, vexDoc(notAffected("GO-2022-1059", "vulnerable_code_not_in_execute_path", "")))
	report, err = check(t, covered, "symbol")
	mustFail(t, report, err, want)
	if report.Verdicts[0].Level != LevelSymbol {
		t.Errorf("level = %s", report.Verdicts[0].Level)
	}
}

// Negative (planted defect): an advisory present but not called fails without a statement: with
// no document at the declared path, and with a document that names another advisory, whose
// statement is then reported unused.
func TestCheck_Negative_UncalledAdvisoryWithoutStatementFails(t *testing.T) {
	report, err := check(t, module(t, ""), "module")
	mustFail(t, report, err, "module golang.org/x/text@v0.3.7 is required but not called, and security/vex/go.openvex.json does not exist")
	other := module(t, vexDoc(notAffected("GO-2026-0001", "vulnerable_code_not_present", "")))
	report, err = check(t, other, "package")
	mustFail(t, report, err, "package golang.org/x/text/language is imported but not called, and security/vex/go.openvex.json holds no not_affected statement")
	if !slices.Equal(report.Unused, []string{"GO-2026-0001"}) || len(report.UnusedLines()) != 1 {
		t.Errorf("unused = %q", report.Unused)
	}
}

// Positive: the same advisories pass with a valid not_affected statement: a module-level finding
// under vulnerable_code_not_present, a package-level one under vulnerable_code_not_in_execute_path,
// and a statement naming the advisory's CVE alias, which govulncheck's OSV entry lists.
func TestCheck_Positive_NotAffectedStatementCovers(t *testing.T) {
	cases := map[string]string{
		"module":  notAffected("GO-2022-1059", "vulnerable_code_not_present", ""),
		"package": notAffected("GO-2022-1059", "vulnerable_code_not_in_execute_path", "2026-10-01T09:00:00Z"),
	}
	for stream, statement := range cases {
		report, err := check(t, module(t, vexDoc(statement)), stream)
		mustPass(t, report, err, 1)
		if !strings.Contains(report.Lines(true)[0], "not called; not_affected") || report.Scanner != "govulncheck v1.8.0" {
			t.Errorf("%s: covered %q, scanner %q", stream, report.Lines(true), report.Scanner)
		}
	}
	alias := module(t, vexDoc(notAffected("CVE-2022-32149", "vulnerable_code_not_in_execute_path", "")))
	report, err := check(t, alias, "package")
	mustPass(t, report, err, 1)
	if !strings.Contains(report.Summary(), "1 advisories present, 0 failing, 1 covered") {
		t.Errorf("summary = %q", report.Summary())
	}
}

// Negative: a statement that claims the vulnerable code is absent cannot cover an imported package,
// and an expired statement covers nothing.
func TestCheck_Negative_AbsentClaimAndExpiredStatementFail(t *testing.T) {
	absent := module(t, vexDoc(notAffected("GO-2022-1059", "vulnerable_code_not_present", "")))
	report, err := check(t, absent, "package")
	mustFail(t, report, err, "justifies it as vulnerable_code_not_present, which covers a module-level finding only")
	expired := module(t, vexDoc(notAffected("GO-2022-1059", "vulnerable_code_not_present", "2026-07-01T00:00:00Z")))
	report, err = check(t, expired, "module")
	mustFail(t, report, err, "was last reviewed 2026-07-01, more than 90 days ago")
}

// modfileCase is one test case for resolveScannerModfile.
type modfileCase struct {
	name  string
	words []string
	want  []string
}

// modfileRewriteCases takes root and absPath from t.TempDir: a path built from the
// separator alone carries no volume on Windows, where filepath.IsAbs reads it as relative.
func modfileRewriteCases(root, absPath string) []modfileCase {
	expected := filepath.Join(root, "tools", "go", "go.mod")
	return []modfileCase{
		{"modfile equals form", []string{"go", "tool", "-modfile=tools/go/go.mod", "govulncheck"}, []string{"go", "tool", "-modfile=" + expected, "govulncheck"}},
		{"modfile two-word form", []string{"go", "tool", "-modfile", "tools/go/go.mod", "govulncheck"}, []string{"go", "tool", "-modfile", expected, "govulncheck"}},
		{"double-dash modfile equals form", []string{"go", "tool", "--modfile=tools/go/go.mod", "govulncheck"}, []string{"go", "tool", "--modfile=" + expected, "govulncheck"}},
		{"double-dash modfile two-word form", []string{"go", "tool", "--modfile", "tools/go/go.mod", "govulncheck"}, []string{"go", "tool", "--modfile", expected, "govulncheck"}},
		{"absolute path equals form kept", []string{"go", "tool", "-modfile=" + absPath, "govulncheck"}, []string{"go", "tool", "-modfile=" + absPath, "govulncheck"}},
		{"absolute path two-word form kept", []string{"go", "tool", "-modfile", absPath, "govulncheck"}, []string{"go", "tool", "-modfile", absPath, "govulncheck"}},
		{"no modfile unchanged", []string{"go", "tool", "govulncheck"}, []string{"go", "tool", "govulncheck"}},
		{"trailing modfile left as is", []string{"go", "tool", "-modfile"}, []string{"go", "tool", "-modfile"}},
		{"trailing double-dash modfile left as is", []string{"go", "tool", "--modfile"}, []string{"go", "tool", "--modfile"}},
	}
}

// Positive: resolveScannerModfile resolves relative -modfile flags against the repository root,
// preserving absolute paths, non-modfile words, and incomplete flags.
func TestGovuln_Positive_ScannerModfileRewrite(t *testing.T) {
	root := t.TempDir()
	absPath := filepath.Join(t.TempDir(), "abs", "tools", "go.mod")
	for _, tc := range modfileRewriteCases(root, absPath) {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveScannerModfile(tc.words, root)
			if !slices.Equal(got, tc.want) {
				t.Errorf("resolveScannerModfile(%q) = %q, want %q", tc.words, got, tc.want)
			}
		})
	}
}

// Positive: Check runs the scanner with -modfile resolved against VEXRoot when Dir is a submodule.
func TestCheck_GovulnSubmoduleAbsoluteModfile(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	vexPath := filepath.Join(root, "security", "vex", "go.openvex.json")
	if err := os.MkdirAll(filepath.Dir(vexPath), 0o750); err != nil {
		t.Fatal(err)
	}
	vex := vexDoc(notAffected("GO-2022-1059", "vulnerable_code_not_present", ""))
	if err := os.WriteFile(vexPath, []byte(vex), 0o600); err != nil {
		t.Fatal(err)
	}
	subVexPath := filepath.Join(sub, "security", "vex", "go.openvex.json")
	if err := os.MkdirAll(filepath.Dir(subVexPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subVexPath, []byte(vex), 0o600); err != nil {
		t.Fatal(err)
	}

	var calls []call
	run := stubRunner(fixture(t, "module"), nil, &calls)
	scanner := []string{"go", "tool", "-modfile=tools/go/go.mod", "govulncheck"}
	report, err := Check(t.Context(), Options{
		Dir:     sub,
		VEXRoot: root,
		Scanner: scanner,
		Run:     run,
		Now:     func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.Failed() {
		t.Fatalf("report failed: %v", report.Lines(false))
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	wantModfile := "-modfile=" + filepath.Join(root, "tools", "go", "go.mod")
	if !slices.Contains(calls[0].args, wantModfile) {
		t.Fatalf("args = %q, want containing %q", calls[0].args, wantModfile)
	}
	if slices.Contains(calls[0].args, "-modfile=tools/go/go.mod") {
		t.Fatalf("args still contains relative modfile: %q", calls[0].args)
	}
}

// Boundary: a relative repository root (the hook's default --path=.) still yields an absolute
// -modfile, so a submodule run finds the tools module.
func TestCheck_Boundary_RelativeRootYieldsAbsoluteModfile(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	vex := vexDoc(notAffected("GO-2022-1059", "vulnerable_code_not_present", ""))
	for _, dir := range []string{root, sub} {
		vexPath := filepath.Join(dir, "security", "vex", "go.openvex.json")
		if err := os.MkdirAll(filepath.Dir(vexPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vexPath, []byte(vex), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stream := fixture(t, "module") // read before the working directory leaves the package
	t.Chdir(root)
	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	var calls []call
	run := stubRunner(stream, nil, &calls)
	_, err = Check(t.Context(), Options{Dir: "sub", VEXRoot: ".", Run: run, Now: func() time.Time { return testNow },
		Scanner: []string{"go", "tool", "-modfile=tools/go/go.mod", "govulncheck"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	want := "-modfile=" + filepath.Join(abs, "tools", "go", "go.mod")
	if len(calls) != 1 || !slices.Contains(calls[0].args, want) {
		t.Fatalf("calls = %+v, want one scanner run with %q", calls, want)
	}
}
