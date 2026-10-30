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
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"go.yaml.in/yaml/v3"
)

// exceptionsToday is the fixed day the validator tests judge expiry against.
var exceptionsToday = time.Date(2026, time.October, 6, 15, 30, 0, 0, time.UTC)

func validException() Exception {
	return Exception{
		Rule: ExceptionRuleClangTidyCoverage, Path: "src/win32/getopt.c",
		Reason: "Windows-only translation unit; no lane configures a Windows build yet", Expires: "2026-11-28",
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
    category: A
    expires: %s
  - rule: "clang-tidy-coverage"
    glob: "tests/fixtures/**/*.c"
    reason: "rule-engine fixtures that are never built"
    category: A
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
	lintersEx := validException()
	lintersEx.Rule = ExceptionRuleDeclaredLinters
	lintersEx.Path = ""
	lintersEx.Tool = "gitleaks"
	if err := ValidateExceptions([]Exception{validException(), credits, lintersEx}, exceptionsToday); err != nil {
		t.Fatalf("an entry of the credits or declared-linters gate's rule was refused: %v", err)
	}
	if got := ExceptionsFor([]Exception{validException(), credits, lintersEx}, ExceptionRuleDeclaredLinters); len(got) != 1 || got[0].Tool != "gitleaks" {
		t.Fatalf("ExceptionsFor(declared-linters) = %+v", got)
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
		"unknown rule":                  {func(e *Exception) { e.Rule = "clang-tidy-coverag" }, `rule "clang-tidy-coverag" is not a rule`},
		"neither target":                {func(e *Exception) { e.Path = "" }, "exactly one of path and glob"},
		"both targets":                  {func(e *Exception) { e.Glob = "src/**/*.c" }, "exactly one of path and glob"},
		"absolute path":                 {func(e *Exception) { e.Path = "/src/a.c" }, "clean repository-relative file path"},
		"parent path":                   {func(e *Exception) { e.Path = "../a.c" }, "clean repository-relative file path"},
		"pattern as path":               {func(e *Exception) { e.Path = "src/*.c" }, "without glob characters"},
		"unclean path":                  {func(e *Exception) { e.Path = "src//a.c" }, "clean repository-relative file path"},
		"wildcard-only glob":            {func(e *Exception) { e.Path, e.Glob = "", "**/*" }, "must name a path"},
		"negated glob":                  {func(e *Exception) { e.Path, e.Glob = "", "!src/*.c" }, "repository-relative"},
		"empty reason":                  {func(e *Exception) { e.Reason = "  " }, "reason must say why"},
		"multi-line reason":             {func(e *Exception) { e.Reason = "one\ntwo" }, "reason must be one line"},
		"oversized reason":              {func(e *Exception) { e.Reason = strings.Repeat("r", MaxExceptionReasonBytes+1) }, "exceeds 1024 bytes"},
		"no date":                       {func(e *Exception) { e.Expires = "next year" }, "must be a YYYY-MM-DD date"},
		"unpadded date":                 {func(e *Exception) { e.Expires = "2026-12-1" }, "must be a YYYY-MM-DD date"},
		"too far ahead":                 {func(e *Exception) { e.Category, e.Entered, e.Expires = "A", "2026-10-06", "2027-01-05" }, "category A requires expires on or before 2027-01-04 (90 days after entered 2026-10-06); got 2027-01-05"},
		"missing expiry date":           {func(e *Exception) { e.Expires = "" }, "must be a YYYY-MM-DD date"},
		"declared-linters without tool": {func(e *Exception) { e.Rule, e.Path, e.Tool = ExceptionRuleDeclaredLinters, "", "" }, "rule declared-linters must name one tool"},
		"declared-linters with path":    {func(e *Exception) { e.Rule, e.Path, e.Tool = ExceptionRuleDeclaredLinters, "a.go", "gitleaks" }, "rule declared-linters must not set path or glob"},
		"other rule with tool": {func(e *Exception) {
			e.Rule, e.Path, e.Tool = ExceptionRuleBuildWarnings, ".github/workflows/ci.yml", "gitleaks"
		}, "rule HISS-10 must not set tool"},
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
		"version: 1\nexceptions:\n  - rule: \"clang-tidy-coverage\"\n    path: \"a.c\"\n    reason: \"r\"\n    category: A\n    expires: \"9999-12-31\"\n",
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
		entry.Category = "A"
		entry.Entered = "2026-10-06"
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

func TestValidateExceptionsCategories(t *testing.T) {
	base := Exception{Rule: ExceptionRuleMutants, Path: ".cargo/mutants.toml", Reason: "unsupported generated code", Category: "D", Entered: "2026-10-16", Expires: "2027-02-13"}
	if err := ValidateExceptions([]Exception{base}, time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("category D exception refused: %v", err)
	}
	if err := ValidateExceptions([]Exception{base}, time.Date(2027, 2, 14, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("an expired declaration must remain valid for its owning gate to fail: %v", err)
	}
	base.Category, base.Expires = "C", "2026-10-27"
	if err := ValidateExceptions([]Exception{base}, exceptionsToday); err == nil || !strings.Contains(err.Error(), "category C requires expires on or before 2026-10-26") {
		t.Fatalf("category C with a mismatched expiry = %v", err)
	}
	base = validException()
	if err := ValidateExceptions([]Exception{base}, exceptionsToday); err != nil {
		t.Fatalf("uncategorized exception changed behavior: %v", err)
	}
	base.Category, base.Entered, base.Expires = "A", "2026-10-06", "2027-01-05"
	if err := ValidateExceptions([]Exception{base}, exceptionsToday); err == nil || !strings.Contains(err.Error(), "category A requires expires on or before 2027-01-04 (90 days") {
		t.Fatalf("category A escaped the 90-day cap: %v", err)
	}
	base.Expires = "2027-01-04"
	if err := ValidateExceptions([]Exception{base}, exceptionsToday); err != nil {
		t.Fatalf("category A stated expiry inside 90 days refused: %v", err)
	}
	mutantsUncat := Exception{Rule: ExceptionRuleMutants, Path: ".cargo/mutants.toml", Reason: "unsupported generated code", Entered: "2026-10-29", Expires: "2027-01-04"}
	if err := ValidateExceptions([]Exception{mutantsUncat}, exceptionsToday); err != nil {
		t.Fatalf("uncategorized mutants exception refused: %v", err)
	}
}

func TestException_Problem(t *testing.T) {
	today := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	base := Exception{Rule: ExceptionRuleDeclaredLinters, Tool: "exclusion", Reason: "#133", Category: "C", Entered: "2026-10-10", Expires: "2026-10-20"}
	if prob := base.Problem(today); prob != "" {
		t.Fatalf("expected valid category C to pass, got: %s", prob)
	}
	base.Expires = "2026-10-25"
	if prob := base.Problem(today); !strings.Contains(prob, "category C requires expires on or before 2026-10-20") {
		t.Fatalf("expected category C interval failure, got: %s", prob)
	}
}

func TestValidateExceptions_HISSRulesAndFunctionScope(t *testing.T) {
	for _, rule := range hisscatalog.RuleIDs() {
		entry := Exception{Rule: rule, Path: "src/a.go", Reason: "by design", Expires: "2026-11-28"}
		switch rule {
		case ExceptionRuleBuildWarnings:
			entry.Path = ".github/workflows/ci.yml"
		case ExceptionRuleSupplyChain, ExceptionRuleWorkflowTriggers:
			entry.Path = ".github/workflows/release.yml"
		case ExceptionRuleDedupe:
			entry.Path = "src/a.go"
		case ExceptionRuleSecrets:
			entry.Path = "src/a.go"
			entry.Tool = "generic-api-key"
		}
		if err := ValidateExceptions([]Exception{entry}, exceptionsToday); err != nil {
			t.Errorf("registered HISS rule %s refused: %v", rule, err)
		}
	}
	entry := Exception{Rule: "HISS-02", Path: "src/a.go", Function: "NewBrowser", Reason: "browser lifetime", Expires: "2026-11-28"}
	if err := ValidateExceptions([]Exception{entry}, exceptionsToday); err != nil {
		t.Fatalf("function scoped HISS entry refused: %v", err)
	}
	entry.Rule = ExceptionRuleCredits
	if err := ValidateExceptions([]Exception{entry}, exceptionsToday); err == nil || !strings.Contains(err.Error(), "function is only valid for a HISS scanner rule") {
		t.Fatalf("non-HISS function scope error = %v", err)
	}
	entry = Exception{Rule: "HISS-02", Glob: "src/*.go", Function: "NewBrowser", Reason: "browser lifetime", Expires: "2026-11-28"}
	if err := ValidateExceptions([]Exception{entry}, exceptionsToday); err == nil || !strings.Contains(err.Error(), "one file by path") {
		t.Fatalf("globbed HISS exception error = %v", err)
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
	for _, rule := range []string{ExceptionRuleBuildWarnings, ExceptionRuleWorkflowTriggers, ExceptionRuleTestTimeout, ExceptionRuleJobTimeout, ExceptionRuleReleaseToken} {
		t.Run(rule, func(t *testing.T) { validateWorkflowTarget(t, rule) })
	}
}

// A release-token entry must name one workflow file: an entry naming a script is refused with the
// workflow-target message, which names release.yml as the example.
func TestValidateExceptionsReleaseTokenNamesAWorkflow(t *testing.T) {
	entry := Exception{Rule: ExceptionRuleReleaseToken, Path: "scripts/release.sh", Reason: "custom release", Expires: "2026-11-28"}
	err := ValidateExceptions([]Exception{entry}, exceptionsToday)
	want := "rule release-token must name one workflow file directly in .github/workflows by path, such as .github/workflows/release.yml"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("ValidateExceptions = %v; want %q", err, want)
	}
}

// A test-timeout entry must name one workflow file: an entry naming a script is refused with the
// workflow-target message, which names portability.yml as the example (#1381).
func TestValidateExceptionsTestTimeoutNamesAWorkflow(t *testing.T) {
	entry := Exception{Rule: ExceptionRuleTestTimeout, Path: "scripts/test.sh", Reason: "longer timeout", Expires: "2026-11-28"}
	err := ValidateExceptions([]Exception{entry}, exceptionsToday)
	want := "rule test-timeout must name one workflow file directly in .github/workflows by path, such as .github/workflows/portability.yml"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("ValidateExceptions = %v; want %q", err, want)
	}
}

// A job-timeout entry must name one workflow file: an entry naming a script is refused with the
// workflow-target message, which names ci.yml as the example.
func TestValidateExceptionsJobTimeoutNamesAWorkflow(t *testing.T) {
	entry := Exception{Rule: ExceptionRuleJobTimeout, Path: "scripts/test.sh", Reason: "longer job timeout", Expires: "2026-11-28"}
	err := ValidateExceptions([]Exception{entry}, exceptionsToday)
	want := "rule job-timeout must name one workflow file directly in .github/workflows by path, such as .github/workflows/ci.yml"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("ValidateExceptions = %v; want %q", err, want)
	}
}

// validateWorkflowTarget runs the workflow-target cases for one rule.
func validateWorkflowTarget(t *testing.T, rule string) {
	entry := func(target string) Exception {
		return Exception{Rule: rule, Path: target, Reason: "the workflow falls short of the rule", Expires: "2026-11-28"}
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
		return Exception{Rule: ExceptionRuleWorkflowTriggers, Path: path, Reason: "runs everywhere", Category: "A", Expires: expires}
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

// Positive: valid data paths pass and decode from manifest. Negative: trailing slash, ..,
// absolute, repeated and 65 entries are refused.
func TestValidateClangTidyDataPaths(t *testing.T) {
	baseLane := ClangTidyLane{Name: "cpu", CompileDatabase: "build/compile_commands.json"}
	valid := &ClangTidyPolicy{
		Lanes:     []ClangTidyLane{baseLane},
		DataPaths: []string{"tests/data", "fixtures/sample"},
	}
	if err := ValidateClangTidy(valid); err != nil {
		t.Fatalf("valid data paths refused: %v", err)
	}

	cases := map[string]struct {
		paths []string
		want  string
	}{
		"trailing slash": {[]string{"tests/data/"}, "without trailing slash or '..'"},
		"parent path":    {[]string{"tests/../data"}, "without trailing slash or '..'"},
		"dotdot prefix":  {[]string{"../data"}, "without trailing slash or '..'"},
		"absolute path":  {[]string{"/tests/data"}, "without trailing slash or '..'"},
		"repeated path":  {[]string{"tests/data", "tests/data"}, "repeats the path \"tests/data\""},
	}
	for name, tc := range cases {
		policy := &ClangTidyPolicy{Lanes: []ClangTidyLane{baseLane}, DataPaths: tc.paths}
		if err := ValidateClangTidy(policy); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: ValidateClangTidy = %v, want error containing %q", name, err, tc.want)
		}
	}

	paths := make([]string, MaxClangTidyDataPaths+1)
	for index := range paths {
		paths[index] = fmt.Sprintf("data/path-%d", index)
	}
	if err := ValidateClangTidy(&ClangTidyPolicy{Lanes: []ClangTidyLane{baseLane}, DataPaths: paths[:MaxClangTidyDataPaths]}); err != nil {
		t.Fatalf("%d data paths refused: %v", MaxClangTidyDataPaths, err)
	}
	if err := ValidateClangTidy(&ClangTidyPolicy{Lanes: []ClangTidyLane{baseLane}, DataPaths: paths}); err == nil || !strings.Contains(err.Error(), "maximum is 64") {
		t.Fatalf("%d data paths accepted: %v", len(paths), err)
	}

	m, err := LoadManifest(writeManifest(t, "version: 1\nclang_tidy:\n  lanes:\n    - name: \"cpu\"\n      compile_database: \"b.json\"\n  data_paths:\n    - \"tests/data\"\n"))
	if err != nil || len(m.ClangTidy.DataPaths) != 1 || m.ClangTidy.DataPaths[0] != "tests/data" {
		t.Fatalf("LoadManifest data_paths = %v, %+v", err, m.ClangTidy)
	}
}

// The root-license-notice and reuse-annotation-order rules name one root file per entry.
// Positive: an entry naming a root file by path validates and decodes from the manifest.
// Negative: a glob, or a path below the root, is refused, naming the rule. Boundary: the same
// root file under the other rule is another entry, not a repeat.
func TestValidateExceptionsRootLicenseNotice(t *testing.T) {
	notice := Exception{Rule: ExceptionRuleRootLicenseNotice, Path: "COPYING", Reason: "upstream GPL notice kept verbatim", Expires: "2026-11-28"}
	if err := ValidateExceptions([]Exception{notice}, exceptionsToday); err != nil {
		t.Fatalf("a root file entry refused: %v", err)
	}
	order := Exception{Rule: ExceptionRuleReuseAnnotationOrder, Path: "REUSE.toml", Reason: "4000 vendored globs", Expires: "2026-11-28"}
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
		"    reason: \"upstream notice\"\n    category: A\n    expires: \""+expires+"\"\n"))
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
		Expires: "2026-11-28",
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
	manifest := fmt.Sprintf("version: 1\nexceptions:\n  - rule: %q\n    path: %q\n    reason: %q\n    category: A\n    expires: %q\n",
		ExceptionRuleDedupe, "pkg/deepcopy.go", "generated clones", expires)
	m, err := LoadManifest(writeManifest(t, manifest))
	if err != nil || len(ExceptionsFor(m.Exceptions, ExceptionRuleDedupe)) != 1 {
		t.Fatalf("LoadManifest: %v, %+v", err, m)
	}
}

func TestLoadExceptionsFor_Positive(t *testing.T) {
	root := t.TempDir()
	expires := ExceptionDay(time.Now()).AddDate(0, 0, 30).Format(ExceptionDateLayout)
	content := fmt.Sprintf("version: 1\nexceptions:\n  - rule: %q\n    path: %q\n    reason: %q\n    category: A\n    expires: %q\n",
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

func TestExceptionDaysLeft_3D(t *testing.T) {
	today := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)

	// Positive: future date
	future := Exception{Expires: "2026-10-24"}
	days, err := future.DaysLeft(today)
	if err != nil || days != 14 {
		t.Fatalf("future.DaysLeft() = (%d, %v), want (14, nil)", days, err)
	}

	// Boundary: today, tomorrow, yesterday
	onToday := Exception{Expires: "2026-10-10"}
	if days, err := onToday.DaysLeft(today); err != nil || days != 0 {
		t.Fatalf("onToday.DaysLeft() = (%d, %v), want (0, nil)", days, err)
	}
	tomorrow := Exception{Expires: "2026-10-11"}
	if days, err := tomorrow.DaysLeft(today); err != nil || days != 1 {
		t.Fatalf("tomorrow.DaysLeft() = (%d, %v), want (1, nil)", days, err)
	}
	yesterday := Exception{Expires: "2026-10-09"}
	if days, err := yesterday.DaysLeft(today); err != nil || days != -1 {
		t.Fatalf("yesterday.DaysLeft() = (%d, %v), want (-1, nil)", days, err)
	}

	// Negative: malformed date
	bad := Exception{Expires: "not-a-date"}
	if _, err := bad.DaysLeft(today); err == nil {
		t.Fatal("bad.DaysLeft() want error, got nil")
	}
}

func TestExceptionExpiringWithin_3D(t *testing.T) {
	today := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)

	// Positive: within window
	e7 := Exception{Expires: "2026-10-17"}
	if !e7.ExpiringWithin(today, 14) {
		t.Fatal("e7 (7 days left) want ExpiringWithin(14) == true")
	}

	// Boundary: exactly 14, 15, and 0 days
	e14 := Exception{Expires: "2026-10-24"}
	if !e14.ExpiringWithin(today, 14) {
		t.Fatal("e14 (14 days left) want ExpiringWithin(14) == true")
	}
	e15 := Exception{Expires: "2026-10-25"}
	if e15.ExpiringWithin(today, 14) {
		t.Fatal("e15 (15 days left) want ExpiringWithin(14) == false")
	}
	e0 := Exception{Expires: "2026-10-10"}
	if !e0.ExpiringWithin(today, 14) {
		t.Fatal("e0 (0 days left) want ExpiringWithin(14) == true")
	}

	// Negative: already expired (-1 days) and malformed
	eExpired := Exception{Expires: "2026-10-09"}
	if eExpired.ExpiringWithin(today, 14) {
		t.Fatal("eExpired (-1 days left) want ExpiringWithin(14) == false")
	}
	bad := Exception{Expires: "invalid"}
	if bad.ExpiringWithin(today, 14) {
		t.Fatal("bad date want ExpiringWithin(14) == false")
	}
}

func TestFormatDaysLeft_Cases(t *testing.T) {
	cases := []struct {
		days int
		want string
	}{
		{14, "14 days left"},
		{1, "1 day left"},
		{0, "0 days left"},
		{-1, "expired 1 day ago (-1 days left)"},
		{-5, "expired 5 days ago (-5 days left)"},
	}
	for _, tc := range cases {
		if got := FormatDaysLeft(tc.days); got != tc.want {
			t.Errorf("FormatDaysLeft(%d) = %q, want %q", tc.days, got, tc.want)
		}
	}
}

func assertFilteredPaths(t *testing.T, got []Exception, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Path != want[i] {
			t.Errorf("got[%d].Path = %q, want %q", i, got[i].Path, want[i])
		}
	}
}

