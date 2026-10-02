package caveman

import "testing"

// runStripANSI replays a table of inputs through the one ANSI reader of the package.
func runStripANSI(t *testing.T, cases map[string]struct{ in, want string }) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := stripANSI(tc.in); got != tc.want {
				t.Fatalf("stripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A terminated sequence goes, zero-width, and the text around it joins.
func TestStripANSIPositive(t *testing.T) {
	runStripANSI(t, map[string]struct{ in, want string }{
		"csi colour":             {"\x1b[38;5;196mred 7\x1b[0m", "red 7"},
		"csi inside a number":    {"20\x1b[m\x1b[K26", "2026"},
		"csi private parameter":  {"\x1b[?25lbusy\x1b[?25h", "busy"},
		"csi intermediate blank": {"\x1b[2 qcursor", "cursor"},
		"osc ended by bel":       {"\x1b]0;build 42\x07run", "run"},
		"osc ended by st":        {"see \x1b]8;;https://example.test/1\x1b\\link\x1b]8;;\x1b\\", "see link"},
		"osc on each line":       {"\x1b]0;a\x07one\n\x1b]0;b\x07two", "one\ntwo"},
		"two-byte escape":        {"a\x1bMb", "ab"},
	})
}

// What is no sequence stays text: an escape left unterminated on its line, and above all the
// lines between an OSC and a terminator further down (#713). With an OSC body that crosses
// line ends, each of the later-line cases returns "log  end".
func TestStripANSINegative(t *testing.T) {
	runStripANSI(t, map[string]struct{ in, want string }{
		"no escape":                    {"[31m is no escape, 196 stays", "[31m is no escape, 196 stays"},
		"other cleanups untouched":     {"a  b  \r\n\n\n\nc\nc", "a  b  \r\n\n\n\nc\nc"},
		"csi without final byte":       {"cut \x1b[38;5\nnext m", "cut \x1b[38;5\nnext m"},
		"csi cut before a digit":       {"x \x1b[38;5 42 MUST y", "x \x1b[38;5 42 MUST y"},
		"osc without terminator":       {"\x1b]0;build 42", "0;build 42"},
		"osc ended by bel a line down": {"log \x1b]0;title\nrule HISS-17\ndone\x07 end", "log 0;title\nrule HISS-17\ndone\x07 end"},
		"osc ended by st a line down":  {"log \x1b]0;title\nrule HISS-17\ndone\x1b\\ end", "log 0;title\nrule HISS-17\ndone end"},
		"osc ended by bel after crlf":  {"log \x1b]0;title\r\nrule 42\r\ndone\x07 end\r\n", "log 0;title\r\nrule 42\r\ndone\x07 end\r\n"},
	})
}

// The edges of each form: nothing to read, an escape with nothing after it, an OSC that ends
// on the last byte of its line or one byte past it, and a CSI whose final byte is a letter of
// the next word (ECMA-48 reads a blank as an intermediate byte).
func TestStripANSIBoundary(t *testing.T) {
	runStripANSI(t, map[string]struct{ in, want string }{
		"empty":                          {"", ""},
		"escape alone":                   {"\x1b", "\x1b"},
		"escape before a line end":       {"a\x1b\nb", "a\x1b\nb"},
		"osc with empty body":            {"\x1b]\x07", ""},
		"osc ended by last byte of line": {"\x1b]0;t\x07\nnext", "\nnext"},
		"osc ended by first byte below":  {"\x1b]0;t\n\x07next", "0;t\n\x07next"},
		"osc opened inside an open osc":  {"\x1b]0;a\x1b]0;b\x07c", "0;ac"},
		"csi cut before blank, letter":   {"x \x1b[38;5 MUST y 9", "x UST y 9"},
	})
}
