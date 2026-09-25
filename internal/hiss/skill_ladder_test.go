package hiss

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// hissAuditSkill is the canonical skill whose ladder names the rules Scan() runs; every
// projected copy is compiled from it.
var hissAuditSkill = filepath.Join("..", "..", ".agents", "skills", "hiss-audit", "SKILL.md")

var (
	ruleLiteral   = regexp.MustCompile(`"(HISS-\d{2})"`)
	ruleCitation  = regexp.MustCompile(`HISS-\d{2}`)
	ladderStep    = regexp.MustCompile(`^\d+\. \*\*`)
	hardcodedSpan = regexp.MustCompile(`HISS-\d{2}\s*(?:through|to|\.\.|–|—)\s*(?:HISS-)?\d{2}\b`)
)

// maxLadderLines bounds the skill read line by line (HISS-02).
const maxLadderLines = 512

// scannedRules returns the rule IDs this package can report: every HISS literal in its
// production sources is an argument to record or recordViolation.
func scannedRules(t *testing.T) []string {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	set := make(map[string]bool)
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name) // #nosec G304 -- a source file of this package.
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range ruleLiteral.FindAllStringSubmatch(string(data), -1) {
			set[match[1]] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// ladderRules returns the rule IDs the skill lists under its AST scan step, the one
// numbered step whose title names "AST Scan".
func ladderRules(t *testing.T, text string) []string {
	t.Helper()
	lines := strings.Split(text, "\n")
	set := make(map[string]bool)
	inStep := false
	for i := 0; i < len(lines) && i < maxLadderLines; i++ {
		line := lines[i]
		if ladderStep.MatchString(line) {
			inStep = strings.Contains(line, "AST Scan")
			continue
		}
		if inStep && strings.HasPrefix(strings.TrimSpace(line), "- `HISS-") {
			set[ruleCitation.FindString(line)] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// TestHISSAuditSkill_Positive_LadderMatchesScan keeps the skill's scanner step equal to
// what Scan() implements. The ladder once listed HISS-03 and HISS-10 as AST scanners; no
// rule here reports either.
func TestHISSAuditSkill_Positive_LadderMatchesScan(t *testing.T) {
	data, err := os.ReadFile(hissAuditSkill)
	if err != nil {
		t.Fatal(err)
	}
	want := scannedRules(t)
	if len(want) == 0 {
		t.Fatal("no rule literal found; the source glob is wrong")
	}
	got := ladderRules(t, string(data))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hiss-audit ladder lists %v as scanned; Scan() reports %v", got, want)
	}
}

// TestHISSAuditSkill_Negative_LadderRejectsUnscannedRule proves the comparison is not
// vacuous: the ladder this skill shipped before, which named HISS-03 and HISS-10 as AST
// scanners, yields a set Scan() does not match, and rules listed under a later step are
// not collected as scanned.
func TestHISSAuditSkill_Negative_LadderRejectsUnscannedRule(t *testing.T) {
	want := strings.Join(scannedRules(t), ",")
	old := "2. **Execute Static AST Scanners**:\n   - `HISS-01 (x)`\n   - `HISS-03 (Zero Frame Malloc)`\n" +
		"   - `HISS-10 (Zero-Warning Cascade)`\n3. **Audit 3D Test Coverage (HISS-15)**:\n"
	got := ladderRules(t, old)
	if strings.Join(got, ",") == want || strings.Join(got, ",") != "HISS-01,HISS-03,HISS-10" {
		t.Fatalf("old ladder parsed as %v; want HISS-01,HISS-03,HISS-10, unequal to Scan() set %s", got, want)
	}
	later := "2. **HISS AST Scan**:\n   - `HISS-01 (x)`\n3. **Gates Outside `Scan()`**:\n   - `HISS-10 (x)`\n"
	if got := ladderRules(t, later); len(got) != 1 || got[0] != "HISS-01" {
		t.Fatalf("rules outside the AST scan step were collected: %v", got)
	}
}

// TestAgentText_Boundary_CitesNoHardcodedHISSRange rejects "HISS-01 through HISS-16" style
// spans in canonical agent text. The invariant set grows (HISS-17 to HISS-21 arrived after
// such a span was written), so agent text points at the AGENTS.md table instead.
func TestAgentText_Boundary_CitesNoHardcodedHISSRange(t *testing.T) {
	root := filepath.Join("..", "..", ".agents")
	visited := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		visited++
		data, err := os.ReadFile(path) // #nosec G304 -- a tracked file under .agents.
		if err != nil {
			return err
		}
		if span := hardcodedSpan.FindString(string(data)); span != "" {
			t.Errorf("%s hardcodes the invariant range %q", path, span)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visited == 0 {
		t.Fatal("no agent text visited; the .agents path is wrong")
	}
	for _, span := range []string{"HISS-01 through HISS-16", "HISS-01..16", "HISS-01 to 21"} {
		if !hardcodedSpan.MatchString(span) {
			t.Errorf("guard misses %q", span)
		}
	}
	if hardcodedSpan.MatchString("HISS-17 (State Ledger)") {
		t.Error("guard flags a single citation")
	}
}
