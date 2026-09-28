package forge

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// The pull request template and the issue forms restate the HISS-04 function-length cap for
// whoever opens a pull request or files an issue. The audit caps every repository at
// hiss.DefaultMaxFuncLOC whatever its manifest declares (config.AuditMaxFuncLOC), so a template
// stating another value offers contributors a limit the audit rejects; the bug form offered
// "LOC <= 75" while the audit enforced 60 (#574).

// statedLOCBound matches a function-length bound as the templates write it: "LOC <= 60" in the
// issue forms and "LOC $\le 60$" in the pull request template.
var statedLOCBound = regexp.MustCompile(`LOC\s*(?:<=|\$\\le)\s*(\d+)`)

// maxStatedBounds bounds how many bounds one template is scanned for (HISS-02).
const maxStatedBounds = 64

// statedLOCBounds returns every function-length bound text states, in order.
func statedLOCBounds(text string) []int {
	var bounds []int
	for _, match := range statedLOCBound.FindAllStringSubmatch(text, maxStatedBounds) {
		if bound, err := strconv.Atoi(match[1]); err == nil {
			bounds = append(bounds, bound)
		}
	}
	return bounds
}

// contributorTemplates returns every issue form and the pull request template of the checkout.
func contributorTemplates(t *testing.T) []string {
	t.Helper()
	forms, err := filepath.Glob(filepath.Join(engineCheckout, ".github", "ISSUE_TEMPLATE", "*.yml"))
	if err != nil || len(forms) == 0 {
		t.Fatalf("no issue forms found: %v", err)
	}
	return append(forms, filepath.Join(engineCheckout, ".github", "pull_request_template.md"))
}

// Positive: every function-length bound the shipped templates state is the audit ceiling, and
// the bug form and the pull request template each state one, so the scan cannot pass by
// finding none.
func TestContributorTemplates_Positive_StateTheAuditFuncLOCCeiling(t *testing.T) {
	stated := map[string]int{}
	for _, path := range contributorTemplates(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bounds := statedLOCBounds(string(data))
		stated[filepath.Base(path)] = len(bounds)
		for _, bound := range bounds {
			if bound != hiss.DefaultMaxFuncLOC {
				t.Errorf("%s states func LOC <= %d; the audit enforces %d (hiss.DefaultMaxFuncLOC)",
					filepath.ToSlash(path), bound, hiss.DefaultMaxFuncLOC)
			}
		}
	}
	for _, name := range []string{"bug.yml", "pull_request_template.md"} {
		if stated[name] == 0 {
			t.Errorf("%s states no function-length bound; the scan no longer reads it", name)
		}
	}
}

// Negative: the bound the bug form used to offer reads as 75, which is not the ceiling, and the
// McCabe cap on the same line is not mistaken for a function length.
func TestContributorTemplates_Negative_StaleBoundIsRead(t *testing.T) {
	got := statedLOCBounds(`- "HISS-04: Complexity Bounds (McCabe <= 10, LOC <= 75)"`)
	if !slices.Equal(got, []int{75}) {
		t.Fatalf("stated bounds = %v, want [75]", got)
	}
	if got[0] == hiss.DefaultMaxFuncLOC {
		t.Fatalf("stale bound %d equals the audit ceiling; the negative case proves nothing", got[0])
	}
}

// Boundary: both notations are read, a bound with no digits is not, and text without a
// function-length bound states none.
func TestContributorTemplates_Boundary_NotationsAndAbsence(t *testing.T) {
	if got := statedLOCBounds(`Func LOC $\le 60$, Statements $\le 50$`); !slices.Equal(got, []int{60}) {
		t.Errorf("LaTeX bound = %v, want [60]", got)
	}
	if got := statedLOCBounds("LOC <= N"); len(got) != 0 {
		t.Errorf("a bound without digits stated %v", got)
	}
	if got := statedLOCBounds("McCabe <= 10, cognitive <= 15"); len(got) != 0 {
		t.Errorf("text without a function-length bound stated %v", got)
	}
}
