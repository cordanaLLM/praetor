// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// rootLicenseToday is the fixed day the root licence tests judge expiry against.
var rootLicenseToday = time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)

// eupl stands for the text of LICENSES/EUPL-1.2.txt.
const eupl = "EUROPEAN UNION PUBLIC LICENCE v. 1.2\nEUPL (c) the European Union 2007, 2016\n"

// rootDefault is a REUSE.toml labelling the whole tree EUPL-1.2.
const rootDefault = "version = 1\n\n[[annotations]]\npath = \"**\"\nSPDX-License-Identifier = \"EUPL-1.2\"\n"

// unionDefault labels the whole tree EUPL-1.2 with four globs no one of which covers it alone, as
// a repository that spells out its dotfiles does, followed by a narrower override.
const unionDefault = "version = 1\n\n[[annotations]]\npath = [\"*\", \".*\", \"*/**\", \".*/**\"]\nprecedence = \"closest\"\n" +
	"SPDX-License-Identifier = \"EUPL-1.2\"\n\n[[annotations]]\npath = [\"vendor/**\"]\nSPDX-License-Identifier = \"MIT\"\n"

// licensedRoot writes files, relative path to text, below a fresh root and returns it.
func licensedRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, text := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// checkRoot runs CheckRootLicense over root with exceptions, failing on an error.
func checkRoot(t *testing.T, root string, exceptions ...config.Exception) RootLicenseReport {
	t.Helper()
	report, err := CheckRootLicense(context.Background(), RootLicenseOptions{Root: root, Exceptions: exceptions, Today: rootLicenseToday})
	if err != nil {
		t.Fatalf("CheckRootLicense: %v", err)
	}
	return report
}

// keep is a root-license-notice exception for name expiring on expires.
func keep(name, expires string) config.Exception {
	return config.Exception{Rule: config.ExceptionRuleRootLicenseNotice, Path: name, Reason: "upstream notice kept verbatim", Expires: expires}
}

// Positive: LICENSE holding the text of the licence the whole-tree annotation declares passes,
// in its CRLF checkout too; without REUSE.toml the one text of LICENSES/ is the declared licence;
// an upstream COPYING kept by a live exception passes and is named; NOTICE and a LICENSES text
// are no root licence files.
func TestCheckRootLicensePositive(t *testing.T) {
	base := map[string]string{LicensesDir + "/EUPL-1.2.txt": eupl, LicensesDir + "/MIT.txt": "MIT License\n", ReuseFile: rootDefault,
		RootLicenseFile: eupl, "NOTICE": "upstream notice\n"}
	if report := checkRoot(t, licensedRoot(t, base)); report.Skipped != "" || report.License != "EUPL-1.2" || len(report.Findings) != 0 {
		t.Fatalf("matching LICENSE: %+v", report)
	}
	crlf := withFile(base, RootLicenseFile, strings.ReplaceAll(eupl, "\n", "\r\n"))
	if report := checkRoot(t, licensedRoot(t, crlf)); len(report.Findings) != 0 {
		t.Fatalf("a CRLF checkout of LICENSE: %+v", report)
	}
	single := licensedRoot(t, map[string]string{LicensesDir + "/MIT.txt": "MIT License\n", RootLicenseFile: "MIT License\n"})
	if report := checkRoot(t, single); report.License != "MIT" || len(report.Findings) != 0 {
		t.Fatalf("one LICENSES text, no REUSE.toml: %+v", report)
	}
	union := withFile(base, ReuseFile, unionDefault)
	if report := checkRoot(t, licensedRoot(t, union)); report.Skipped != "" || report.License != "EUPL-1.2" || len(report.Findings) != 0 {
		t.Fatalf("a whole-tree default spelled as a union of globs: %+v", report)
	}
	kept := withFile(base, "COPYING", "GNU GENERAL PUBLIC LICENSE\n")
	if report := checkRoot(t, licensedRoot(t, kept), keep("COPYING", "2026-12-31")); len(report.Findings) != 0 || !slices.Equal(report.Kept, []string{"COPYING"}) {
		t.Fatalf("an upstream COPYING kept by a live exception: %+v", report)
	}
}

