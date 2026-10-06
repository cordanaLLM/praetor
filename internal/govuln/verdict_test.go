// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package govuln

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// cleanConfig is the configuration message govulncheck v1.8.0 opens a symbol scan of source with.
const cleanConfig = `{"config": {"protocol_version": "v1.0.0", "scanner_name": "govulncheck", ` +
	`"scanner_version": "v1.8.0", "scan_level": "symbol", "scan_mode": "source"}}`

// Negative (planted defect): a scan that did not complete is no verdict, never a pass: the
// recorded output of a scan that exited 1 after printing its configuration, empty output, output
// that does not open with the configuration, another scan level, another protocol and output that
// is no JSON. Each wraps ErrScanIncomplete.
func TestCheck_Negative_IncompleteScanIsNoVerdict(t *testing.T) {
	recorded := fixture(t, "incomplete")
	cases := map[string]struct {
		out string
		err error
	}{
		"exit 1 after config": {recorded, errors.New("exit status 1: govulncheck: loading packages")},
		"empty output":        {"", nil},
		"finding first":       {`{"finding": {"osv": "GO-2022-1059", "trace": [{"module": "golang.org/x/text"}]}}`, nil},
		"module level scan":   {strings.Replace(cleanConfig, `"symbol"`, `"module"`, 1), nil},
		"protocol v2":         {strings.Replace(cleanConfig, "v1.0.0", "v2.0.0", 1), nil},
		"not JSON":            {"govulncheck: no go.mod file", nil},
		"finding no trace":    {cleanConfig + `{"finding": {"osv": "GO-2022-1059"}}`, nil},
	}
	for name, tc := range cases {
		var calls []call
		report, err := Check(t.Context(), Options{Dir: module(t, ""), Run: stubRunner(tc.out, tc.err, &calls), Now: func() time.Time { return testNow }})
		if !errors.Is(err, ErrScanIncomplete) || report != nil {
			t.Errorf("%s: report %v, err %v; want ErrScanIncomplete and no report", name, report, err)
		}
	}
}

// Negative: a document that does not validate is no verdict, and the scan never starts. Each
// wraps ErrInvalidVEX and names what is wrong.
func TestCheck_Negative_MalformedVEXIsNoVerdict(t *testing.T) {
	valid := notAffected("GO-2022-1059", "vulnerable_code_not_present", "")
	cases := map[string]struct{ doc, want string }{
		"not JSON":              {"{", "unexpected EOF"},
		"two documents":         {vexDoc(valid) + "{}", "exactly one JSON document"},
		"older context":         {strings.Replace(vexDoc(valid), "v0.2.0", "v0.0.1", 1), "@context"},
		"no author":             {strings.Replace(vexDoc(valid), `"author": "Example maintainers", `, "", 1), "author are required"},
		"no statements":         {strings.Replace(vexDoc(valid), `"statements": [`+valid+`]`, `"tooling": "x"`, 1), "statements is required"},
		"null statements":       {strings.Replace(vexDoc(valid), `[`+valid+`]`, "null", 1), "null"},
		"unknown status":        {vexDoc(strings.Replace(valid, `"not_affected"`, `"wont_fix"`, 1)), `status "wont_fix"`},
		"unknown justify":       {vexDoc(strings.Replace(valid, "vulnerable_code_not_present", "not_reachable", 1)), `justification "not_reachable"`},
		"no impact":             {vexDoc(strings.Replace(valid, `"impact_statement"`, `"status_notes"`, 1)), "justification and an impact_statement"},
		"no name":               {vexDoc(strings.Replace(valid, `"name": "GO-2022-1059"`, `"@id": "x"`, 1)), "vulnerability.name is required"},
		"unknown field":         {vexDoc(strings.Replace(valid, `"status"`, `"expires": "2027-01-01", "status"`, 1)), "unknown field"},
		"future review":         {vexDoc(notAffected("GO-2022-1059", "vulnerable_code_not_present", "2027-01-01T00:00:00Z")), "lies in the future"},
		"timestamp not RFC3339": {vexDoc(notAffected("GO-2022-1059", "vulnerable_code_not_present", "2026-10-01")), "no RFC 3339 timestamp"},
	}
	for name, tc := range cases {
		var calls []call
		_, err := Check(t.Context(), Options{Dir: module(t, tc.doc), Run: stubRunner(cleanConfig, nil, &calls), Now: func() time.Time { return testNow }})
		if !errors.Is(err, ErrInvalidVEX) || !strings.Contains(err.Error(), tc.want) || len(calls) != 0 {
			t.Errorf("%s: err %v, %d scans; want ErrInvalidVEX naming %q and no scan", name, err, len(calls), tc.want)
		}
	}
}

