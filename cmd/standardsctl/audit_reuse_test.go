// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/supplychain"
)

// reuseWarnings runs the vendored license check of the documentation families over a repository
// whose REUSE.toml is text; a nil text writes no REUSE.toml.
func reuseWarnings(t *testing.T, text *string) []string {
	t.Helper()
	root := t.TempDir()
	if text != nil {
		writeFixtureFile(t, root, supplychain.ReuseFile, *text)
	}
	return vendoredLicenseWarnings(t.Context(), root, adopt.DocumentationFamilies())
}

// reuseOverrideTable is the override annotation of reuseWithOverride on its own.
const reuseOverrideTable = "\n[[annotations]]\npath = [\"tools/figures/third_party/interfig/upstream/**\"]\n" +
	"precedence = \"override\"\nSPDX-License-Identifier = \"MIT\"\n"

// Positive: an override annotation naming the vendored tree MIT, alone or in an expression, in
// double or single quotes, on one line or in a multi-line array, satisfies the check, as it does
// followed by a table for paths that do not cover the vendored tree, or by one for some of its
// files that names MIT too; a repository without REUSE.toml gets no warning.
func TestVendoredLicenseWarningsPositive(t *testing.T) {
	variants := []string{
		reuseWithOverride,
		reuseWithOverride + "\n[[annotations]]\npath = [\"docs/**\", \"tools/figures/dist/**\"]\nSPDX-License-Identifier = \"CC-BY-4.0\"\n",
		strings.Replace(reuseWithOverride, `SPDX-License-Identifier = "MIT"`, `SPDX-License-Identifier = "EUPL-1.2 AND (MIT)"`, 1),
		strings.Replace(reuseWithOverride, `"tools/figures/third_party/interfig/upstream/**"`, `'tools/figures/third_party/interfig/upstream/**'`, 1),
		strings.Replace(reuseWithOverride, `path = ["tools/figures/third_party/interfig/upstream/**"]`,
			"path = [\n  \"tools/figures/dist/**\",\n  \"tools/figures/third_party/interfig/upstream/**\",\n]", 1),
		strings.ReplaceAll(reuseWithOverride, "\n", "\r\n"),
		reuseWithOverride + "\n[[annotations]]\npath = \"**/*.js\"\nSPDX-License-Identifier = \"MIT OR Apache-2.0\"\n",
	}
	for _, text := range variants {
		if warnings := reuseWarnings(t, &text); len(warnings) != 0 {
			t.Fatalf("REUSE.toml %q warned: %v", text, warnings)
		}
	}
	if warnings := reuseWarnings(t, nil); len(warnings) != 0 {
		t.Fatalf("a repository without REUSE.toml warned: %v", warnings)
	}
}

// Negative: the whole-tree table alone, an annotation naming the tree under another license, a
// license in another table than the path, the path only in a comment, and a later MIT table
// whose comment quotes the whole-tree glob each warn, naming the glob, the license and the
// remedy.
func TestVendoredLicenseWarningsNegative(t *testing.T) {
	variants := map[string]string{
		"whole tree only": reuseWithoutOverride,
		"other license":   strings.Replace(reuseWithOverride, `SPDX-License-Identifier = "MIT"`, `SPDX-License-Identifier = "EUPL-1.2"`, 1),
		"license elsewhere": reuseWithoutOverride + "\n[[annotations]]\npath = [\"tools/figures/third_party/interfig/upstream/**\"]\n" +
			"\n[[annotations]]\npath = [\"other/**\"]\nSPDX-License-Identifier = \"MIT\"\n",
		"path in a comment":  reuseWithoutOverride + "# \"tools/figures/third_party/interfig/upstream/**\" is MIT\n",
		"MIT as a substring": strings.Replace(reuseWithOverride, `SPDX-License-Identifier = "MIT"`, `SPDX-License-Identifier = "MIT-0"`, 1),
		// REUSE 3.3 applies only the last matching table, so a later covering table relabels.
		"override before the whole tree":   "version = 1\n" + reuseOverrideTable + strings.TrimPrefix(reuseWithoutOverride, "version = 1\n"),
		"override before tools/figures/**": reuseWithOverride + "\n[[annotations]]\npath = ['tools/figures/**']\nSPDX-License-Identifier = \"Apache-2.0\"\n",
		"override before a star glob":      reuseWithOverride + "\n[[annotations]]\npath = \"tools/*/third_party/**\"\nSPDX-License-Identifier = \"Apache-2.0\"\n",
		"some files relabelled":            reuseWithOverride + "\n[[annotations]]\npath = \"**/*.js\"\nSPDX-License-Identifier = \"Apache-2.0\"\n",
		"comment quoting **": reuseWithoutOverride + "\n# Keep this table after the \"**\" table.\n[[annotations]]\n" +
			"path = [\"other/**\"] # not \"**\"\nSPDX-License-Identifier = \"MIT\"\n",
	}
	for name, text := range variants {
		warnings := reuseWarnings(t, &text)
		if len(warnings) != 1 || !strings.Contains(warnings[0], "labelling tools/figures/third_party/interfig/upstream/** MIT") ||
			!strings.Contains(warnings[0], "tools/figures/README.md") {
			t.Fatalf("%s: warnings = %v", name, warnings)
		}
	}
}

