package caveman

import (
	"strings"
	"testing"
)

func TestCompressPositive(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"ansi colours":       {"\x1b[38;2;0;0;0mlefthook\x1b[m ok", "lefthook ok"},
		"osc title":          {"\x1b]0;title\x07run", "run"},
		"inner blank runs":   {"verdict:   pass,\t\tchanged:  none", "verdict: pass, changed: none"},
		"trailing blanks":    {"verdict: pass   \t", "verdict: pass"},
		"blank line runs":    {"a\n\n\n\n b", "a\n\n b"},
		"repeated lines":     {"sync ok\nsync ok\nsync ok\nnext", "sync ok (x3)\nnext"},
		"crlf":               {"a\r\nb\r\n", "a\nb\n"},
		"indent kept":        {"   - nested   item", "   - nested item"},
		"code span kept":     {"run `a   b`   now", "run `a   b` now"},
		"ansi inside fences": {"```\n\x1b[1mlog\x1b[m\n```", "```\nlog\n```"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got, _ := Compress(tc.in); got != tc.want {
				t.Fatalf("Compress(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	in := "\x1b[1mdone\x1b[m   now\n\n\n"
	out, stats := Compress(in)
	if stats.BytesIn != len(in) || stats.BytesOut != len(out) || stats.BytesOut >= stats.BytesIn {
		t.Errorf("stats = %+v for %q -> %q", stats, in, out)
	}
	if stats.TokensEstIn != EstimateTokens(in) || stats.TokensEstOut != EstimateTokens(out) {
		t.Errorf("token stats %+v must come from EstimateTokens", stats)
	}
}

// Nothing protected changes, and prose words are never dropped or replaced.
func TestCompressNegative(t *testing.T) {
	protected := strings.Join([]string{
		"```bash",
		"echo   a",
		"echo   a",
		"",
		"",
		"```",
		"| a   | b |",
		"| a   | b |",
		"PRAETOR_CHECKPOINT_RESULT=due  now",
		"PRAETOR_CHECKPOINT_RESULT=due  now",
		"## Heading   with  gaps",
		"<!-- marker   kept -->",
		OffMarker,
		"quoted   sample",
		"quoted   sample",
		OnMarker,
	}, "\n")
	if got, _ := Compress(protected); got != protected {
		t.Fatalf("protected text changed:\n%s", got)
	}
	prose := "The gate probably failed, so please note that the sync is just stale."
	if got, _ := Compress(prose); got != prose {
		t.Fatalf("prose was rewritten: %q", got)
	}
}

func TestCompressBoundary(t *testing.T) {
	if got, stats := Compress(""); got != "" || stats != (Stats{}) {
		t.Errorf("empty input: %q, %+v", got, stats)
	}
	// Two identical lines fold; lines that differ only in blanks fold after squeezing.
	if got, _ := Compress("a  b\na b"); got != "a b (x2)" {
		t.Errorf("squeezed repeat = %q", got)
	}
	// Blank lines between repeats end the run: nothing is folded across a paragraph.
	if got, _ := Compress("x\n\nx"); got != "x\n\nx" {
		t.Errorf("repeat across a blank line = %q", got)
	}
	// Compress is idempotent, so a second pass never compounds a fold.
	in := "\x1b[31mwarn\x1b[m\nwarn\nwarn\n\n\n\nnext   line  \n"
	once, _ := Compress(in)
	if twice, _ := Compress(once); twice != once {
		t.Errorf("not idempotent: %q then %q", once, twice)
	}
	// A line of pure escapes becomes blank and joins the blank run.
	if got, _ := Compress("a\n\x1b[2K\n\nb"); got != "a\n\nb" {
		t.Errorf("escape-only line = %q", got)
	}
}

// TestCompressKeepsSpansAndFrontMatter keeps the bytes the shared scan protects: a code span
// that wraps onto the next line (#320), a blockquoted fence (#356) and YAML front matter
// (#374), where collapsing blanks would change a quoted value.
func TestCompressKeepsSpansAndFrontMatter(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"wrapped span":  {"run `a   b\nc   d`   now", "run `a   b\nc   d` now"},
		"quoted fence":  {"> ```sh\n>   make   serve\n> ```", "> ```sh\n>   make   serve\n> ```"},
		"front matter":  {"---\nname: x\ndescription: \"a  b\"\n---\nbody   text", "---\nname: x\ndescription: \"a  b\"\n---\nbody text"},
		"not a mapping": {"---\nloose   prose\n---", "---\nloose prose\n---"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got, _ := Compress(tc.in); got != tc.want {
				t.Fatalf("Compress(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// compressCase is one Compress input and the text it must become; a case whose want is empty
// must come back byte for byte.
type compressCase struct{ in, want string }

// runCompressCases checks every case, and that the result is stable under a second pass and
// holds the clarity floor against its input: a kept byte must never cost a fact.
func runCompressCases(t *testing.T, cases map[string]compressCase) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			want := tc.want
			if want == "" {
				want = tc.in
			}
			got, _ := Compress(tc.in)
			if got != want {
				t.Fatalf("Compress(%q) = %q, want %q", tc.in, got, want)
			}
			if again, _ := Compress(got); again != got {
				t.Fatalf("not idempotent: %q then %q", got, again)
			}
			if report := Floor(StripANSI(tc.in), got); !report.Passed() {
				t.Fatalf("the result fails the clarity floor: %v", report.Findings)
			}
		})
	}
}

// TestCompressKeepsIndentedCode pins the review finding on #368: an indented code block keeps
// its bytes like a fenced one (CommonMark 0.31.2, 4.4). Compress used to read it as prose and
// collapse its blank runs, which changes what the code says.
func TestCompressKeepsIndentedCode(t *testing.T) {
	runCompressCases(t, map[string]compressCase{
		"after a blank line":      {in: "para\n\n    key:    value   # aligned\n"},
		"tab":                     {in: "para\n\n\tcol1\t\tcol3   x\n"},
		"blanks then tab":         {in: "para\n\n  \tcode   x\n"},
		"first line of the text":  {in: "    code   x\n"},
		"after a heading":         {in: "# H\n    code   x\n"},
		"after a fence":           {in: "```\nx\n```\n    code   x\n"},
		"trailing blanks":         {in: "    code   \n\ntext"},
		"repeated lines":          {in: "    x   y\n    x   y\n"},
		"interior blank lines":    {in: "    chunk1\n\n\n    chunk2\n"},
		"interior line of blanks": {in: "    chunk1\n      \n      chunk2\n"},
		"in a blockquote":         {in: ">     code   x\n>\n>\n>     more   y\n"},
		"quote marker as code":    {in: "para\n\n    > not   a   quote\n"},
		"in a list item":          {in: "- item\n\n      code   x\n"},
		"list item opens with it": {in: "-     code   x\n      more   y\n"},
		"ordered list item":       {in: "para\n1.     code   x\n"},
		"list item, tabs":         {in: "-\t\tcode   x\n"},
		"crlf and ansi go":        {in: "    \x1b[1mcode\x1b[m   x\r\n", want: "    code   x\n"},
		"paragraph after it":      {in: "    code   x\ntext   y", want: "    code   x\ntext y"},
	})
}

// TestCompressIndentedProseNegative: indentation alone does not make code. A line that
// continues a paragraph, one indented less than four columns and a list marker followed by up
// to four blanks are prose and are cleaned as before.
func TestCompressIndentedProseNegative(t *testing.T) {
	runCompressCases(t, map[string]compressCase{
		"continues a paragraph":  {in: "Foo\n    bar   baz", want: "Foo\n    bar baz"},
		"continues a list item":  {in: "- item\n    more   text", want: "- item\n    more text"},
		"continues a quote":      {in: "> quoted\n    lazy   line", want: "> quoted\n    lazy line"},
		"three columns":          {in: "para\n\n   three   columns", want: "para\n\n   three columns"},
		"marker and four blanks": {in: "-    four   blanks", want: "- four blanks"},
		"tab after a marker":     {in: "-\tone   tab", want: "-\tone tab"},
		"nested list":            {in: "- a\n    - b   c", want: "- a\n    - b c"},
		"blank run after block":  {in: "    code\n\n\n\ntext   x", want: "    code\n\ntext x"},
		"blank run before block": {in: "text\n\n\n\n    code   x", want: "text\n\n    code   x"},
		"blank line of blanks":   {in: "text\n   \t \n\nmore", want: "text\n\nmore"},
		"prose still folds":      {in: "ok\nok\n\n    x\n", want: "ok (x2)\n\n    x\n"},
	})
}

// TestCompressIndentedCodeBoundary: four columns are code and three are not, however the
// columns are made up; a block ends at the first line indented less.
func TestCompressIndentedCodeBoundary(t *testing.T) {
	for indent, code := range map[string]bool{
		"   ": false, "    ": true, "     ": true, "\t": true, " \t": true, "   \t": true, "  ": false,
	} {
		in := "para\n\n" + indent + "a   b"
		want := in
		if !code {
			want = "para\n\n" + indent + "a b"
		}
		if got, _ := Compress(in); got != want {
			t.Errorf("indentation %q: Compress = %q, want %q", indent, got, want)
		}
	}
	runCompressCases(t, map[string]compressCase{
		"block ends at prose":    {in: "    a   b\n\n    c   d\ntext   e\n\n\n    f   g", want: "    a   b\n\n    c   d\ntext e\n\n    f   g"},
		"only the text":          {in: "    a   b"},
		"no final newline":       {in: "x\n\n    a   b  "},
		"quote depth two":        {in: "> >     code   x\n"},
		"after an empty quote":   {in: "> text\n>\n>     code   x\n"},
		"marker and five blanks": {in: "-     five   blanks"},
	})
}

// TestCompressKeepsHardLineBreaks pins the second half of the finding: a hard line break (two
// or more trailing spaces, or a backslash, before a line that continues the block; CommonMark
// 0.31.2, 6.7) survives, and so do the blanks of a code span that wraps.
func TestCompressKeepsHardLineBreaks(t *testing.T) {
	runCompressCases(t, map[string]compressCase{
		"two spaces":             {in: "foo  \nbaz"},
		"more than two":          {in: "foo       \nbaz"},
		"tab before the spaces":  {in: "foo\t  \nbaz"},
		"backslash":              {in: "foo\\\nbaz"},
		"inner run still goes":   {in: "foo   bar  \nbaz   qux", want: "foo bar  \nbaz qux"},
		"in a list item":         {in: "- foo  \n  bar"},
		"in a quote":             {in: "> foo  \n> bar"},
		"never folded":           {in: "a  \na  \nb"},
		"backslash never folded": {in: "a\\\na\\\nb"},
		"break then repeat":      {in: "a  \na\na\n", want: "a  \na (x2)\n"},
		"wrapped span":           {in: "`code  \nspan`"},
		"wrapped span, one":      {in: "`code \nspan`"},
		"backslash and a blank":  {in: "foo\\ \nbar"},
		"backslash and blanks":   {in: "foo\\ \t\nbar", want: "foo\\ \nbar"},
	})
}

// TestCompressTrailingBlanksNegative: trailing blanks that are no hard line break still go.
// Neither form works at the end of a block, and one space or a tab is no break at all.
func TestCompressTrailingBlanksNegative(t *testing.T) {
	runCompressCases(t, map[string]compressCase{
		"end of a paragraph":  {in: "foo  \n\nbar", want: "foo\n\nbar"},
		"end of the text":     {in: "foo  ", want: "foo"},
		"final newline":       {in: "foo  \n", want: "foo\n"},
		"one space":           {in: "foo \nbar", want: "foo\nbar"},
		"ends in a tab":       {in: "foo \t\nbar", want: "foo\nbar"},
		"escaped backslash":   {in: "foo\\\\ \nbar", want: "foo\\\\\nbar"},
		"backslash at an end": {in: "foo\\ \n\nbar", want: "foo\\\n\nbar"},
		"plain repeat folds":  {in: "a\na\nb", want: "a (x2)\nb"},
	})
}

// TestCompressHardLineBreakBoundary: two spaces are the least that break, an odd run of
// backslashes ends in a break and an even one does not, and a break before a blank line is
// none.
func TestCompressHardLineBreakBoundary(t *testing.T) {
	runCompressCases(t, map[string]compressCase{
		"exactly two":           {in: "a  \nb"},
		"exactly one":           {in: "a \nb", want: "a\nb"},
		"three backslashes":     {in: "a\\\\\\\na\\\\\\\nb"},
		"two backslashes fold":  {in: "a\\\\\na\\\\\nb", want: "a\\\\ (x2)\nb"},
		"last line of the text": {in: "a  \nb  ", want: "a  \nb"},
		"backslash, last line":  {in: "a\\\na\\", want: "a\\\na\\"},
		"before a heading":      {in: "a  \n# H"},
		"only blanks follow":    {in: "a  \n   \nb", want: "a\n\nb"},
	})
}

// TestCompressKeepsInlineValues: blanks inside an inline HTML tag, the target and title of an
// inline link and a double-quoted string are a value, not layout, and stay; the blank runs
// around them still collapse. Positive, negative and boundary cases in turn.
func TestCompressKeepsInlineValues(t *testing.T) {
	runCompressCases(t, map[string]compressCase{
		"attribute value":      {in: `<a title="a  b">x</a>   and`, want: `<a title="a  b">x</a> and`},
		"single-quoted value":  {in: `<img alt='a  b'   src=x>   end`, want: `<img alt='a  b'   src=x> end`},
		"inline comment":       {in: `text   <!-- a  b -->   more`, want: `text <!-- a  b --> more`},
		"link title":           {in: `[l](u "t  t")   end`, want: `[l](u "t  t") end`},
		"single-quoted title":  {in: `see   [l](u 't  t')`, want: `see [l](u 't  t')`},
		"reference definition": {in: `[ref]: /u "t  t"`},
		"definition, quoted":   {in: "[ref]: /u 't  t'   \n\nnext", want: "[ref]: /u 't  t'\n\nnext"},
		"definition, parens":   {in: `   [ref]:   /u   (t  t)`},
		"title in parentheses": {in: `[l](u (t  t))   end`, want: `[l](u (t  t)) end`},
		"target with parens":   {in: `[l](u_(x) 't  t')   end`, want: `[l](u_(x) 't  t') end`},
		"single-quoted, alone": {in: `  title='a  b'   x`, want: `  title='a  b' x`},
		"quoted string":        {in: `say "a  b"   now`, want: `say "a  b" now`},
		"two quoted strings":   {in: `"a  b"   "c  d"`, want: `"a  b" "c  d"`},

		"comparison, no tag":    {in: `a < b   and   c > d`, want: `a < b and c > d`},
		"one quote character":   {in: `5"   pipe`, want: `5" pipe`},
		"bracket, no link":      {in: `[l]   (not  a  link)   x`, want: `[l] (not a link) x`},
		"label, no definition":  {in: `[note] :   a   b`, want: `[note] : a b`},
		"definition, indented":  {in: "p\n    [ref]:   /u   x", want: "p\n    [ref]: /u x"},
		"single quotes, prose":  {in: `don't   say   it's`, want: `don't say it's`},
		"plain prose":           {in: `x   y`, want: `x y`},
		"empty quoted string":   {in: `""   x`, want: `"" x`},
		"quote never closed":    {in: `"open   quote`, want: `"open quote`},
		"tag never closed":      {in: `<a   title=x`, want: `<a title=x`},
		"quote closes far away": {in: `a "b   c   d" e   f`, want: `a "b   c   d" e f`},
	})
}

// TestStripANSI: the stripper removes the escapes Compress removes and nothing else, so a
// Floor comparison against its result judges the text rather than escape parameters. Positive:
// CSI, OSC and two-byte escapes go, and the digits of their parameters with them. Negative:
// text without an escape character keeps every byte, bracketed digits included, and no other
// Compress cleanup runs. Boundary: empty text, an escape alone and an escape that never ends.
//
// Its last assertions are the tripwire of #713 (Floor F9 reads escape parameter digits as
// numbers): they fail once that defect is fixed, and StripANSI's caller in
// cmd/standardsctl/caveman_compress.go can then compare against the input as it is.
func TestStripANSI(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"csi colour":         {"\x1b[38;5;196mred 7\x1b[0m", "red 7"},
		"osc title":          {"\x1b]0;build 42\x07run", "run"},
		"osc string end":     {"\x1b]8;;https://example.test\x1b\\link\x1b]8;;\x1b\\", "link"},
		"two-byte escape":    {"a\x1bMb", "ab"},
		"no escape":          {"[31m is no escape, 196 stays", "[31m is no escape, 196 stays"},
		"other cleanups off": {"a  b  \r\n\n\n\nc\nc", "a  b  \r\n\n\n\nc\nc"},
		"empty":              {"", ""},
		"escape alone":       {"\x1b", "\x1b"},
		"unterminated csi":   {"\x1b[38;5", "\x1b[38;5"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := StripANSI(tc.in); got != tc.want {
				t.Fatalf("StripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	// Floor reads an escape parameter as a number; against the stripped text nothing is lost.
	coloured := "\x1b[38;5;196msync ok\x1b[0m\n"
	compressed, _ := Compress(coloured)
	if Floor(coloured, compressed).Passed() {
		t.Fatal("Floor no longer reads escape parameters as numbers; StripANSI's reason is gone")
	}
	if report := Floor(StripANSI(coloured), compressed); !report.Passed() {
		t.Fatalf("stripped text must hold the floor: %v", report.Findings)
	}
}
