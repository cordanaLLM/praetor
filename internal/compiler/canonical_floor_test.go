package compiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

// agentsFloorFixture is AGENTS.md as it stood before the caveman rewrite. It is frozen: the
// live file may move facts around, but it may never lose one the fixture carries.
var (
	agentsFloorFixture = filepath.Join("testdata", "agents-floor.txt")
	canonicalAgents    = filepath.Join("..", "..", "AGENTS.md")
)

func readFloorText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestCanonicalAgentsHoldsClarityFloor fails when the live AGENTS.md dropped a rule id, a
// MUST directive, a prohibition, a numbered rule, an inline command, a fenced command, a
// link or a marker that the pre-rewrite fixture carried.
func TestCanonicalAgentsHoldsClarityFloor(t *testing.T) {
	report := caveman.Floor(readFloorText(t, agentsFloorFixture), readFloorText(t, canonicalAgents))
	for _, finding := range report.Findings {
		t.Errorf("AGENTS.md lost a fact of %s: %s", agentsFloorFixture, finding)
	}
}

// TestCanonicalAgentsFloorFixtureIsComplete pins the anchor itself, so the floor cannot be
// weakened by trimming the fixture: 12 HISS rows, 12 numbered rules, 4 MUST directives.
func TestCanonicalAgentsFloorFixtureIsComplete(t *testing.T) {
	fixture := readFloorText(t, agentsFloorFixture)
	counts := map[string]struct {
		re   *regexp.Regexp
		want int
	}{
		"HISS rows":      {regexp.MustCompile(`(?m)^\| \*\*HISS-\d+\*\*`), 12},
		"numbered rules": {regexp.MustCompile(`(?m)^\d+\. \*\*`), 12},
		"MUST":           {regexp.MustCompile(`\bMUST\b`), 4},
	}
	for name, count := range counts {
		if got := len(count.re.FindAllString(fixture, -1)); got != count.want {
			t.Errorf("fixture %s = %d, want %d", name, got, count.want)
		}
	}
}

// TestCanonicalAgentsFloorCatchesLoss cuts facts of each kind from the live file and expects
// the floor rule that owns them to fire: the anchor test has teeth. Prohibitions are a count
// with headroom, so that case cuts every never and no.
func TestCanonicalAgentsFloorCatchesLoss(t *testing.T) {
	fixture, live := readFloorText(t, agentsFloorFixture), readFloorText(t, canonicalAgents)
	cases := map[string]struct {
		cut  *regexp.Regexp
		rule string
	}{
		"rule id":     {regexp.MustCompile(`\*\*HISS-21\*\*`), caveman.RuleFloorID},
		"MUST":        {regexp.MustCompile(`Agents MUST maintain`), caveman.RuleFloorMust},
		"command":     {regexp.MustCompile(`(?m)^   standardsctl compile-context$`), caveman.RuleFloorCommand},
		"inline code": {regexp.MustCompile("`AskUserQuestion`"), caveman.RuleFloorCodeSpan},
		"prohibition": {regexp.MustCompile(`(?i)\b(?:never|no)\b`), caveman.RuleFloorProhibition},
		"numbered":    {regexp.MustCompile(`12\. \*\*No tool attribution`), caveman.RuleFloorNumbered},
		"link":        {regexp.MustCompile(`\(docs/guides/checkpoint-cadence\.md\)`), caveman.RuleFloorLink},
	}
	for name, tc := range cases {
		if !tc.cut.MatchString(live) {
			t.Fatalf("%s: live AGENTS.md no longer matches %s; update this test", name, tc.cut)
		}
		report := caveman.Floor(fixture, tc.cut.ReplaceAllString(live, ""))
		if !hasFloorRule(report, tc.rule) {
			t.Errorf("%s: cutting %s did not trip %s: %v", name, tc.cut, tc.rule, report.Findings)
		}
	}
}

// TestCanonicalAgentsTurnStartIsBounded pins the HISS-17 turn start: `praetorctl state status`
// in the invariant row and in rule 10, and no instruction to read all of STATE.md.
func TestCanonicalAgentsTurnStartIsBounded(t *testing.T) {
	live := readFloorText(t, canonicalAgents)
	if got := strings.Count(live, "`praetorctl state status`"); got < 2 {
		t.Errorf("state status named %d time(s), want the HISS-17 row and rule 10", got)
	}
	if strings.Contains(strings.ToLower(live), "inspect `.workingdir/state.md`") {
		t.Error("turn start still reads the whole STATE.md")
	}
}

func hasFloorRule(report caveman.Report, rule string) bool {
	for _, finding := range report.Findings {
		if finding.Rule == rule {
			return true
		}
	}
	return false
}