// Boundary: the nearest ancestor glob of the vendored tree covers it and relabels it from a
// later table, while a later table quoting the vendored glob again with MIT is the one applied;
// a REUSE.toml at the line bound is read and one past it warns that it was not; a REUSE.toml
// that is not a regular file warns that it cannot be read; families that vendor nothing read no
// REUSE.toml at all.
func TestVendoredLicenseWarningsBoundary(t *testing.T) {
	nearest := reuseWithOverride + "\n[[annotations]]\npath = [\"tools/figures/third_party/interfig/**\"]\nSPDX-License-Identifier = \"Apache-2.0\"\n"
	if warnings := reuseWarnings(t, &nearest); len(warnings) != 1 {
		t.Fatalf("a later table for the nearest ancestor did not warn: %v", warnings)
	}
	again := nearest + reuseOverrideTable
	if warnings := reuseWarnings(t, &again); len(warnings) != 0 {
		t.Fatalf("the override repeated after the ancestor table warned: %v", warnings)
	}
	atBound := reuseWithOverride + strings.Repeat("\n", supplychain.MaxReuseLines-strings.Count(reuseWithOverride, "\n")-1)
	if warnings := reuseWarnings(t, &atBound); len(warnings) != 0 {
		t.Fatalf("a REUSE.toml at the line bound warned: %v", warnings)
	}
	past := atBound + "\n"
	if warnings := reuseWarnings(t, &past); len(warnings) != 1 || !strings.Contains(warnings[0], "more than the 4096") {
		t.Fatalf("a REUSE.toml past the line bound: %v", warnings)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, supplychain.ReuseFile), 0o755); err != nil {
		t.Fatal(err)
	}
	families := adopt.DocumentationFamilies()
	if warnings := vendoredLicenseWarnings(t.Context(), root, families); len(warnings) != 1 || !strings.Contains(warnings[0], "cannot be read") {
		t.Fatalf("an unreadable REUSE.toml: %v", warnings)
	}
	if warnings := vendoredLicenseWarnings(t.Context(), root, []managedasset.Family{families[0]}); len(warnings) != 0 {
		t.Fatalf("a family that vendors nothing read REUSE.toml: %v", warnings)
	}
}

// reuseOrderToday is the day the annotation order tests judge exceptions entries against.
var reuseOrderToday = time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)

// auditReuseOrder runs the REUSE.toml annotation order gate over a repository whose REUSE.toml
// is text, or that has none for a nil text, under a manifest with entries as its exceptions, and
// returns what it printed and its verdict.
func auditReuseOrder(t *testing.T, text *string, entries ...config.Exception) (string, error) {
	t.Helper()
	root := t.TempDir()
	if text != nil {
		writeFixtureFile(t, root, supplychain.ReuseFile, *text)
	}
	manifest := &config.Manifest{Exceptions: entries}
	return captureStdout(t, func() error { return auditReuseRecords(t.Context(), manifest, root, reuseOrderToday) })
}

