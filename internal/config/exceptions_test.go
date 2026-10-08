// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// exceptionsToday is the fixed day the validator tests judge expiry against.
var exceptionsToday = time.Date(2026, time.October, 6, 15, 30, 0, 0, time.UTC)

func validException() Exception {
	return Exception{
		Rule: ExceptionRuleClangTidyCoverage, Path: "src/win32/getopt.c",
		Reason: "Windows-only translation unit; no lane configures a Windows build yet", Expires: "2026-12-31",
	}
}

// Positive: a path entry and a glob entry decode from the manifest as written, unquoted and
// quoted dates alike, survive RenderManifest, and ExceptionsFor selects by rule.
func TestLoadManifestExceptionsPositive(t *testing.T) {
	expires := ExceptionDay(time.Now()).AddDate(0, 0, 30).Format(ExceptionDateLayout)
	m, err := LoadManifest(writeManifest(t, fmt.Sprintf(`version: 1
exceptions:
  - rule: "clang-tidy-coverage"
    path: "src/win32/getopt.c"
    reason: "Windows-only translation unit"
    expires: %s
  - rule: "clang-tidy-coverage"
    glob: "tests/fixtures/**/*.c"
    reason: "rule-engine fixtures that are never built"
    expires: "%s"
clang_tidy:
  lanes:
    - name: "cpu"
      compile_database: "build/compile_commands.json"
    - name: "metal"
      files: ".config/clang-tidy/metal-files.txt"
`, expires, expires)))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Exceptions) != 2 || m.Exceptions[0].Expires != expires || m.Exceptions[1].Glob != "tests/fixtures/**/*.c" {
		t.Fatalf("exceptions = %+v", m.Exceptions)
	}
	if m.ClangTidy == nil || len(m.ClangTidy.Lanes) != 2 || m.ClangTidy.Lanes[1].Source() != ".config/clang-tidy/metal-files.txt" {
		t.Fatalf("clang_tidy = %+v", m.ClangTidy)
	}
	if got := ExceptionsFor(m.Exceptions, ExceptionRuleClangTidyCoverage); len(got) != 2 {
		t.Fatalf("ExceptionsFor selected %d entries, want 2", len(got))
	}
	credits := validException()
	credits.Rule = ExceptionRuleCredits
	if err := ValidateExceptions([]Exception{validException(), credits}, exceptionsToday); err != nil {
		t.Fatalf("an entry of the credits gate's rule was refused: %v", err)
	}
	if got := ExceptionsFor([]Exception{validException(), credits}, ExceptionRuleCredits); len(got) != 1 || got[0].Rule != ExceptionRuleCredits {
		t.Fatalf("ExceptionsFor(credits) = %+v", got)
	}
	if got := ExceptionsFor(m.Exceptions, "other-rule"); len(got) != 0 {
		t.Fatalf("ExceptionsFor selected %+v for a rule no entry names", got)
	}
	rendered, err := RenderManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadManifest(writeManifest(t, string(rendered)))
	if err != nil || len(again.Exceptions) != 2 || again.ClangTidy == nil || again.Exceptions[0].Expires != expires {
		t.Fatalf("exceptions or clang_tidy lost in the render round trip: %+v, %v\n%s", again, err, rendered)
	}
}