// Boundary: a statement reviewed exactly MaxStatementAge ago still covers and one a second older
// does not; a clean scan passes with no verdict; the latest of two statements speaks for the
// advisory, and two with the same review time leave it without an answer.
func TestCheck_Boundary_ReviewAgeLatestStatementAndCleanScan(t *testing.T) {
	edge := testNow.Add(-MaxStatementAge)
	atEdge := module(t, vexDoc(notAffected("GO-2022-1059", "vulnerable_code_not_present", edge.Format(time.RFC3339))))
	report, err := check(t, atEdge, "module")
	mustPass(t, report, err, 1)
	pastEdge := module(t, vexDoc(notAffected("GO-2022-1059", "vulnerable_code_not_present", edge.Add(-time.Second).Format(time.RFC3339))))
	report, err = check(t, pastEdge, "module")
	mustFail(t, report, err, "more than 90 days ago")

	var calls []call
	report, err = Check(t.Context(), Options{Dir: module(t, ""), Run: stubRunner(cleanConfig, nil, &calls), Now: func() time.Time { return testNow }})
	mustPass(t, report, err, 0)

	affected := `{"vulnerability": {"name": "GO-2022-1059"}, "timestamp": "2026-09-01T00:00:00Z", "status": "affected"}`
	newer := notAffected("GO-2022-1059", "vulnerable_code_not_present", "2026-10-01T00:00:00Z")
	report, err = check(t, module(t, vexDoc(affected, newer)), "module")
	mustPass(t, report, err, 1)
	older := notAffected("GO-2022-1059", "vulnerable_code_not_present", "2026-08-01T00:00:00Z")
	report, err = check(t, module(t, vexDoc(affected, older)), "module")
	mustFail(t, report, err, "the latest statement in security/vex/go.openvex.json says affected")
	tied := notAffected("GO-2022-1059", "vulnerable_code_not_present", "2026-09-01T00:00:00Z")
	report, err = check(t, module(t, vexDoc(affected, tied)), "module")
	mustFail(t, report, err, "two statements in security/vex/go.openvex.json name it with the same review time")
}

// Boundary: the scanner command a caller names runs with ScanArgs appended, an empty one runs
// govulncheck from PATH, and a manifest's security.go_vex moves the document.
func TestCheck_Boundary_ScannerCommandAndDeclaredDocument(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".standards.yaml"), []byte("version: 1\nsecurity:\n  go_vex: \"vex.json\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vex.json"), []byte(vexDoc(notAffected("GO-2022-1059", "vulnerable_code_not_present", ""))), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []call
	scanner := []string{"go", "tool", "-modfile=tools/go/go.mod", "govulncheck"}
	report, err := Check(t.Context(), Options{Dir: dir, Scanner: scanner, Run: stubRunner(fixture(t, "module"), nil, &calls), Now: func() time.Time { return testNow }})
	mustPass(t, report, err, 1)
	want := append(slices.Clone(scanner[1:]), ScanArgs()...)
	if len(calls) != 1 || calls[0].name != "go" || !slices.Equal(calls[0].args, want) || report.VEXPath != "vex.json" {
		t.Fatalf("calls %+v, document %q", calls, report.VEXPath)
	}
	calls = nil
	if _, err := Check(t.Context(), Options{Dir: dir, Run: stubRunner(cleanConfig, nil, &calls)}); err != nil || calls[0].name != DefaultScanner {
		t.Fatalf("default scanner: %+v, %v", calls, err)
	}
	if _, err := Check(t.Context(), Options{Dir: dir, Scanner: []string{""}, Run: stubRunner(cleanConfig, nil, &calls)}); err == nil {
		t.Fatal("an empty scanner word ran")
	}
}