// The annotation order gate holds an override to the record REUSE resolves for its files.
// Positive: the default table first and the override after it passes, as praetor's own
// REUSE.toml does. Negative: the default table after the override fails, naming each override
// path and the default. Boundary: no REUSE.toml skips, saying why, and one the read cannot follow
// fails instead of passing unchecked.
func TestAuditReuseRecords_3D(t *testing.T) {
	correct := "version = 1\n\n[[annotations]]\npath = \"**\"\nSPDX-License-Identifier = \"EUPL-1.2\"\n" + reuseOverrideTable
	if output, err := auditReuseOrder(t, &correct); err != nil || !strings.Contains(output, "[PASS] REUSE.toml annotation order") {
		t.Fatalf("default then override: %v\n%s", err, output)
	}
	own, err := os.ReadFile(filepath.Join("..", "..", supplychain.ReuseFile))
	if err != nil {
		t.Fatalf("read praetor's own %s: %v", supplychain.ReuseFile, err)
	}
	ownText := string(own)
	if output, err := auditReuseOrder(t, &ownText); err != nil {
		t.Fatalf("praetor's own %s: %v\n%s", supplychain.ReuseFile, err, output)
	}
	reversed := "version = 1\n" + reuseOverrideTable + "\n[[annotations]]\npath = \"**\"\nSPDX-License-Identifier = \"EUPL-1.2\"\n"
	output, err := auditReuseOrder(t, &reversed)
	if err == nil || !strings.Contains(err.Error(), "[FAIL] REUSE.toml annotation order: 1 path(s)") ||
		!strings.Contains(output, `annotation 1 path "tools/figures/third_party/interfig/upstream/**" never takes effect: annotation 2 path "**"`) {
		t.Fatalf("default after override: %v\n%s", err, output)
	}
	if output, err := auditReuseOrder(t, nil); err != nil || !strings.Contains(output, "[SKIP] REUSE.toml annotation order not checked") {
		t.Fatalf("no REUSE.toml: %v\n%s", err, output)
	}
	unreadable := "version = 1\n[[annotations]]\npath = \"\"\"\n**\n\"\"\"\n"
	if _, err := auditReuseOrder(t, &unreadable); err == nil || !strings.Contains(err.Error(), "not checked") {
		t.Fatalf("a REUSE.toml the read cannot follow passed: %v", err)
	}
	// The reversed order written as an inline array of tables, which REUSE reads, fails closed
	// instead of passing over no annotation.
	inline := "version = 1\nannotations = [\n  { path = \"tools/figures/third_party/interfig/upstream/**\", SPDX-License-Identifier = \"MIT\" },\n" +
		"  { path = \"**\", SPDX-License-Identifier = \"EUPL-1.2\" },\n]\n"
	if output, err := auditReuseOrder(t, &inline); err == nil || !strings.Contains(err.Error(), "[FAIL] REUSE.toml annotation order not checked") ||
		!strings.Contains(err.Error(), "top-level key annotations") || strings.Contains(output, "[PASS]") {
		t.Fatalf("annotations as an inline array of tables: %v\n%s", err, output)
	}
}

// reuseOrderEntry is an exceptions entry of the annotation order rule naming REUSE.toml, expiring
// on expires.
func reuseOrderEntry(expires string) config.Exception {
	return config.Exception{Rule: config.ExceptionRuleReuseAnnotationOrder, Path: supplychain.ReuseFile,
		Reason: "4000 vendored file globs", Expires: expires}
}

// A REUSE.toml whose comparisons pass the step bound is not checked. Negative: without an
// exceptions entry the gate fails, naming the bound and the rule that would excuse it, and an
// expired entry excuses nothing. Positive: a live entry excuses it, printing the entry's reason
// and expiry as not checked, never as a pass. Boundary: an entry for a file the gate checks in
// full is stale and fails the gate.
func TestAuditReuseRecords_Bound(t *testing.T) {
	bound := fmt.Errorf("REUSE.toml annotation 1 path \"**\": %w of 16777216", supplychain.ErrReuseGlobBound)
	err := reuseOrderBound(nil, bound, reuseOrderToday)
	if err == nil || !strings.Contains(err.Error(), "[FAIL] REUSE.toml annotation order not checked") || !strings.Contains(err.Error(), "of 16777216") ||
		!strings.Contains(err.Error(), "exceptions entry of rule reuse-annotation-order naming REUSE.toml") {
		t.Fatalf("no entry: %v", err)
	}
	if err := reuseOrderBound([]config.Exception{reuseOrderEntry("2026-10-06")}, bound, reuseOrderToday); err == nil ||
		!strings.Contains(err.Error(), "its reuse-annotation-order exception expired on 2026-10-06") {
		t.Fatalf("expired entry: %v", err)
	}
	output, err := captureStdout(t, func() error {
		return reuseOrderBound([]config.Exception{reuseOrderEntry("2026-12-31")}, bound, reuseOrderToday)
	})
	if err != nil || !strings.Contains(output, "[SKIP] REUSE.toml annotation order not checked") ||
		!strings.Contains(output, "until 2026-12-31: 4000 vendored file globs") || strings.Contains(output, "[PASS]") {
		t.Fatalf("live entry: %v\n%s", err, output)
	}
	correct := "version = 1\n\n[[annotations]]\npath = \"**\"\nSPDX-License-Identifier = \"EUPL-1.2\"\n" + reuseOverrideTable
	output, err = auditReuseOrder(t, &correct, reuseOrderEntry("2026-12-31"))
	if err == nil || !strings.Contains(err.Error(), "reuse-annotation-order exceptions entries excuse nothing") ||
		!strings.Contains(output, "exceptions entry REUSE.toml (reuse-annotation-order): the annotation order was checked in full") {
		t.Fatalf("stale entry: %v\n%s", err, output)
	}
}

