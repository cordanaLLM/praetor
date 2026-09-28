package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// The pull request template, the issue forms, the contributor guides and the lint configuration
// restate the HISS-04 function-length cap for whoever opens a pull request, files an issue or
// runs the linters. The audit caps every repository at hiss.DefaultMaxFuncLOC whatever its
// manifest declares (config.AuditMaxFuncLOC), so a file stating another value offers
// contributors a limit the audit rejects; the bug form, CONTRIBUTING.md and .golangci.yml said
// 75 while the audit enforced 60 (#574).

// statedLOCBound matches a function-length bound as the repository writes it: "LOC <= 60" in the
// issue forms and .golangci.yml, "LOC $\le 60$" in the pull request template and
// CONTRIBUTING.md, and the unit after the number, "$\le 60$ LOC", in README.md and the HISS
// specification.
var statedLOCBound = regexp.MustCompile(`LOC\s*(?:<=|\$\\le)\s*(\d+)|(?:<=|\$\\le)\s*(\d+)\$?\s*LOC\b`)

// maxStatedBounds bounds how many bounds one file is scanned for (HISS-02).
const maxStatedBounds = 64

// statedLOCBounds returns every function-length bound text states, in order.
func statedLOCBounds(text string) []int {
	var bounds []int
	for _, match := range statedLOCBound.FindAllStringSubmatch(text, maxStatedBounds) {
		digits := match[1]
		if digits == "" {
			digits = match[2]
		}
		if bound, err := strconv.Atoi(digits); err == nil {
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

// statingFiles are the files each of which must state the function-length cap, relative to the
// checkout: the templates that ask for it and the guides and lint configuration that restate it.
var statingFiles = []string{
	".github/ISSUE_TEMPLATE/bug.yml",
	".github/pull_request_template.md",
	"CONTRIBUTING.md",
	"README.md",
	"docs/standards/hiss-spec.md",
	".golangci.yml",
}

// Positive: every function-length bound the shipped templates, guides and lint configuration
// state is the audit ceiling, and each of statingFiles states one, so the scan cannot pass by
// finding none.
func TestContributorTemplates_Positive_StateTheAuditFuncLOCCeiling(t *testing.T) {
	paths := contributorTemplates(t)
	for _, rel := range statingFiles[2:] {
		paths = append(paths, filepath.Join(engineCheckout, filepath.FromSlash(rel)))
	}
	stated := map[string]int{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(engineCheckout, path)
		if err != nil {
			t.Fatal(err)
		}
		bounds := statedLOCBounds(string(data))
		stated[filepath.ToSlash(rel)] += len(bounds)
		for _, bound := range bounds {
			if bound != hiss.DefaultMaxFuncLOC {
				t.Errorf("%s states func LOC <= %d; the audit enforces %d (hiss.DefaultMaxFuncLOC)",
					filepath.ToSlash(rel), bound, hiss.DefaultMaxFuncLOC)
			}
		}
	}
	for _, rel := range statingFiles {
		if stated[rel] == 0 {
			t.Errorf("%s states no function-length bound; the scan no longer reads it", rel)
		}
	}
}

// Negative: the bounds the bug form and README.md used to state read as 75, which is not the
// ceiling, and the McCabe cap on the same line is not mistaken for a function length.
func TestContributorTemplates_Negative_StaleBoundIsRead(t *testing.T) {
	got := statedLOCBounds(`- "HISS-04: Complexity Bounds (McCabe <= 10, LOC <= 75)"`)
	if !slices.Equal(got, []int{75}) {
		t.Fatalf("stated bounds = %v, want [75]", got)
	}
	if got[0] == hiss.DefaultMaxFuncLOC {
		t.Fatalf("stale bound %d equals the audit ceiling; the negative case proves nothing", got[0])
	}
	readme := `complexity $M \le 10$, cognitive complexity $\le 15$, function length $\le 75$ LOC, $\le 50$ executable statements`
	if got := statedLOCBounds(readme); !slices.Equal(got, []int{75}) {
		t.Fatalf("stated bounds in the old README line = %v, want [75]", got)
	}
}

// Boundary: every notation is read, a bound with no digits is not, a line count that is no
// bound is not, and text without a function-length bound states none.
func TestContributorTemplates_Boundary_NotationsAndAbsence(t *testing.T) {
	for text, want := range map[string][]int{
		`Func LOC $\le 60$, Statements $\le 50$`:  {60},
		`| **Function Length** | $\le 60$ LOC |`:  {60},
		`lines: 58 # HISS-04: function LOC <= 60`: {60},
		"function length <= 60 LOC":               {60},
		"LOC <= N":                                nil,
		"CLAUDE.md (&lt; 300 LOC)":                nil,
		"McCabe <= 10, cognitive <= 15":           nil,
	} {
		if got := statedLOCBounds(text); !slices.Equal(got, want) {
			t.Errorf("statedLOCBounds(%q) = %v, want %v", text, got, want)
		}
	}
}

// funlenUncountedLines is how many lines of a function the audit counts and funlen does not.
// The scanner spans the `func` line through the closing brace, both counted (hiss.MeasureFunc);
// funlen counts only the lines between them. With comment lines counted on both sides, funlen
// lines N stops exactly the functions the audit's N+2 stops.
const funlenUncountedLines = 2

// golangciFunlen is the part of a golangci-lint v2 configuration that carries the HISS-04 caps.
type golangciFunlen struct {
	Linters struct {
		Settings struct {
			Funlen *struct {
				Lines          int   `yaml:"lines"`
				Statements     int   `yaml:"statements"`
				IgnoreComments *bool `yaml:"ignore-comments"`
			} `yaml:"funlen"`
		} `yaml:"settings"`
	} `yaml:"linters"`
}

// funlenDrift returns every way the funlen settings in a golangci-lint configuration differ from
// the audit's HISS-04 caps, or nil when funlen stops exactly the functions the audit stops.
// golangci-lint drops comment lines unless ignore-comments is false, so an absent key drifts.
func funlenDrift(data []byte) []string {
	var cfg golangciFunlen
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return []string{"unparsable configuration: " + err.Error()}
	}
	funlen := cfg.Linters.Settings.Funlen
	if funlen == nil {
		return []string{"no linters.settings.funlen block"}
	}
	var drift []string
	if span := funlen.Lines + funlenUncountedLines; span != hiss.DefaultMaxFuncLOC {
		drift = append(drift, fmt.Sprintf("funlen lines %d caps a function at %d lines as the audit counts them; the audit enforces %d (hiss.DefaultMaxFuncLOC)",
			funlen.Lines, span, hiss.DefaultMaxFuncLOC))
	}
	if funlen.IgnoreComments == nil || *funlen.IgnoreComments {
		drift = append(drift, "funlen ignore-comments is not false; the audit counts comment lines")
	}
	if funlen.Statements != hiss.DefaultMaxStatements {
		drift = append(drift, fmt.Sprintf("funlen statements %d; the HISS-04 cap is %d (hiss.DefaultMaxStatements)",
			funlen.Statements, hiss.DefaultMaxStatements))
	}
	return drift
}

// funlenConfig renders a golangci-lint configuration whose funlen block holds settings.
func funlenConfig(lines int, ignoreComments string) []byte {
	settings := fmt.Sprintf("      lines: %d\n      statements: %d\n", lines, hiss.DefaultMaxStatements)
	if ignoreComments != "" {
		settings += "      ignore-comments: " + ignoreComments + "\n"
	}
	return []byte("version: \"2\"\nlinters:\n  settings:\n    funlen:\n" + settings)
}

// Positive: the repository's .golangci.yml runs funlen at the audit's function length, comment
// lines counted, and at the HISS-04 statement cap.
func TestGolangciFunlen_Positive_MeasuresTheAuditSpan(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(engineCheckout, ".golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if drift := funlenDrift(data); len(drift) != 0 {
		t.Fatalf(".golangci.yml funlen drifts from the audit: %v", drift)
	}
}

// Negative: the 75-line setting the repository shipped, the ceiling copied into funlen lines
// with comment lines dropped, a statement cap that moved, a missing block and a malformed file
// each drift.
func TestGolangciFunlen_Negative_StaleAndLooserSettings(t *testing.T) {
	at := hiss.DefaultMaxFuncLOC - funlenUncountedLines
	cases := map[string][]byte{
		"shipped 75":             funlenConfig(75, "false"),
		"ceiling, comments drop": funlenConfig(hiss.DefaultMaxFuncLOC, ""),
		"comments ignored":       funlenConfig(at, "true"),
		"statement cap moved": []byte("linters:\n  settings:\n    funlen:\n      lines: " + strconv.Itoa(at) +
			"\n      statements: 40\n      ignore-comments: false\n"),
		"no funlen block": []byte("version: \"2\"\nlinters:\n  settings:\n    gocyclo:\n      min-complexity: 11\n"),
		"malformed":       []byte("linters: [\n"),
	}
	for name, data := range cases {
		if drift := funlenDrift(data); len(drift) == 0 {
			t.Errorf("%s: no drift reported for\n%s", name, data)
		}
	}
}

// Boundary: funlen lines one under, at and one over the audit's span. Only the exact span
// passes; a stricter funlen would fail functions the audit accepts.
func TestGolangciFunlen_Boundary_OneLineEitherSide(t *testing.T) {
	at := hiss.DefaultMaxFuncLOC - funlenUncountedLines
	for lines, want := range map[int]int{at - 1: 1, at: 0, at + 1: 1} {
		if drift := funlenDrift(funlenConfig(lines, "false")); len(drift) != want {
			t.Errorf("funlen lines %d: drift %v, want %d finding(s)", lines, drift, want)
		}
	}
}
