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

// TestCompressUnterminatedEscapes pins the reader Compress shares with Floor (stripANSI): an
// escape left unterminated on its line stays text, and so does every line below it. An OSC
// body used to cross line ends, so Compress deleted the lines down to the next BEL or ST and
// each case that ends a line down came out as "log end\n" (#713).
func TestCompressUnterminatedEscapes(t *testing.T) {
	between := "\nrule HISS-17 MUST hold 42 items\nnever drop `foo`\ndone"
	cases := map[string]struct{ in, want string }{
		"osc ended by bel a line down": {floorOSCText("\x07"), "log 0;title" + between + "\x07 end\n"},
		"osc ended by st a line down":  {floorOSCText("\x1b\\"), "log 0;title" + between + " end\n"},
		"osc ended by bel after crlf":  {"log \x1b]0;title\r\nrule 42\r\ndone\x07 end\r\n", "log 0;title\nrule 42\ndone\x07 end\n"},
		"osc ended on its line":        {"log \x1b]0;title\x07\nrule 42\n", "log\nrule 42\n"},
		"osc without terminator":       {"log \x1b]0;build 42\nnext\n", "log 0;build 42\nnext\n"},
		"csi without final byte":       {"cut \x1b[38;5\nnext m\n", "cut \x1b[38;5\nnext m\n"},
		"csi cut before blank, letter": {"x \x1b[38;5 MUST y 9\n", "x UST y 9\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got, _ := Compress(tc.in); got != tc.want {
				t.Fatalf("Compress(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