func TestFilterExceptionsExpiringWithin_3D(t *testing.T) {
	today := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)
	entries := []Exception{
		{Rule: ExceptionRuleSupplyChain, Path: ".github/workflows/a.yml", Expires: "2026-10-15"}, // 5 days
		{Rule: ExceptionRuleSupplyChain, Path: ".github/workflows/b.yml", Expires: "2026-10-24"}, // 14 days
		{Rule: ExceptionRuleSupplyChain, Path: ".github/workflows/c.yml", Expires: "2026-10-25"}, // 15 days
		{Rule: ExceptionRuleSupplyChain, Path: ".github/workflows/d.yml", Expires: "2026-10-09"}, // -1 days (expired)
	}

	t.Run("Positive_Window14", func(t *testing.T) {
		res, err := FilterExceptionsExpiringWithin(entries, today, 14)
		if err != nil {
			t.Fatalf("FilterExceptionsExpiringWithin(14) failed: %v", err)
		}
		assertFilteredPaths(t, res, ".github/workflows/a.yml", ".github/workflows/b.yml", ".github/workflows/d.yml")
	})

	t.Run("Boundary_Window5And4", func(t *testing.T) {
		res5, err := FilterExceptionsExpiringWithin(entries, today, 5)
		if err != nil {
			t.Fatalf("FilterExceptionsExpiringWithin(5) failed: %v", err)
		}
		assertFilteredPaths(t, res5, ".github/workflows/a.yml", ".github/workflows/d.yml")

		res4, err := FilterExceptionsExpiringWithin(entries, today, 4)
		if err != nil {
			t.Fatalf("FilterExceptionsExpiringWithin(4) failed: %v", err)
		}
		assertFilteredPaths(t, res4, ".github/workflows/d.yml")
	})

	t.Run("Negative_MalformedDate", func(t *testing.T) {
		broken := []Exception{{Expires: "not-a-date"}}
		if _, err := FilterExceptionsExpiringWithin(broken, today, 14); err == nil {
			t.Fatal("FilterExceptionsExpiringWithin on broken date want error, got nil")
		}
	})
}