// Negative: every malformed entry is refused with its position named, by the validator and by
// the manifest loader, and an unknown key fails the strict decode.
func TestValidateExceptionsNegative(t *testing.T) {
	cases := map[string]struct {
		edit func(*Exception)
		want string
	}{
		"unknown rule":        {func(e *Exception) { e.Rule = "clang-tidy-coverag" }, `rule "clang-tidy-coverag" is not a rule`},
		"neither target":      {func(e *Exception) { e.Path = "" }, "exactly one of path and glob"},
		"both targets":        {func(e *Exception) { e.Glob = "src/**/*.c" }, "exactly one of path and glob"},
		"absolute path":       {func(e *Exception) { e.Path = "/src/a.c" }, "clean repository-relative file path"},
		"parent path":         {func(e *Exception) { e.Path = "../a.c" }, "clean repository-relative file path"},
		"pattern as path":     {func(e *Exception) { e.Path = "src/*.c" }, "without glob characters"},
		"unclean path":        {func(e *Exception) { e.Path = "src//a.c" }, "clean repository-relative file path"},
		"wildcard-only glob":  {func(e *Exception) { e.Path, e.Glob = "", "**/*" }, "must name a path"},
		"negated glob":        {func(e *Exception) { e.Path, e.Glob = "", "!src/*.c" }, "repository-relative"},
		"empty reason":        {func(e *Exception) { e.Reason = "  " }, "reason must say why"},
		"multi-line reason":   {func(e *Exception) { e.Reason = "one\ntwo" }, "reason must be one line"},
		"oversized reason":    {func(e *Exception) { e.Reason = strings.Repeat("r", MaxExceptionReasonBytes+1) }, "exceeds 1024 bytes"},
		"no date":             {func(e *Exception) { e.Expires = "next year" }, "must be a YYYY-MM-DD date"},
		"unpadded date":       {func(e *Exception) { e.Expires = "2026-12-1" }, "must be a YYYY-MM-DD date"},
		"too far ahead":       {func(e *Exception) { e.Expires = "2027-01-05" }, "more than 90 days after today; the latest date allowed is 2027-01-04"},
		"missing expiry date": {func(e *Exception) { e.Expires = "" }, "must be a YYYY-MM-DD date"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			entry := validException()
			tc.edit(&entry)
			err := ValidateExceptions([]Exception{validException(), entry}, exceptionsToday)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateExceptions = %v, want an error containing %q", err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), "exceptions[1] ") {
				t.Fatalf("error %q does not name the entry's position", err)
			}
		})
	}
	repeated := ValidateExceptions([]Exception{validException(), validException()}, exceptionsToday)
	if repeated == nil || !strings.Contains(repeated.Error(), "exceptions[1] repeats exceptions[0]") {
		t.Fatalf("a repeated entry must be refused, got %v", repeated)
	}
	for _, manifest := range []string{
		"version: 1\nexceptions:\n  - rule: \"clang-tidy-coverage\"\n    path: \"a.c\"\n    reason: \"r\"\n    expires: \"2001-01-01\"\n    owner: \"x\"\n",
		"version: 1\nexceptions:\n  - rule: \"clang-tidy-coverage\"\n    path: \"a.c\"\n    reason: \"r\"\n    expires: \"9999-12-31\"\n",
	} {
		if _, err := LoadManifest(writeManifest(t, manifest)); err == nil {
			t.Fatalf("LoadManifest accepted a malformed exceptions list:\n%s", manifest)
		}
	}
}

