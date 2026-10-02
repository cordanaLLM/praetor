package caveman

import (
	"maps"
	"reflect"
	"slices"
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

// TestFloorCommandFences pins #322: F2 reads shell fences only, a session after its prompt;
// a source or data fence holds its code tokens (F10) and numbers (F9) outside comments.
func TestFloorCommandFences(t *testing.T) {
	cases := map[string]struct {
		before, after string
		want          []Finding
	}{
		"go comment corrected": {"```go\nrun() // old\n```", "```go\nrun() // new\n```", nil},
		"go code reformatted":  {"```go\nrun( a,b )\n```", "```go\nrun(a, b)\n```", nil},
		"go call renamed": {"```go\nrun() // note\n```", "```go\nstart() // note\n```",
			[]Finding{{2, RuleFloorCodeWord, "run"}}},
		"yaml comment corrected": {"```yaml\nport: 1 # old\n```", "```yml\nport: 1 # new\n```", nil},
		"yaml value changed": {"```yaml\nport: 1\n```", "```yaml\nport: 2\n```",
			[]Finding{{2, RuleFloorNumber, "1"}}},
		"yaml entry commented out": {"```yaml\nretries: 5\n```", "```yaml\n# retries: 5\n```",
			[]Finding{{2, RuleFloorCodeWord, "retries"}, {2, RuleFloorNumber, "5"}}},
		"json key dropped": {"```json\n{\"a\": true, \"b\": true}\n```", "```json\n{\"a\": true}\n```",
			[]Finding{{2, RuleFloorCodeWord, "b"}}},
		"text output changed": {"```text\nok 1\n```", "```text\nok 2\n```",
			[]Finding{{2, RuleFloorNumber, "1"}}},
		"mermaid label changed":         {"```mermaid\nA[old 1] --> B\n```", "```mermaid\nA[new] --> B\n```", nil},
		"source moved into a code span": {"```go\nrun(5)\n```", "Call `run(5)`.", nil},
		"script edited": {"```bash\nmake serve\n```", "```bash\nmake run\n```",
			[]Finding{{2, RuleFloorCommand, "make serve"}}},
		"session prompt dropped into script": {"```console\n$ make check\nok\n```", "```sh\nmake check\n```\nPrints `ok`.", nil},
		"session output reformatted": {"```console\n$ go test ./...\nok  pkg 0.5s\n```",
			"```console\n$ go test ./...\nok    pkg    0.5s\n```", nil},
		"session comment corrected": {"```console\n# run the gate\n$ make check\n```",
			"```console\n# run every gate\n$ make check\n```", nil},
		"session output count changed": {"```console\n$ make check\n[PASS] 58 claims\n```",
			"```console\n$ make check\n[PASS] 57 claims\n```", []Finding{{3, RuleFloorNumber, "58"}}},
		"session output dropped": {"```console\n$ make check\n[PASS] 58 claims\n```", "```console\n$ make check\n```",
			[]Finding{{3, RuleFloorCodeWord, "PASS"}, {3, RuleFloorCodeWord, "claims"}, {3, RuleFloorNumber, "58"}}},
		"session command edited": {"```console\n$ make check\nok\n```", "```console\n$ make test\nok\n```",
			[]Finding{{2, RuleFloorCommand, "make check"}}},
		"unlabeled fence edited": {"```\nmake lint\n```", "```\nmake vet\n```",
			[]Finding{{2, RuleFloorCommand, "make lint"}}},
		"continuation dropped": {"```console\n$ make check \\\n  --verbose\n```", "```console\n$ make check \\\n```",
			[]Finding{{3, RuleFloorCommand, "--verbose"}}},
		"powershell edited": {"```powershell\nGet-Item x\n```", "```powershell\nGet-Item y\n```",
			[]Finding{{2, RuleFloorCommand, "Get-Item x"}}},
		"go block comment corrected": {"```go\nrun() /* old */\n```", "```go\nrun() /* new */\n```", nil},
		"go comment across lines":    {"```go\n/* old\n   note */\nrun()\n```", "```go\n/* new\n   text */\nrun()\n```", nil},
		"golang alias comment":       {"```golang\nrun() // old\n```", "```golang\nrun() // new\n```", nil},
		"html comment corrected":     {"```html\n<p>1</p> <!-- old -->\n```", "```xml\n<p>1</p> <!-- new -->\n```", nil},
		"yaml quoted value changed": {"```yaml\nmsg: \"a # b\"\n```", "```yaml\nmsg: \"a # c\"\n```",
			[]Finding{{2, RuleFloorCodeWord, "b"}}},
		"batch comment corrected": {"```cmd\nREM old\nset A=1\n```", "```bat\n:: new\nset A=1\n```", nil},
		"batch command edited": {"```cmd\nset A=1\n```", "```cmd\nset A=2\n```",
			[]Finding{{2, RuleFloorCommand, "set A=1"}}},
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

// TestFloorMarkdownLabels pins that the labels of Markdown syntax are no number facts:
// ordered list markers, also quoted, heading section numbers, reference and footnote labels
// and HTML entities. A number a reader sees in the new text, a list marker included, keeps
// the number of the old one.
func TestFloorMarkdownLabels(t *testing.T) {
	cases := map[string]struct {
		before, after string
		want          []Finding
	}{
		"quoted ordered list":  {"> 1. a\n> 2. b", "> - a\n> - b", nil},
		"reference link":       {"See [docs][1].\n\n[1]: https://example.invalid/d", "See [docs](https://example.invalid/d).", nil},
		"footnote":             {"Noted.[^1]\n\n[^1]: Source.", "Noted (source).", nil},
		"heading number":       {"## 3. Setup\n### 3.1 Install", "## Setup\n### Install", nil},
		"entity":               {"Wait&#8212;then retry.", "Wait—then retry.", nil},
		"steps become a list":  {"Step 1: drain. Step 2: restart.", "1. Drain.\n2. Restart.", nil},
		"footnote value lost":  {"Noted.[^1]\n\n[^1]: Keep 2.", "Noted (keep some).", []Finding{{3, RuleFloorNumber, "2"}}},
		"heading count lost":   {"## 404 errors", "## Missing pages", []Finding{{1, RuleFloorNumber, "404"}}},
		"quoted item value":    {"> 1. Keep 3 replicas.", "> - Keep replicas.", []Finding{{1, RuleFloorNumber, "3"}}},
		"entity next to value": {"Wait 30&nbsp;s.", "Wait a while.", []Finding{{1, RuleFloorNumber, "30"}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Floor(tc.before, tc.after).Findings; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findings\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}

// TestFloorNumbersBoundary covers what a number fact is: a word that holds a digit is one
// fact, whole, dotted, hyphenated or signed, a number with a unit carries the number alone,
// and ordered list markers and numbers inside code spans, ids, link targets and URLs are no
// facts.
func TestFloorNumbersBoundary(t *testing.T) {
	before := "p99 sha256 x86 F9 (x3) v8.6.0 p99.94 go1.27 x1,5 utf-8 x86-64 -5 24h 64-bit\n2. Second item.\n" +
		"`port 8080` ADR-0010 [a](b-4.md) https://example.invalid/5"
	want := []string{"-5", "24", "64", "F9", "go1.27", "p99", "p99.94", "sha256", "utf-8", "v8.6.0", "x1,5", "x3", "x86", "x86-64"}
	if got := slices.Sorted(maps.Keys(extractFacts(before).items[RuleFloorNumber])); !reflect.DeepEqual(got, want) {
		t.Fatalf("number facts\n got %v\nwant %v", got, want)
	}
	if got := slices.Sorted(maps.Keys(extractFacts("1,024 and 3.0, then 7. (.5) 1..9").items[RuleFloorNumber])); !reflect.DeepEqual(got,
		[]string{"1", "1,024", "3.0", "5", "7", "9"}) {
		t.Fatalf("number facts %v, want 1,024, 3.0, 7, 5, 1 and 9", got)
	}
	// Backticking a dotted token or a bare number loses no number.
	if got := Floor("Pin v8.6.0, alert at p99.94, built with go1.27.", "Pin `v8.6.0`, `p99.94`, `go1.27`.").Findings; len(got) != 0 {
		t.Fatalf("dotted token moved into code lost: %v", got)
	}
	if got := Floor("Limit is 60 lines.", "Limit: `60` lines.").Findings; len(got) != 0 {
		t.Fatalf("number moved into a code span lost: %v", got)
	}
	// Changing a version, a percentile or a sign fails; the digits of a word are not loose
	// numbers another word can keep.
	changes := map[string][2]string{
		"version":    {"Pin v8.6.0.", "Pin v8.7.0 (6.0 is gone)."},
		"percentile": {"Alert at p99.94.", "Alert at p99.9 (94)."},
		"sign":       {"Offset -5.", "Offset 5."},
		"dropped":    {"Alert at p99.94.", "Alert at the tail."},
	}
	for name, pair := range changes {
		if got := Floor(pair[0], pair[1]).Findings; len(got) != 1 || got[0].Rule != RuleFloorNumber {
			t.Errorf("%s: findings %v, want one F9", name, got)
		}
	}
}

// TestFloorNumbersMoveIntoCode pins where a number may move: into a code span, a command or
// the code of a source fence keeps it; into a comment, where no reader runs it, does not.
func TestFloorNumbersMoveIntoCode(t *testing.T) {
	cases := map[string]struct {
		after string
		want  []Finding
	}{
		"code span":      {"Wait `30` s.", nil},
		"shell command":  {"```sh\nsleep 30\n```", nil},
		"session prompt": {"```console\n$ sleep 30\n```", nil},
		"yaml value":     {"```yaml\nwait: 30\n```", nil},
		"yaml comment":   {"```yaml\nwait: 0 # 30\n```", []Finding{{1, RuleFloorNumber, "30"}}},
		"shell comment":  {"```sh\n# 30\n```", []Finding{{1, RuleFloorNumber, "30"}}},
		"html comment":   {"Wait. <!-- 30 -->", []Finding{{1, RuleFloorNumber, "30"}}},
		"dropped":        {"Wait.", []Finding{{1, RuleFloorNumber, "30"}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Floor("Wait 30 s.", tc.after).Findings; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findings\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}

// TestFloorANSIEscapesPositive pins #713: ANSI escape sequences (CSI, OSC and two-byte escapes)
// are ignored on both sides before collecting number facts, so a rewrite removing colour codes
// passes the clarity floor.
func TestFloorANSIEscapesPositive(t *testing.T) {
	cases := map[string]struct {
		before, after string
	}{
		"csi color removed": {
			before: "a \x1b[31mMUST\x1b[0m b\n",
			after:  "a MUST b\n",
		},
		"csi 256 color with real number kept": {
			before: "result: \x1b[38;5;196mred 7\x1b[0m\n",
			after:  "result: red 7\n",
		},
		"osc title removed": {
			before: "\x1b]0;build 42\x07run\n",
			after:  "run\n",
		},
		"osc hyperlink removed": {
			before: "see \x1b]8;;https://example.test/1\x1b\\link\x1b]8;;\x1b\\\n",
			after:  "see link\n",
		},
		"two-byte escape removed": {
			before: "a\x1bMb\n",
			after:  "ab\n",
		},
		"both sides have escapes": {
			before: "\x1b[31mred 7\x1b[0m\n",
			after:  "\x1b[32mgreen 7\x1b[0m\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if report := Floor(tc.before, tc.after); !report.Passed() {
				t.Fatalf("want floor held, got findings: %v", report.Findings)
			}
		})
	}
}

// TestFloorANSIEscapesNegative pins #713: a real number lost beside or inside an escape
// sequence still fails with F9 number-lost.
func TestFloorANSIEscapesNegative(t *testing.T) {
	cases := map[string]struct {
		before, after string
		want          []Finding
	}{
		"real number lost beside escape": {
			before: "a \x1b[31mred 7\x1b[0m b\n",
			after:  "a red b\n",
			want:   []Finding{{1, RuleFloorNumber, "7"}},
		},
		"real number inside escape lost": {
			before: "status: \x1b[38;5;196m42\x1b[0m\n",
			after:  "status: ok\n",
			want:   []Finding{{1, RuleFloorNumber, "42"}},
		},
		"multiple numbers with one lost beside escape": {
			before: "count: 1 and \x1b[31m2\x1b[0m\n",
			after:  "count: 1\n",
			want:   []Finding{{1, RuleFloorNumber, "2"}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Floor(tc.before, tc.after).Findings; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findings\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}

// TestFloorANSIEscapesBoundary pins #713: unterminated escapes stay text and their parameter
// digits still count as numbers.
func TestFloorANSIEscapesBoundary(t *testing.T) {
	cases := map[string]struct {
		before, after string
		want          []Finding
	}{
		"unterminated csi digits lost": {
			before: "unterminated \x1b[38;5\n",
			after:  "unterminated\n",
			want:   []Finding{{1, RuleFloorNumber, "38"}, {1, RuleFloorNumber, "5"}},
		},
		"unterminated osc digits lost": {
			before: "unterminated \x1b]0;build 42\n",
			after:  "unterminated\n",
			want:   []Finding{{1, RuleFloorNumber, "0"}, {1, RuleFloorNumber, "42"}},
		},
		"unterminated escape kept": {
			before: "unterminated \x1b[38;5\n",
			after:  "unterminated \x1b[38;5\n",
			want:   nil,
		},
		"escape character alone": {
			before: "raw \x1b alone\n",
			after:  "raw \x1b alone\n",
			want:   nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Floor(tc.before, tc.after).Findings; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findings\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}
