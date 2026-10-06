// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
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

// A HISS-11 entry names the release workflow the supply-chain gate measured (#330). Positive: a
// .yml or .yaml file directly in .github/workflows is accepted and selected by its rule.
// Negative: a glob, a file outside .github/workflows or below it, and a file that is no YAML
// document are refused with the rule named. Boundary: the target check binds only HISS-11, so a
// clang-tidy-coverage entry for a C file stays valid beside it.
func TestValidateExceptionsSupplyChainTarget(t *testing.T) {
	entry := func(target string) Exception {
		return Exception{Rule: ExceptionRuleSupplyChain, Path: target, Reason: "release provenance below the declared level",
			Expires: "2026-12-31"}
	}
	for _, target := range []string{".github/workflows/release.yml", ".github/workflows/release-binaries.yaml"} {
		if err := ValidateExceptions([]Exception{validException(), entry(target)}, exceptionsToday); err != nil {
			t.Fatalf("HISS-11 entry for %s refused: %v", target, err)
		}
	}
	if got := ExceptionsFor([]Exception{validException(), entry(".github/workflows/release.yml")}, ExceptionRuleSupplyChain); len(got) != 1 ||
		got[0].Path != ".github/workflows/release.yml" {
		t.Fatalf("ExceptionsFor(HISS-11) = %+v", got)
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
		if err == nil || !strings.Contains(err.Error(), "rule HISS-11 must name one workflow file directly in .github/workflows") {
			t.Errorf("%s: ValidateExceptions = %v; want the HISS-11 target refused", name, err)
		}
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