// Boundary: an expiry exactly MaxExceptionDays ahead, today, and in the past are all valid (an
// expired entry is the gate's finding, not a load error); the day boundary follows the caller's
// calendar day; a list of exactly MaxExceptions entries passes and one more is refused.
func TestValidateExceptionsBoundary(t *testing.T) {
	for _, expires := range []string{"2027-01-04", "2026-10-06", "2020-01-01"} {
		entry := validException()
		entry.Expires = expires
		if err := ValidateExceptions([]Exception{entry}, exceptionsToday); err != nil {
			t.Fatalf("expires %s refused: %v", expires, err)
		}
	}
	entry := validException()
	entry.Expires = "2026-10-06"
	if entry.Expired(exceptionsToday) || entry.Expired(time.Date(2026, time.October, 6, 23, 59, 59, 0, time.UTC)) {
		t.Fatal("an entry must still hold on its expires day")
	}
	if !entry.Expired(time.Date(2026, time.October, 7, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("an entry must stop holding the day after its expires day")
	}
	if broken := (Exception{Expires: "soon"}); !broken.Expired(exceptionsToday) {
		t.Fatal("an expiry that is no date must count as expired")
	}
	entries := make([]Exception, MaxExceptions+1)
	for index := range entries {
		entries[index] = validException()
		entries[index].Path = fmt.Sprintf("src/unit_%d.c", index)
	}
	if err := ValidateExceptions(entries[:MaxExceptions], exceptionsToday); err != nil {
		t.Fatalf("%d entries refused: %v", MaxExceptions, err)
	}
	if err := ValidateExceptions(entries, exceptionsToday); err == nil || !strings.Contains(err.Error(), "maximum is 1024") {
		t.Fatalf("%d entries accepted: %v", len(entries), err)
	}
}

// A HISS-11 entry names the release workflow the supply-chain gate measured (#330), and a
// HISS-10 entry the workflow holding the build lanes it excuses (#816), and a HISS-18 entry the
// workflow the trigger check reported (#817). Positive: a .yml or
// .yaml file directly in .github/workflows is accepted and selected by its rule. Negative: a
// glob, a file outside .github/workflows or below it, and a file that is no YAML document are
// refused with the rule named. Boundary: the target check binds only those rules, so a
// clang-tidy-coverage entry for a C file stays valid beside them.
func TestValidateExceptionsWorkflowTarget(t *testing.T) {
	for _, rule := range []string{ExceptionRuleSupplyChain, ExceptionRuleBuildWarnings, ExceptionRuleWorkflowTriggers} {
		t.Run(rule, func(t *testing.T) { validateWorkflowTarget(t, rule) })
	}
}

// validateWorkflowTarget runs the workflow-target cases for one rule.
func validateWorkflowTarget(t *testing.T, rule string) {
	entry := func(target string) Exception {
		return Exception{Rule: rule, Path: target, Reason: "the workflow falls short of the rule", Expires: "2026-12-31"}
	}
	for _, target := range []string{".github/workflows/release.yml", ".github/workflows/release-binaries.yaml"} {
		if err := ValidateExceptions([]Exception{validException(), entry(target)}, exceptionsToday); err != nil {
			t.Fatalf("%s entry for %s refused: %v", rule, target, err)
		}
	}
	if got := ExceptionsFor([]Exception{validException(), entry(".github/workflows/release.yml")}, rule); len(got) != 1 ||
		got[0].Path != ".github/workflows/release.yml" {
		t.Fatalf("ExceptionsFor(%s) = %+v", rule, got)
	}
	globbed := entry("")
	globbed.Glob = ".github/workflows/*.yml"
	for name, refused := range map[string]Exception{
		"glob":                  globbed,
		"outside the workflows": entry("release.yml"),
		"below the workflows":   entry(".github/workflows/nested/release.yml"),
		"not a YAML document":   entry(".github/workflows/release.sh"),
		"another directory":     entry(".github/actions/release.yml"),
	} {
		err := ValidateExceptions([]Exception{refused}, exceptionsToday)
		want := "rule " + rule + " must name one workflow file directly in .github/workflows by path, such as .github/workflows/" +
			workflowRuleExamples[rule]
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: ValidateExceptions = %v; want the %s target refused", name, err, rule)
		}
	}
}