// MatchesAny (#1259). Positive: a glob matching one file, also through a "**" segment.
// Negative: a glob matching none. Boundary: an empty file list, and a path or tool entry,
// which MatchesAny never reports (the audit checks a path on disk).
func TestExceptionMatchesAny(t *testing.T) {
	files := []string{"src/gen/a.pb.go", "docs/deep/x/y.md"}
	cases := []struct {
		name  string
		entry Exception
		files []string
		want  bool
	}{
		{"glob matches a file", Exception{Glob: "src/gen/*.pb.go"}, files, true},
		{"double star spans segments", Exception{Glob: "docs/**/*.md"}, files, true},
		{"glob matches nothing", Exception{Glob: "src/gen/*.pb.c"}, files, false},
		{"star stays inside one segment", Exception{Glob: "src/*.pb.go"}, files, false},
		{"no files", Exception{Glob: "src/gen/*.pb.go"}, nil, false},
		{"path entry", Exception{Path: "src/gen/a.pb.go"}, files, false},
		{"tool entry", Exception{Tool: "gitleaks"}, files, false},
	}
	for _, tc := range cases {
		if got := tc.entry.MatchesAny(tc.files); got != tc.want {
			t.Errorf("%s: MatchesAny = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// WarningDays (HISS-15 3D). Positive: categorized and uncategorized entries with valid
// dates. Negative: unparsable dates and unknown categories. Boundary: interval halving
// truncation (B: 3 -> 1) and clamping to ExceptionExpiryWarningDays (14).
var warningDaysCases = []struct {
	name     string
	entry    Exception
	wantDays int
}{
	{
		name:     "none",
		entry:    Exception{Rule: "HISS-11", Expires: "2026-10-24"},
		wantDays: 14,
	},
	{
		name:     "category B",
		entry:    Exception{Rule: "HISS-11", Category: "B", Entered: "2026-10-01", Expires: "2026-10-04"},
		wantDays: 1,
	},
	{
		name:     "category C",
		entry:    Exception{Rule: "HISS-11", Category: "C", Entered: "2026-10-01", Expires: "2026-10-11"},
		wantDays: 5,
	},
	{
		name:     "category D",
		entry:    Exception{Rule: "HISS-11", Category: "D", Entered: "2026-10-01", Expires: "2027-01-29"},
		wantDays: 14,
	},
	{
		name:     "category A entered 2026-10-01 expires 2026-10-11",
		entry:    Exception{Rule: "HISS-11", Category: "A", Entered: "2026-10-01", Expires: "2026-10-11"},
		wantDays: 5,
	},
	{
		name:     "category A entered 2026-10-01 expires 2026-12-30",
		entry:    Exception{Rule: "HISS-11", Category: "A", Entered: "2026-10-01", Expires: "2026-12-30"},
		wantDays: 14,
	},
	{
		name:     "category A unparsable entered",
		entry:    Exception{Rule: "HISS-11", Category: "A", Entered: "bad-date", Expires: "2026-10-11"},
		wantDays: 14,
	},
	{
		name:     "category A unparsable expires",
		entry:    Exception{Rule: "HISS-11", Category: "A", Entered: "2026-10-01", Expires: "bad-date"},
		wantDays: 14,
	},
	{
		name:     "category A expires before entered",
		entry:    Exception{Rule: "HISS-11", Category: "A", Entered: "2026-10-11", Expires: "2026-10-01"},
		wantDays: 0,
	},
	{
		name:     "unknown category",
		entry:    Exception{Rule: "HISS-11", Category: "Z", Entered: "2026-10-01", Expires: "2026-10-11"},
		wantDays: 14,
	},
}

func TestException_WarningDays_3D(t *testing.T) {
	for _, tc := range warningDaysCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.entry.WarningDays(); got != tc.wantDays {
				t.Errorf("WarningDays() = %d, want %d", got, tc.wantDays)
			}
		})
	}
}

func TestExceptionRuleSecrets_3D(t *testing.T) {
	valid := Exception{
		Rule:    ExceptionRuleSecrets,
		Path:    "settings.py",
		Tool:    "generic-api-key",
		Expires: "2026-11-28",
		Reason:  "legacy test token",
	}
	if err := ValidateExceptions([]Exception{valid}, exceptionsToday); err != nil {
		t.Fatalf("positive: valid HISS-12 exception failed: %v", err)
	}

	withFp := valid
	withFp.Fingerprint = "abcd1234abcd1234:settings.py:generic-api-key:10"
	if err := ValidateExceptions([]Exception{withFp}, exceptionsToday); err != nil {
		t.Fatalf("positive: HISS-12 exception with fingerprint failed: %v", err)
	}

	negatives := map[string]func(*Exception){
		"glob refused":       func(e *Exception) { e.Glob = "*.py"; e.Path = "" },
		"missing path":       func(e *Exception) { e.Path = "" },
		"missing tool":       func(e *Exception) { e.Tool = "" },
		"whitespace in tool": func(e *Exception) { e.Tool = "generic api key" },
		"whitespace in fp":   func(e *Exception) { e.Fingerprint = "bad fp here" },
		"invalid path glob":  func(e *Exception) { e.Path = "dir/*.py" },
		"path traversal":     func(e *Exception) { e.Path = "../settings.py" },
	}
	for name, edit := range negatives {
		bad := valid
		edit(&bad)
		if err := ValidateExceptions([]Exception{bad}, exceptionsToday); err == nil {
			t.Errorf("negative: %s: want error, got nil", name)
		}
	}

	boundaryFp := valid
	boundaryFp.Fingerprint = strings.Repeat("a", 512)
	if err := ValidateExceptions([]Exception{boundaryFp}, exceptionsToday); err != nil {
		t.Fatalf("boundary: 512-byte fingerprint want nil, got %v", err)
	}
	boundaryFp.Fingerprint = strings.Repeat("a", 513)
	if err := ValidateExceptions([]Exception{boundaryFp}, exceptionsToday); err == nil {
		t.Fatal("boundary: 513-byte fingerprint want error, got nil")
	}
}

func TestExceptionMatchesSecret_3D(t *testing.T) {
	entry := Exception{
		Rule:        ExceptionRuleSecrets,
		Path:        "pkg/secrets.go",
		Tool:        "generic-api-key",
		Fingerprint: "11112222:pkg/secrets.go:generic-api-key:42",
	}

	if !entry.MatchesSecret("pkg/secrets.go", "generic-api-key", "11112222:pkg/secrets.go:generic-api-key:42") {
		t.Errorf("positive: exact match failed")
	}

	noFp := entry
	noFp.Fingerprint = ""
	if !noFp.MatchesSecret("pkg/secrets.go", "generic-api-key", "99998888:pkg/secrets.go:generic-api-key:99") {
		t.Errorf("positive: path and rule match with any fingerprint failed")
	}

	if entry.MatchesSecret("other/file.go", "generic-api-key", "11112222:pkg/secrets.go:generic-api-key:42") {
		t.Errorf("negative: path mismatch must not match")
	}
	if entry.MatchesSecret("pkg/secrets.go", "aws-access-key", "11112222:pkg/secrets.go:generic-api-key:42") {
		t.Errorf("negative: rule mismatch must not match")
	}
	if entry.MatchesSecret("pkg/secrets.go", "generic-api-key", "different:fp") {
		t.Errorf("negative: fingerprint mismatch must not match")
	}

	wrongRule := entry
	wrongRule.Rule = ExceptionRuleCredits
	if wrongRule.MatchesSecret("pkg/secrets.go", "generic-api-key", "11112222:pkg/secrets.go:generic-api-key:42") {
		t.Errorf("negative: non-HISS-12 rule must not match")
	}
}

// Positive: a codespell entry naming one file with category C, entered today and expiring today+10
// passes. Negative: a glob, a missing category, a tool and an invalid expiry are refused. Boundary:
// category A with an explicit expiry passes.
func TestValidateExceptionsCodespell(t *testing.T) {
	today := exceptionsToday
	valid := Exception{
		Rule:     ExceptionRuleCodespell,
		Path:     "tools/figures/player/player.js",
		Reason:   "minified third-party interfig bundle",
		Category: "C",
		Entered:  today.Format(ExceptionDateLayout),
		Expires:  today.AddDate(0, 0, 10).Format(ExceptionDateLayout),
	}
	if err := ValidateExceptions([]Exception{valid}, today); err != nil {
		t.Fatalf("valid codespell exception refused: %v", err)
	}

	globbed := valid
	globbed.Path = ""
	globbed.Glob = "tools/figures/player/*.js"
	if err := ValidateExceptions([]Exception{globbed}, today); err == nil || !strings.Contains(err.Error(), "must name one file by path, not a glob") {
		t.Fatalf("globbed codespell exception accepted: %v", err)
	}

	noCat := valid
	noCat.Category = ""
	noCat.Entered = ""
	if err := ValidateExceptions([]Exception{noCat}, today); err == nil || !strings.Contains(err.Error(), "rule codespell requires a category") {
		t.Fatalf("uncategorized codespell exception accepted: %v", err)
	}

	withTool := valid
	withTool.Tool = "codespell"
	if err := ValidateExceptions([]Exception{withTool}, today); err == nil || !strings.Contains(err.Error(), "must not set tool") {
		t.Fatalf("codespell exception with tool accepted: %v", err)
	}

	badExpiry := valid
	badExpiry.Expires = today.AddDate(0, 0, 15).Format(ExceptionDateLayout)
	if err := ValidateExceptions([]Exception{badExpiry}, today); err == nil || !strings.Contains(err.Error(), "requires expires") {
		t.Fatalf("codespell exception with bad expiry accepted: %v", err)
	}
}

func TestValidateExceptionsDedupeScope(t *testing.T) {
	cases := []struct{ name, scope, want string }{
		{"unscoped", "", ""},
		{"prefix", "fingerprint: abcdef12", ""},
		{"odd prefix", "fingerprint: abcdef123", ""},
		{"full hash", "fingerprint: " + strings.Repeat("a", 64), ""},
		{"uppercase", "fingerprint: ABCDEF12", ""},
		{"short hash", "fingerprint: abcdef1", "fingerprint"},
		{"long hash", "fingerprint: " + strings.Repeat("a", 65), "fingerprint"},
		{"nonhex hash", "fingerprint: abcdef1z", "fingerprint"},
		{"whitespace hash", "fingerprint: ' abcdef12'", "fingerprint"},
		{"single line", "lines: 1-1", ""},
		{"both scopes", "lines: 10-20\nfingerprint: abcdef12", ""},
		{"missing end", "lines: '1-'", "lines"},
		{"missing separator", "lines: '12'", "lines"},
		{"zero", "lines: 0-10", "lines"},
		{"negative", "lines: -1-10", "lines"},
		{"reversed", "lines: 20-10", "lines"},
		{"whitespace", "lines: '1 - 10'", "lines"},
		{"extra separator", "lines: 1-2-3", "lines"},
		{"overflow", "lines: 1-99999999999999999999999", "lines"},
		{"nondigit", "lines: one-two", "lines"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := validException()
			entry.Rule = ExceptionRuleDedupe
			if err := yaml.Unmarshal([]byte(tc.scope), &entry); err != nil {
				t.Fatal(err)
			}
			err := ValidateExceptions([]Exception{entry}, exceptionsToday)
			if tc.want == "" && err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("want named %s refusal, got %v", tc.want, err)
			}
		})
	}
}

