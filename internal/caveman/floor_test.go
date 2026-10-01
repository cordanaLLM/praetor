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
	// Empty and blank-only spans are never facts (#356): "``" is one unclosed run, and
	// "` `" holds only a blank.
	if report := Floor("a `` b ` ` c ```", "a b c"); !report.Passed() {
		t.Errorf("empty or blank span became a fact: %v", report.Findings)
	}
}

// TestFloorWrappedCodeSpan pins #320: a code span that wraps across a line break is one fact,
// the same fact as its unwrapped form, and the punctuation between two spans never is one.
func TestFloorWrappedCodeSpan(t *testing.T) {
	before := "Use `alpha/module\n  v1.0.0`, and `beta v2`."
	if got := Floor(before, "- `alpha/module v1.0.0`\n- `beta v2`").Findings; len(got) != 0 {
		t.Fatalf("unwrapped rewrite lost %v", got)
	}
	want := []Finding{{1, RuleFloorCodeSpan, "`alpha/module v1.0.0`"}}
	if got := Floor(before, "- `alpha/module`\n- `beta v2`").Findings; !reflect.DeepEqual(got, want) {
		t.Fatalf("findings\n got %v\nwant %v", got, want)
	}
	// A blockquoted paragraph wraps the same way; its markers are not part of the span.
	if got := Floor("> Use `alpha\n> beta` now.", "Use `alpha beta` now.").Findings; len(got) != 0 {
		t.Fatalf("quoted wrap lost %v", got)
	}
	// A list item starts a new paragraph: a backtick left open on one item never pairs with
	// the next item's backtick.
	if got := Floor("- open ` here\n- `kept`", "- `kept`").Findings; len(got) != 0 {
		t.Fatalf("span paired across list items: %v", got)
	}
}

// TestFloorCommandFences pins #322: F2 reads shell fences only, a session after its prompt.
func TestFloorCommandFences(t *testing.T) {
	cases := map[string]struct {
		before, after string
		want          []Finding
	}{
		"go comment corrected": {"```go\nrun() // old\n```", "```go\nrun() // new\n```", nil},
		"yaml value changed":   {"```yaml\nport: 1\n```", "```yaml\nport: 2\n```", nil},
		"text output changed":  {"```text\nok 1\n```", "```text\nok 2\n```", nil},
		"script edited": {"```bash\nmake serve\n```", "```bash\nmake run\n```",
			[]Finding{{2, RuleFloorCommand, "make serve"}}},
		"session prompt dropped into script": {"```console\n$ make check\nok\n```", "```sh\nmake check\n```", nil},
		"session command edited": {"```console\n$ make check\nok\n```", "```console\n$ make test\nok\n```",
			[]Finding{{2, RuleFloorCommand, "make check"}}},
		"unlabeled fence edited": {"```\nmake lint\n```", "```\nmake vet\n```",
			[]Finding{{2, RuleFloorCommand, "make lint"}}},
		"continuation dropped": {"```console\n$ make check \\\n  --verbose\n```", "```console\n$ make check \\\n```",
			[]Finding{{3, RuleFloorCommand, "--verbose"}}},
		"powershell edited": {"```powershell\nGet-Item x\n```", "```powershell\nGet-Item y\n```",
			[]Finding{{2, RuleFloorCommand, "Get-Item x"}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Floor(tc.before, tc.after).Findings; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findings\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}

// TestFloorBlockquotedFence pins #356: a fence opens inside a blockquote, yields no empty code
// span, and its commands compare without the quote markers.
func TestFloorBlockquotedFence(t *testing.T) {
	quoted := "> ```bash\n> make serve\n> ```\n\n> ```mermaid\n> A --> B\n> ```"
	if got := Floor(quoted, "```bash\nmake serve\n```").Findings; len(got) != 0 {
		t.Fatalf("unquoted rewrite lost %v", got)
	}
	want := []Finding{{2, RuleFloorCommand, "make serve"}}
	if got := Floor(quoted, "no command").Findings; !reflect.DeepEqual(got, want) {
		t.Fatalf("findings\n got %v\nwant %v", got, want)
	}
	// The fence closes with its blockquote: the prose after it carries facts again.
	got := Floor("> ```sh\n> make a\n\n`kept`", "```sh\nmake a\n```").Findings
	if len(got) != 1 || got[0].Rule != RuleFloorCodeSpan {
		t.Fatalf("fence outlived its blockquote: %v", got)
	}
	// A quote marker inside an unquoted fence is content, not a closing delimiter.
	got = Floor("```sh\n> ```\nmake b\n```", "```sh\nmake b\n```").Findings
	if len(got) != 1 || got[0].Excerpt != "> ```" {
		t.Fatalf("quoted delimiter inside a fence: %v", got)
	}
}

// TestFloorNumbers pins #363: F9 is a set rule over numbers outside fenced code.
func TestFloorNumbers(t *testing.T) {
	before := "Measured 557.3 in 24 h over 1,819 requests (HISS-17, see `v1.2.0`, [doc](x-3.md)).\n" +
		"| p99 | 15 s |\n1. Drain node.\n```bash\nsleep 30\n```"
	kept := "| p99 | 15 s |\nMeasured 557.3 in 24h over 1,819 requests (HISS-17, `v1.2.0`, [doc](x-3.md)).\n" +
		"- Drain node.\n```bash\nsleep 30\n```"
	cases := map[string]struct {
		after string
		want  []Finding
	}{
		"kept and moved":     {kept, nil},
		"rounded":            {strings.Replace(before, "557.3", "557", 1), []Finding{{1, RuleFloorNumber, "557.3"}}},
		"table cell lost":    {strings.Replace(before, "15 s", "fast", 1), []Finding{{2, RuleFloorNumber, "15"}}},
		"id lost is F3 only": {strings.Replace(before, "HISS-17, ", "", 1), []Finding{{1, RuleFloorID, "HISS-17"}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Floor(before, tc.after).Findings; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findings\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}

// TestFloorNumbersBoundary covers what is not a number fact: digits glued to a word, ordered
// list markers, and numbers inside code spans, ids, link targets and URLs.
func TestFloorNumbersBoundary(t *testing.T) {
	before := "p99 sha256 x86 F9 (x3)\n2. Second item.\n`port 8080` ADR-0010 [a](b-4.md) https://example.invalid/5"
	if got := extractFacts(before).items[RuleFloorNumber]; len(got) != 0 {
		t.Fatalf("number facts %v, want none", got)
	}
	if got := extractFacts("1,024 and 3.0, then 7.").items[RuleFloorNumber]; len(got) != 3 {
		t.Fatalf("number facts %v, want 1,024, 3.0 and 7", got)
	}
}