// Negative: LICENSE with another text, a missing LICENSE, a missing text of the declared licence,
// every other licence-named root file without an exception, an expired exception and an
// exception that keeps nothing each fail, naming the file or entry.
func TestCheckRootLicenseNegative(t *testing.T) {
	base := map[string]string{LicensesDir + "/EUPL-1.2.txt": eupl, ReuseFile: rootDefault, RootLicenseFile: eupl}
	for name, tc := range map[string]struct {
		files      map[string]string
		exceptions []config.Exception
		want       string
	}{
		"other text":      {withFile(base, RootLicenseFile, "MIT License\n"), nil, "LICENSE differs from LICENSES/EUPL-1.2.txt"},
		"no LICENSE":      {without(base, RootLicenseFile), nil, "LICENSE is missing: copy LICENSES/EUPL-1.2.txt to LICENSE"},
		"no text":         {withFile(base, ReuseFile, strings.Replace(rootDefault, "EUPL-1.2", "Apache-2.0", 1)), nil, "LICENSES/Apache-2.0.txt is missing"},
		"COPYING":         {withFile(base, "COPYING", "x\n"), nil, "COPYING: a second root licence file beside LICENSE"},
		"LICENSE.md":      {withFile(base, "LICENSE.md", "x\n"), nil, "LICENSE.md: a second root licence file"},
		"LICENCE":         {withFile(base, "LICENCE", "x\n"), nil, "LICENCE: a second root licence file"},
		"LICENSE-MIT":     {withFile(base, "LICENSE-MIT", "x\n"), nil, "LICENSE-MIT: a second root licence file"},
		"lower case":      {withFile(base, "copying.txt", "x\n"), nil, "copying.txt: a second root licence file"},
		"expired":         {withFile(base, "COPYING", "x\n"), []config.Exception{keep("COPYING", "2026-10-06")}, "its root-license-notice exception expired on 2026-10-06"},
		"stale exception": {base, []config.Exception{keep("COPYING", "2026-12-31")}, "exceptions entry COPYING (root-license-notice): keeps no root file"},
		"union default": {withFile(withFile(withFile(base, ReuseFile, unionDefault), LicensesDir+"/MIT.txt", "MIT License\n"), "LICENSE-MIT", "MIT License\n"),
			nil, "LICENSE-MIT: a second root licence file"},
	} {
		report := checkRoot(t, licensedRoot(t, tc.files), tc.exceptions...)
		if !slices.ContainsFunc(report.Findings, func(finding string) bool { return strings.Contains(finding, tc.want) }) {
			t.Errorf("%s: want a finding containing %q, got %+v", name, tc.want, report)
		}
	}
}

// Boundary: a root without LICENSES/ and a declaration that is no single licence skip with the
// reason; a directory named like a licence is no licence file; a LICENSE with mixed line
// endings compares byte for byte and says so; a nil context is refused.
func TestCheckRootLicenseBoundary(t *testing.T) {
	if report := checkRoot(t, licensedRoot(t, map[string]string{RootLicenseFile: eupl, "COPYING": "x\n"})); !strings.Contains(report.Skipped, "no LICENSES/") {
		t.Errorf("no LICENSES/: %+v", report)
	}
	dual := map[string]string{LicensesDir + "/MIT.txt": "m\n", LicensesDir + "/Apache-2.0.txt": "a\n",
		ReuseFile: strings.Replace(rootDefault, `"EUPL-1.2"`, `"MIT OR Apache-2.0"`, 1)}
	if report := checkRoot(t, licensedRoot(t, dual)); !strings.Contains(report.Skipped, `"MIT OR Apache-2.0", not one licence`) {
		t.Errorf("a dual-licensed whole tree: %+v", report)
	}
	several := map[string]string{LicensesDir + "/MIT.txt": "m\n", LicensesDir + "/Apache-2.0.txt": "a\n"}
	if report := checkRoot(t, licensedRoot(t, several)); !strings.Contains(report.Skipped, "holds 2 licence texts") {
		t.Errorf("several texts, no REUSE.toml: %+v", report)
	}
	gap := withFile(several, ReuseFile, strings.Replace(unionDefault, `"*/**", `, "", 1))
	if report := checkRoot(t, licensedRoot(t, gap)); !strings.Contains(report.Skipped, "holds 2 licence texts") {
		t.Errorf("a union of globs that leaves subdirectories out is no whole-tree default: %+v", report)
	}
	base := map[string]string{LicensesDir + "/EUPL-1.2.txt": eupl, ReuseFile: rootDefault, RootLicenseFile: eupl, "LICENSE.d/readme": "x\n"}
	if report := checkRoot(t, licensedRoot(t, base)); len(report.Findings) != 0 {
		t.Errorf("a directory named like a licence: %+v", report)
	}
	mixed := withFile(base, RootLicenseFile, strings.Replace(eupl, "\n", "\r\n", 1))
	if report := checkRoot(t, licensedRoot(t, mixed)); len(report.Findings) != 1 || !strings.Contains(report.Findings[0], "compared byte for byte") {
		t.Errorf("mixed line endings: %+v", report)
	}
	//nolint:staticcheck // SA1012: the nil context is the refusal under test.
	if _, err := CheckRootLicense(nil, RootLicenseOptions{Root: t.TempDir()}); err == nil {
		t.Error("a nil context was accepted")
	}
}

// withFile returns a copy of files with rel set to text.
func withFile(files map[string]string, rel, text string) map[string]string {
	out := make(map[string]string, len(files)+1)
	for key, value := range files {
		out[key] = value
	}
	out[rel] = text
	return out
}

// without returns a copy of files without rel.
func without(files map[string]string, rel string) map[string]string {
	out := withFile(files, rel, "")
	delete(out, rel)
	return out
}