func TestLoadManifestDedupeScopeSchemaRoundTrip(t *testing.T) {
	expires := ExceptionDay(time.Now()).AddDate(0, 0, 30).Format(ExceptionDateLayout)
	body := fmt.Sprintf(`version: 1
exceptions:
  - rule: HISS-19
    path: tests/common/mod.rs
    fingerprint: abcdef123
    lines: 10-16
    reason: retained fixture clone
    category: A
    expires: %s
  - rule: HISS-19
    path: tests/common/mod.rs
    fingerprint: abcdef123
    lines: 30-36
    reason: second copy of retained fixture clone
    category: A
    expires: %s
`, expires, expires)
	manifest, err := LoadManifest(writeManifest(t, body))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadManifest(writeManifest(t, string(rendered)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest.Exceptions, again.Exceptions) || !strings.Contains(string(rendered), "lines: 10-16") {
		t.Fatalf("scopes lost on round trip: %s", rendered)
	}
	if err := ValidateExceptions(append(again.Exceptions, again.Exceptions[0]), time.Now()); err == nil {
		t.Fatal("identical scoped entries must still be refused")
	}
}

func TestDedupeExceptionForScopedUsage(t *testing.T) {
	entry := validException()
	entry.Rule, entry.Fingerprint, entry.Lines = ExceptionRuleDedupe, "abcdef12", "10-20"
	expired, absent, other := entry, entry, entry
	expired.Expires = "2026-10-05"
	absent.Fingerprint, other.Path = "12345678", "other.go"
	entries := []Exception{expired, absent, other, entry}
	for _, line := range []int{9, 10, 20, 21} {
		t.Run(fmt.Sprintf("line=%d", line), func(t *testing.T) {
			used := make([]bool, len(entries))
			live, old := DedupeExceptionFor(entries, entry.Path, "abcdef1234567890", line, exceptionsToday, used)
			var wantLive, wantOld *Exception
			wantUsed := make([]bool, len(entries))
			if line >= 10 && line <= 20 {
				wantLive, wantOld = &entries[3], &entries[0]
				wantUsed[0], wantUsed[3] = true, true
			}
			if live != wantLive || old != wantOld || !slices.Equal(used, wantUsed) {
				t.Fatalf("scoped judgement: live=%+v expired=%+v used=%v", live, old, used)
			}
		})
	}

	for _, used := range [][]bool{nil, make([]bool, 1)} {
		live, _ := DedupeExceptionFor(entries, entry.Path, "abcdef1234567890", 10, exceptionsToday, used)
		if live == nil {
			t.Fatal("short usage slice must not prevent judgement")
		}
	}
}

func TestDedupeExceptionScopeGuards(t *testing.T) {
	entry := validException()
	entry.Rule = ExceptionRuleDedupe
	entry.Lines = "20-10"
	if live, _ := DedupeExceptionFor([]Exception{entry}, entry.Path, "abcdef12", 10, exceptionsToday, nil); live != nil {
		t.Fatal("malformed line range must not excuse a location")
	}
	entry = validException()
	entry.Lines = "10-20"
	if err := ValidateExceptions([]Exception{entry}, exceptionsToday); err == nil || !strings.Contains(err.Error(), "lines") {
		t.Fatalf("line scope for another rule must be refused by name: %v", err)
	}
}

func TestException_UILatencySource(t *testing.T) {
	today := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	base := Exception{
		Rule:     ExceptionRuleUILatencySource,
		Category: "C",
		Entered:  "2026-10-28",
		Expires:  "2026-11-03",
		Reason:   "threshold schema owner cordanaLLM/Aegis-OS M28, parked until 2026-11-03",
	}
	if err := ValidateExceptions([]Exception{base}, today); err != nil {
		t.Fatalf("valid ui-latency-source exception refused: %v", err)
	}
	withStandardsPath := base
	withStandardsPath.Path = ManifestFileName
	if err := ValidateExceptions([]Exception{withStandardsPath}, today); err != nil {
		t.Fatalf("ui-latency-source with .standards.yaml path refused: %v", err)
	}

	noCat := base
	noCat.Category = ""
	if err := ValidateExceptions([]Exception{noCat}, today); err == nil || !strings.Contains(err.Error(), "rule ui-latency-source requires a category") {
		t.Fatalf("missing category must be refused, got: %v", err)
	}

	withGlob := base
	withGlob.Glob = "*.yaml"
	if err := ValidateExceptions([]Exception{withGlob}, today); err == nil || !strings.Contains(err.Error(), "must not set glob") {
		t.Fatalf("glob must be refused, got: %v", err)
	}

	withTool := base
	withTool.Tool = "tool"
	if err := ValidateExceptions([]Exception{withTool}, today); err == nil || !strings.Contains(err.Error(), "must not set tool") {
		t.Fatalf("tool must be refused, got: %v", err)
	}

	foreignPath := base
	foreignPath.Path = "other.txt"
	if err := ValidateExceptions([]Exception{foreignPath}, today); err == nil || !strings.Contains(err.Error(), "path must be .standards.yaml") {
		t.Fatalf("foreign path must be refused, got: %v", err)
	}
}
