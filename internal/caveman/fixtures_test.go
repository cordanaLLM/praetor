package caveman

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The fixtures replay both ways (HISS-20): every pass-* text must pass and every fail-*
// text must fail with exactly the rules its file name lists, no fewer and no more.
var fixtureRuleRe = regexp.MustCompile(`\b[CF]\d\b`)

func readFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(data)
}

// wantRules returns the rule ids a fixture name promises, sorted; none for a pass fixture.
func wantRules(name string) []string {
	ids := fixtureRuleRe.FindAllString(strings.ReplaceAll(name, "-", " "), -1)
	sort.Strings(ids)
	return ids
}

// gotRules returns the distinct rule ids of a report, sorted.
func gotRules(report Report) []string {
	seen := map[string]bool{}
	var ids []string
	for _, f := range report.Findings {
		id := strings.Fields(f.Rule)[0]
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func fixtureNames(t *testing.T, pattern string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join("testdata", pattern))
	if err != nil || len(names) == 0 {
		t.Fatalf("no fixtures match %s: %v", pattern, err)
	}
	return names
}

func TestCheckFixturesReplayBothWays(t *testing.T) {
	var passes, fails int
	for _, path := range fixtureNames(t, "check/*.md") {
		name := filepath.Base(path)
		report := Check(readFixture(t, path), Options{})
		want := wantRules(name)
		if strings.HasPrefix(name, "fail-") {
			fails++
		} else {
			passes++
		}
		if got := gotRules(report); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: rules %v, want %v; findings %v", name, got, want, report.Findings)
		}
	}
	if passes == 0 || fails == 0 {
		t.Fatalf("fixtures must replay both ways: %d pass, %d fail", passes, fails)
	}
}

func TestFloorFixturesReplayBothWays(t *testing.T) {
	before := readFixture(t, filepath.Join("testdata", "floor", "before.md"))
	if report := Floor(before, before); !report.Passed() {
		t.Fatalf("a text must hold its own floor: %v", report.Findings)
	}
	var passes, fails int
	for _, path := range fixtureNames(t, "floor/after-*.md") {
		name := filepath.Base(path)
		report := Floor(before, readFixture(t, path))
		want := wantRules(name)
		if strings.HasPrefix(name, "after-fail-") {
			fails++
		} else {
			passes++
		}
		if got := gotRules(report); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: rules %v, want %v; findings %v", name, got, want, report.Findings)
		}
	}
	if passes == 0 || fails == 0 {
		t.Fatalf("fixtures must replay both ways: %d pass, %d fail", passes, fails)
	}
}

// The measured claim behind the density threshold: the prose fixture sits well above 2.0 and
// the caveman fixture well below it.
func TestCheckFixtureDensitySeparates(t *testing.T) {
	prose := Check(readFixture(t, filepath.Join("testdata", "check", "fail-C1-prose.md")), Options{})
	caveman := Check(readFixture(t, filepath.Join("testdata", "check", "pass-caveman-rules.md")), Options{})
	if prose.Density() < 2*DefaultMaxArticleDensity || caveman.Density() > DefaultMaxArticleDensity/2 {
		t.Fatalf("density prose %.1f, caveman %.1f: threshold %.1f no longer separates them",
			prose.Density(), caveman.Density(), DefaultMaxArticleDensity)
	}
	if caveman.ProseWords < DefaultMinProseWords {
		t.Fatalf("caveman fixture has %d prose words, below the %d the density rule needs", caveman.ProseWords, DefaultMinProseWords)
	}
}
