// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package govuln

import (
	"strings"
	"testing"
	"time"
)

// documentIssue is the issue line vexDoc renders, which the tests below replace.
const documentIssue = `"timestamp": "2026-10-05T12:00:00Z"`

// Positive: a document carrying every field OpenVEX v0.2.0 defines, and the supplier go-vex
// writes on the document and on a product and subcomponent (pkg/vex Metadata and Component),
// decodes and covers the advisory. The strict decode refused the go-vex suppliers before.
func TestCheck_Positive_EveryOpenVEXAndGoVEXFieldDecodes(t *testing.T) {
	component := `"@id": "pkg:golang/example.com/app@v1.0.0", "identifiers": {"purl": "pkg:golang/example.com/app@v1.0.0"}, ` +
		`"hashes": {"sha-256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}, "supplier": "Example maintainers"`
	statement := `{"@id": "https://example.com/vex/go#1", "version": 1, ` +
		`"vulnerability": {"@id": "https://pkg.go.dev/vuln/GO-2022-1059", "name": "GO-2022-1059", "description": "Denial of service", "aliases": ["CVE-2022-32149"]}, ` +
		`"timestamp": "2026-10-01T00:00:00Z", "last_updated": "2026-10-02T00:00:00Z", ` +
		`"products": [{` + component + `, "subcomponents": [{"@id": "pkg:golang/golang.org/x/text@v0.3.7", "supplier": "The Go Authors"}]}], ` +
		`"status": "not_affected", "supplier": "Example maintainers", "status_notes": "Checked with go list -deps.", ` +
		`"justification": "vulnerable_code_not_present", "impact_statement": "No package of it is built.", ` +
		`"action_statement": "None needed.", "action_statement_timestamp": "2026-10-02T00:00:00Z"}`
	document := strings.Replace(vexDoc(statement), documentIssue,
		documentIssue+`, "last_updated": "2026-10-05T13:00:00Z", "role": "Document Creator", "tooling": "vexctl", "supplier": "Example maintainers"`, 1)
	report, err := check(t, module(t, document), "module")
	mustPass(t, report, err, 1)
	if !strings.Contains(report.Lines(true)[0], "reviewed 2026-10-02") {
		t.Errorf("covered = %q, want the statement's own last_updated as its review", report.Lines(true))
	}
}

// Negative (planted defect): a statement without a time of its own is reviewed when the document
// was issued, so an edit that moves the document's last_updated does not renew it: issued 100 days
// ago and edited yesterday, it has expired. A statement the edit did review, with its own
// last_updated, still covers its advisory.
func TestCheck_Negative_DocumentEditDoesNotRenewAStatement(t *testing.T) {
	issued := testNow.Add(-100 * 24 * time.Hour).Format(time.RFC3339)
	edited := testNow.Add(-24 * time.Hour).Format(time.RFC3339)
	edit := func(statement string) string {
		return strings.Replace(vexDoc(statement), documentIssue,
			`"timestamp": "`+issued+`", "last_updated": "`+edited+`"`, 1)
	}
	report, err := check(t, module(t, edit(notAffected("GO-2022-1059", "vulnerable_code_not_present", ""))), "module")
	mustFail(t, report, err, "was last reviewed "+issued[:len(reviewDateLayout)]+", more than 90 days ago")
	report, err = check(t, module(t, edit(notAffected("GO-2022-1059", "vulnerable_code_not_present", edited))), "module")
	mustPass(t, report, err, 1)
}

// Boundary: govulncheck v1.8.0 adds the standard library to the module graph as "stdlib" at the
// version of the toolchain that scans (x/vuln internal/vulncheck/packages.go NewPackageGraph), so
// an advisory against that toolchain is a module-level finding even when no affected package is
// imported. It fails like any other uncovered advisory, and a not_affected statement covers it
// until the toolchain is upgraded. The stream is the shape of a v1.8.0 module finding for
// GO-2026-5037 (crypto/x509, fixed in Go 1.25.11 and 1.26.4) under Go 1.26.3.
func TestCheck_Boundary_StandardLibraryAdvisoryIsAModuleFinding(t *testing.T) {
	stream := cleanConfig +
		`{"osv": {"id": "GO-2026-5037", "aliases": ["CVE-2026-27145"]}}` +
		`{"finding": {"osv": "GO-2026-5037", "fixed_version": "v1.26.4", "trace": [{"module": "stdlib", "version": "v1.26.3"}]}}`
	var calls []call
	run := func(dir string) (*Report, error) {
		return Check(t.Context(), Options{Dir: dir, Run: stubRunner(stream, nil, &calls), Now: func() time.Time { return testNow }})
	}
	report, err := run(module(t, ""))
	mustFail(t, report, err,
		"GO-2026-5037: module stdlib@v1.26.3 is required but not called, and security/vex/go.openvex.json does not exist to hold a not_affected statement for it")
	report, err = run(module(t, vexDoc(notAffected("CVE-2026-27145", "vulnerable_code_not_present", ""))))
	mustPass(t, report, err, 1)
	if report.Verdicts[0].Level != LevelModule || report.Verdicts[0].FixedVersion != "v1.26.4" {
		t.Errorf("verdict = %+v, want a module-level finding fixed in v1.26.4", report.Verdicts[0])
	}
}
