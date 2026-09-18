package caveman

import (
	"reflect"
	"strings"
	"testing"
)

func TestFloorPositive(t *testing.T) {
	before := "Agents MUST sync (HISS-17). Never stage `.workingdir`.\n```bash\npraetorctl state sync .\n```\n"
	cases := map[string]string{
		"verbatim copy":        before,
		"caveman rewrite":      "HISS-17: agents MUST sync. never stage `.workingdir`.\n```bash\npraetorctl state sync .\n```",
		"facts moved":          "```bash\npraetorctl state sync .\n```\nnever stage `.workingdir`. HISS-17: sync MUST run.",
		"prohibitions gained":  "HISS-17: MUST sync. never stage `.workingdir`; no force, do not bypass.\n```\npraetorctl state sync .\n```",
		"crlf rewrite":         strings.ReplaceAll(before, "\n", "\r\n"),
		"id moved into a code": "Agents MUST sync. Never stage `.workingdir`.\n```bash\n# HISS-17\npraetorctl state sync .\n```",
	}
	for name, after := range cases {
		t.Run(name, func(t *testing.T) {
			if report := Floor(before, after); !report.Passed() {
				t.Fatalf("want the floor held, got %v", report.Findings)
			}
		})
	}
}

func TestFloorNegative(t *testing.T) {
	before := "1. **Sync**: agents MUST sync (HISS-17), see [guide](docs/guides/state.md).\n" +
		"2. **Stage**: never stage `.workingdir`.\n<!-- praetor:harness:end -->\n```bash\npraetorctl state sync .\n```\n"
	cases := map[string]struct {
		after string
		want  []Finding
	}{
		"everything lost": {"agents sync.", []Finding{
			{0, RuleFloorMust, "1 -> 0"},
			{0, RuleFloorProhibition, "1 -> 0"},
			{0, RuleFloorNumbered, "2 -> 0"},
			{1, RuleFloorID, "HISS-17"},
			{1, RuleFloorLink, "docs/guides/state.md"},
			{2, RuleFloorCodeSpan, "`.workingdir`"},
			{3, RuleFloorMarker, "<!-- praetor:harness:end -->"},
			{5, RuleFloorCommand, "praetorctl state sync ."},
		}},
		"code span reworded": {strings.Replace(before, "`.workingdir`", "`.workingdir/`", 1), []Finding{
			{2, RuleFloorCodeSpan, "`.workingdir`"},
		}},
		"command edited": {strings.Replace(before, "sync .", "sync", 1), []Finding{
			{5, RuleFloorCommand, "praetorctl state sync ."},
		}},
		"must lowered": {strings.Replace(before, "MUST", "must", 1), []Finding{
			{0, RuleFloorMust, "1 -> 0"},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Floor(before, tc.after).Findings; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findings\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}

func TestFloorBoundary(t *testing.T) {
	if report := Floor("", ""); !report.Passed() {
		t.Errorf("empty texts: %v", report.Findings)
	}
	if report := Floor("", "MUST never `x`"); !report.Passed() {
		t.Errorf("adding facts never breaks the floor: %v", report.Findings)
	}
	// Equal counts hold the floor; one fewer breaks it.
	if report := Floor("never never", "Never NEVER"); !report.Passed() {
		t.Errorf("prohibition count is case-insensitive and equal: %v", report.Findings)
	}
	if report := Floor("never never", "never"); report.Passed() {
		t.Error("one prohibition fewer must break the floor")
	}
	// Mermaid lines, fence delimiters, blanks and shell comments are not commands.
	diagram := "```mermaid\nflowchart LR\n  A --> B\n```\n```bash\n# comment\n\n```"
	if report := Floor(diagram, "no diagram"); !report.Passed() {
		t.Errorf("a dropped diagram or comment is not a lost command: %v", report.Findings)
	}
	// A repeated fact is reported once, on the line it first appears on.
	report := Floor("`a`\n`a`", "")
	if len(report.Findings) != 1 || report.Findings[0].Line != 1 {
		t.Errorf("repeated span: %v", report.Findings)
	}
}