// ExceptionFor. Positive: a live entry naming the path excuses it, the last of several live ones
// winning, and marks every entry naming it, expired ones included. Negative: an expired entry
// alone is returned as expired and excuses nothing; an entry naming another path is neither
// returned nor marked. Boundary: an entry holds on its expires day and has expired the day after,
// and a used slice shorter than the entries marks only what it holds.
func TestExceptionFor(t *testing.T) {
	const ci = ".github/workflows/ci.yml"
	entry := func(path, expires string) Exception {
		return Exception{Rule: ExceptionRuleWorkflowTriggers, Path: path, Reason: "runs everywhere", Expires: expires}
	}
	entries := []Exception{
		entry(ci, "2026-10-05"), entry(".github/workflows/other.yml", "2026-12-31"),
		entry(ci, "2026-10-06"), entry(ci, "2026-12-31"),
	}
	used := make([]bool, len(entries))
	live, expired := ExceptionFor(entries, ci, exceptionsToday, used)
	if live != &entries[3] || expired != &entries[0] || !slices.Equal(used, []bool{true, false, true, true}) {
		t.Fatalf("ExceptionFor(ci) = %+v, %+v, used %v", live, expired, used)
	}
	live, expired = ExceptionFor(entries[:1], ci, exceptionsToday, nil)
	if live != nil || expired != &entries[0] {
		t.Fatalf("an expired entry alone: live %+v, expired %+v", live, expired)
	}
	live, expired = ExceptionFor(entries[2:3], ci, exceptionsToday, nil)
	if live != &entries[2] || expired != nil {
		t.Fatalf("an entry on its expires day: live %+v, expired %+v", live, expired)
	}
	short := make([]bool, 1)
	if live, _ := ExceptionFor(entries, ".github/workflows/absent.yml", exceptionsToday, short); live != nil || short[0] {
		t.Fatalf("an unnamed path: live %+v, used %v", live, short)
	}
	if live, _ := ExceptionFor(entries, ci, exceptionsToday, short); live == nil || !short[0] {
		t.Fatalf("a short used slice: live %+v, used %v", live, short)
	}
}

// Boundary: Matches compares a path entry exactly and a glob entry segment by segment, where
// "*" stays inside one segment and "**" spans any number, zero included.
func TestExceptionMatchesBoundary(t *testing.T) {
	path := Exception{Path: "src/a.c"}
	glob := Exception{Glob: "tests/**/*.c"}
	cases := []struct {
		entry Exception
		rel   string
		want  bool
	}{
		{path, "src/a.c", true},
		{path, "src/a.cc", false},
		{path, "other/src/a.c", false},
		{glob, "tests/a.c", true},
		{glob, "tests/x/y/a.c", true},
		{glob, "tests/x/a.cpp", false},
		{glob, "src/tests/a.c", false},
		{Exception{}, "a.c", false},
	}
	for _, tc := range cases {
		if got := tc.entry.Matches(tc.rel); got != tc.want {
			t.Errorf("%+v.Matches(%q) = %v, want %v", tc.entry, tc.rel, got, tc.want)
		}
	}
	deep := strings.Repeat("d/", maxExceptionPathSegments) + "a.c"
	if (Exception{Glob: "**/a.c"}).Matches(deep) {
		t.Fatal("a path deeper than the segment bound must not match")
	}
	if target := glob.Target(); target != "tests/**/*.c" {
		t.Fatalf("Target() = %q", target)
	}
}