// auditLicensing runs both licensing gates and prints both verdicts. Positive: a root whose
// LICENSE holds the one LICENSES text passes, and an upstream COPYING the manifest keeps is named;
// so does one whose REUSE.toml spells the whole-tree default as a union of globs and writes its
// copyright as an array of escaped strings and as a multi-line string, which neither gate reads.
// Negative: the same COPYING without the exception fails the root licence gate while the
// annotation order gate still prints its verdict. Boundary: a root with neither LICENSES/ nor
// REUSE.toml skips both, saying why.
func TestAuditLicensing_3D(t *testing.T) {
	today := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	run := func(manifest *config.Manifest, root string) (string, error) {
		return captureStdout(t, func() error { return auditLicensing(t.Context(), manifest, root, today) })
	}
	root := t.TempDir()
	writeFixtureFile(t, root, "LICENSES/MIT.txt", "MIT License\n")
	writeFixtureFile(t, root, supplychain.RootLicenseFile, "MIT License\n")
	writeFixtureFile(t, root, "COPYING", "upstream notice\n")
	kept := &config.Manifest{Exceptions: []config.Exception{{Rule: config.ExceptionRuleRootLicenseNotice, Path: "COPYING",
		Reason: "upstream notice kept verbatim", Expires: "2026-12-31"}}}
	output, err := run(kept, root)
	if err != nil || !strings.Contains(output, "[PASS] root licence: LICENSE holds the text of LICENSES/MIT.txt; kept upstream notices COPYING.") ||
		!strings.Contains(output, "[SKIP] REUSE.toml annotation order not checked") {
		t.Fatalf("kept COPYING: %v\n%s", err, output)
	}
	writeFixtureFile(t, root, supplychain.ReuseFile, "version = 1\n\n[[annotations]]\npath = [\"*\", \".*\", \"*/**\", \".*/**\"]\n"+
		"SPDX-FileCopyrightText = [\"2024 A \\\"B\\\" C\", \"x\"]\nSPDX-FileCopyrightText = \"\"\"\n2024 A\n2025 B\n\"\"\"\n"+
		"SPDX-License-Identifier = \"MIT\"\n")
	output, err = run(kept, root)
	if err != nil || !strings.Contains(output, "[PASS] REUSE.toml annotation order") ||
		!strings.Contains(output, "[PASS] root licence: LICENSE holds the text of LICENSES/MIT.txt") {
		t.Fatalf("a union default with escaped and multi-line copyright values: %v\n%s", err, output)
	}
	output, err = run(&config.Manifest{}, root)
	if err == nil || !strings.Contains(err.Error(), "[FAIL] root licence: 1 problem(s)") ||
		!strings.Contains(output, "COPYING: a second root licence file beside LICENSE") || !strings.Contains(output, "REUSE.toml annotation order") {
		t.Fatalf("unkept COPYING: %v\n%s", err, output)
	}
	output, err = run(&config.Manifest{}, t.TempDir())
	if err != nil || !strings.Contains(output, "[SKIP] root licence not checked: the repository keeps no LICENSES/ directory.") {
		t.Fatalf("no licensing: %v\n%s", err, output)
	}
}