// Negative and boundary: the clang_tidy section needs at least one lane, each with a name and
// exactly one clean source path, distinct names, and at most MaxClangTidyLanes lanes.
func TestValidateClangTidyNegativeAndBoundary(t *testing.T) {
	lane := ClangTidyLane{Name: "cpu", CompileDatabase: "build/compile_commands.json"}
	cases := map[string]struct {
		policy *ClangTidyPolicy
		want   string
	}{
		"no lanes":      {&ClangTidyPolicy{}, "at least one lane"},
		"no name":       {&ClangTidyPolicy{Lanes: []ClangTidyLane{{CompileDatabase: "b/c.json"}}}, "clang_tidy.lanes[0].name must be a non-empty string"},
		"no source":     {&ClangTidyPolicy{Lanes: []ClangTidyLane{{Name: "cpu"}}}, "exactly one of compile_database and files"},
		"both sources":  {&ClangTidyPolicy{Lanes: []ClangTidyLane{{Name: "cpu", CompileDatabase: "a.json", Files: "b.txt"}}}, "exactly one of"},
		"absolute path": {&ClangTidyPolicy{Lanes: []ClangTidyLane{{Name: "cpu", Files: "/tmp/files.txt"}}}, "clean repository-relative file path"},
		"parent path":   {&ClangTidyPolicy{Lanes: []ClangTidyLane{{Name: "cpu", CompileDatabase: "../build/compile_commands.json"}}}, "clean repository-relative"},
		"repeated name": {&ClangTidyPolicy{Lanes: []ClangTidyLane{lane, lane}}, "clang_tidy.lanes[1] repeats the name \"cpu\""},
	}
	for name, tc := range cases {
		if err := ValidateClangTidy(tc.policy); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: ValidateClangTidy = %v, want an error containing %q", name, err, tc.want)
		}
	}
	if err := ValidateClangTidy(nil); err != nil {
		t.Fatalf("an absent section must be valid: %v", err)
	}
	lanes := make([]ClangTidyLane, MaxClangTidyLanes+1)
	for index := range lanes {
		lanes[index] = ClangTidyLane{Name: fmt.Sprintf("lane-%d", index), Files: fmt.Sprintf("lanes/%d.txt", index)}
	}
	if err := ValidateClangTidy(&ClangTidyPolicy{Lanes: lanes[:MaxClangTidyLanes]}); err != nil {
		t.Fatalf("%d lanes refused: %v", MaxClangTidyLanes, err)
	}
	if err := ValidateClangTidy(&ClangTidyPolicy{Lanes: lanes}); err == nil || !strings.Contains(err.Error(), "maximum is 64") {
		t.Fatalf("%d lanes accepted: %v", len(lanes), err)
	}
	if _, err := LoadManifest(writeManifest(t, "version: 1\nclang_tidy:\n  lanes: []\n")); err == nil {
		t.Fatal("LoadManifest accepted a clang_tidy section without lanes")
	}
	if _, err := LoadManifest(writeManifest(t, "version: 1\nclang_tidy:\n  lanes:\n    - name: \"cpu\"\n      database: \"b.json\"\n")); err == nil {
		t.Fatal("LoadManifest accepted an unknown lane key")
	}
}

// The root-license-notice and reuse-annotation-order rules name one root file per entry.
// Positive: an entry naming a root file by path validates and decodes from the manifest.
// Negative: a glob, or a path below the root, is refused, naming the rule. Boundary: the same
// root file under the other rule is another entry, not a repeat.
func TestValidateExceptionsRootLicenseNotice(t *testing.T) {
	notice := Exception{Rule: ExceptionRuleRootLicenseNotice, Path: "COPYING", Reason: "upstream GPL notice kept verbatim", Expires: "2026-12-31"}
	if err := ValidateExceptions([]Exception{notice}, exceptionsToday); err != nil {
		t.Fatalf("a root file entry refused: %v", err)
	}
	order := Exception{Rule: ExceptionRuleReuseAnnotationOrder, Path: "REUSE.toml", Reason: "4000 vendored globs", Expires: "2026-12-31"}
	if err := ValidateExceptions([]Exception{order}, exceptionsToday); err != nil {
		t.Fatalf("a REUSE.toml order entry refused: %v", err)
	}
	order.Path = "sub/REUSE.toml"
	if err := ValidateExceptions([]Exception{order}, exceptionsToday); err == nil ||
		!strings.Contains(err.Error(), "rule reuse-annotation-order must name one file at the repository root by path") {
		t.Errorf("a nested REUSE.toml order entry: ValidateExceptions = %v", err)
	}
	expires := ExceptionDay(time.Now()).AddDate(0, 0, 30).Format(ExceptionDateLayout)
	m, err := LoadManifest(writeManifest(t, "version: 1\nexceptions:\n  - rule: \"root-license-notice\"\n    path: \"COPYING\"\n"+
		"    reason: \"upstream notice\"\n    expires: \""+expires+"\"\n"))
	if err != nil || len(ExceptionsFor(m.Exceptions, ExceptionRuleRootLicenseNotice)) != 1 {
		t.Fatalf("LoadManifest: %v, %+v", err, m)
	}
	for name, edit := range map[string]func(*Exception){
		"glob":        func(e *Exception) { e.Path, e.Glob = "", "LICENSE-*" },
		"nested path": func(e *Exception) { e.Path = "vendor/COPYING" },
	} {
		entry := notice
		edit(&entry)
		if err := ValidateExceptions([]Exception{entry}, exceptionsToday); err == nil || !strings.Contains(err.Error(), "one file at the repository root by path") {
			t.Errorf("%s: ValidateExceptions = %v", name, err)
		}
	}
	other := notice
	other.Rule = ExceptionRuleClangTidyCoverage
	if err := ValidateExceptions([]Exception{notice, other}, exceptionsToday); err != nil {
		t.Fatalf("one file under two rules refused: %v", err)
	}
}

// A HISS-19 entry names one repository file by path (dedupe clone exceptions, #891).
// Positive: a regular repository file path validates, round-trips through LoadManifest,
// and is selected by ExceptionsFor. Negative: a glob target is refused.
func TestValidateExceptionsDedupeTarget(t *testing.T) {
	entry := Exception{
		Rule:    ExceptionRuleDedupe,
		Path:    "api/v1/zz_generated.deepcopy.go",
		Reason:  "controller-gen repeated DeepCopyInto",
		Expires: "2026-12-31",
	}
	if err := ValidateExceptions([]Exception{entry}, exceptionsToday); err != nil {
		t.Fatalf("a valid HISS-19 entry was refused: %v", err)
	}
	if got := ExceptionsFor([]Exception{validException(), entry}, ExceptionRuleDedupe); len(got) != 1 || got[0].Path != entry.Path {
		t.Fatalf("ExceptionsFor(HISS-19) = %+v", got)
	}

	globbed := entry
	globbed.Path, globbed.Glob = "", "api/**/*.go"
	if err := ValidateExceptions([]Exception{globbed}, exceptionsToday); err == nil ||
		!strings.Contains(err.Error(), "rule HISS-19 must name one repository file by path") {
		t.Errorf("glob target accepted for HISS-19: %v", err)
	}

	expires := ExceptionDay(time.Now()).AddDate(0, 0, 30).Format(ExceptionDateLayout)
	manifest := fmt.Sprintf("version: 1\nexceptions:\n  - rule: %q\n    path: %q\n    reason: %q\n    expires: %q\n",
		ExceptionRuleDedupe, "pkg/deepcopy.go", "generated clones", expires)
	m, err := LoadManifest(writeManifest(t, manifest))
	if err != nil || len(ExceptionsFor(m.Exceptions, ExceptionRuleDedupe)) != 1 {
		t.Fatalf("LoadManifest: %v, %+v", err, m)
	}
}

func TestLoadExceptionsFor_Positive(t *testing.T) {
	root := t.TempDir()
	expires := ExceptionDay(time.Now()).AddDate(0, 0, 30).Format(ExceptionDateLayout)
	content := fmt.Sprintf("version: 1\nexceptions:\n  - rule: %q\n    path: %q\n    reason: %q\n    expires: %q\n",
		ExceptionRuleDedupe, "pkg/deepcopy.go", "generated clones", expires)
	if err := os.WriteFile(filepath.Join(root, ManifestFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := LoadExceptionsFor(t.Context(), root, ExceptionRuleDedupe)
	if err != nil {
		t.Fatalf("LoadExceptionsFor failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != "pkg/deepcopy.go" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestLoadExceptionsFor_Boundary_MissingManifestReturnsNil(t *testing.T) {
	root := t.TempDir()
	entries, err := LoadExceptionsFor(t.Context(), root, ExceptionRuleDedupe)
	if err != nil {
		t.Fatalf("LoadExceptionsFor missing manifest: %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil entries, got %+v", entries)
	}
}

func TestLoadExceptionsFor_Negative_MalformedManifestFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ManifestFileName), []byte("version: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadExceptionsFor(t.Context(), root, ExceptionRuleDedupe)
	if err == nil {
		t.Fatal("expected error on malformed manifest")
	}
}

func TestLoadExceptionsFor_Negative_CanceledContextFails(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := LoadExceptionsFor(ctx, ".", ExceptionRuleDedupe)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadExceptionsFor with canceled context: %v, want context.Canceled", err)
	}
}
