package util

import (
	"errors"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// The shim of #538: an npm pnpm.cmd under a profile directory holding '&' and no space, which
// os/exec left unquoted, so cmd.exe split the command at the ampersand. Here the script path
// is quoted, a caret range is quoted instead of caret-escaped, and a plain word stays bare.
func TestBatchCommandLine_Positive_QuotesScriptAndMetacharacterArguments(t *testing.T) {
	script := `C:\Users\Tom&Jerry\AppData\Roaming\npm\pnpm.cmd`
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"update", "lib@^5.7.3"},
			`cmd.exe /d /e:on /v:off /s /c ""C:\Users\Tom&Jerry\AppData\Roaming\npm\pnpm.cmd" update "lib@^5.7.3""`},
		{[]string{"a&b", "(x)", "a|b", "<in>", "x y", "!USERNAME!", ",;=", "^^"},
			`cmd.exe /d /e:on /v:off /s /c ""C:\Users\Tom&Jerry\AppData\Roaming\npm\pnpm.cmd" "a&b" "(x)" "a|b" "<in>" "x y" "!USERNAME!" ",;=" "^^""`},
	}
	for _, tc := range cases {
		got, err := batchCommandLine(script, tc.args)
		if err != nil || got != tc.want {
			t.Errorf("batchCommandLine(%q) = %q, %v\nwant %q", tc.args, got, err, tc.want)
		}
	}
}

// What no quoting carries through cmd.exe unchanged is refused with ErrBatchFileArgument, and
// no command line is returned: a quote, a line break or NUL in an argument or the script path,
// an empty script path, and one ending in a separator.
func TestBatchCommandLine_Negative_RefusesWhatCmdCannotCarry(t *testing.T) {
	const script = `C:\shims\pnpm.cmd`
	for _, arg := range []string{`lib"@1.0.0`, "a\rb", "a\nb", "a\x00b", `"`} {
		if got, err := batchCommandLine(script, []string{"update", arg}); !errors.Is(err, ErrBatchFileArgument) || got != "" {
			t.Errorf("argument %q: batchCommandLine = %q, %v; want a refusal", arg, got, err)
		}
	}
	for _, bad := range []string{"", `C:\shims\`, "C:/shims/", `C:\a"b\pnpm.cmd`, "C:\\shims\npnpm.cmd", "C:\\sh\x00ims\\pnpm.cmd"} {
		if got, err := batchCommandLine(bad, []string{"update"}); !errors.Is(err, ErrBatchFileArgument) || got != "" {
			t.Errorf("script %q: batchCommandLine = %q, %v; want a refusal", bad, got, err)
		}
	}
}

// The edges of the quoting: no arguments, an empty argument, trailing backslashes doubled only
// inside quotes, a backslash inside an unquoted argument, every percent sign guarded (script
// path included), the unquoted punctuation and non-ASCII letters bare, a tab quoted.
func TestBatchCommandLine_Boundary_QuotingEdges(t *testing.T) {
	const prefix = `cmd.exe /d /e:on /v:off /s /c ""C:\x\a.bat"`
	cases := []struct {
		name   string
		script string
		args   []string
		want   string
	}{
		{"no arguments", `C:\x\a.bat`, nil, prefix + `"`},
		{"empty argument", `C:\x\a.bat`, []string{""}, prefix + ` """`},
		{"trailing backslash", `C:\x\a.bat`, []string{`C:\dir\`}, prefix + ` "C:\dir\\""`},
		{"two trailing backslashes", `C:\x\a.bat`, []string{`a b\\`}, prefix + ` "a b\\\\""`},
		{"inner backslash", `C:\x\a.bat`, []string{`a\b`}, prefix + ` a\b"`},
		{"percent", `C:\x\a.bat`, []string{"%PATH%"}, prefix + ` "%%cd:~,%PATH%%cd:~,%""`},
		{"percent in script", `C:\100%\a.bat`, nil, `cmd.exe /d /e:on /v:off /s /c ""C:\100%%cd:~,%\a.bat""`},
		{"unquoted set", `C:\x\a.bat`, []string{`#$*+-./:?@\_`, "Zé9"}, prefix + ` #$*+-./:?@\_ Zé9"`},
		{"tab", `C:\x\a.bat`, []string{"a\tb"}, prefix + " \"a\tb\"\""},
		{"tilde", `C:\x\a.bat`, []string{"lib@~1.0.0"}, prefix + ` "lib@~1.0.0""`},
	}
	for _, tc := range cases {
		got, err := batchCommandLine(tc.script, tc.args)
		if err != nil || got != tc.want {
			t.Errorf("%s: batchCommandLine = %q, %v\nwant %q", tc.name, got, err, tc.want)
		}
	}
}

// Whatever an argument holds, no character cmd.exe gives a meaning to is left outside quotes,
// and no '%' is left unguarded, so neither the first parse nor any re-read of the quoted text
// in the batch file can act on it.
func TestBatchCommandLine_Boundary_NoMetacharacterOutsideQuotes(t *testing.T) {
	hostile := []string{"a&b", "a|b", "a<b", "a>b", "(a)", "a^b", "a!b", "a%b", "a,b", "a;b", "a=b",
		"a b", "a\tb", "a~b", "a`b", "a'b", "a[b]", "a{b}", "%%", "%cd:~,%", `\\server\share\`, "é^&"}
	for _, arg := range hostile {
		line, err := batchCommandLine(`C:\s&t\x.cmd`, []string{arg})
		if err != nil {
			t.Fatalf("%q: %v", arg, err)
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(line, `cmd.exe /d /e:on /v:off /s /c "`), `"`)
		if bare := unquotedMetacharacter(inner); bare != "" {
			t.Errorf("%q: %q leaves %q outside quotes", arg, line, bare)
		}
		if strings.Count(inner, "%") != 3*strings.Count(arg, "%") {
			t.Errorf("%q: %q does not guard every percent sign", arg, line)
		}
	}
}

// unquotedMetacharacter returns the first character of text outside cmd.exe quotes that is
// neither a separating space nor one batchNeedsQuotes lets stand bare, or "".
func unquotedMetacharacter(text string) string {
	quoted := false
	for _, r := range text {
		switch {
		case r == '"':
			quoted = !quoted
		case quoted, r == ' ', r >= utf8.RuneSelf, unicode.IsLetter(r), unicode.IsDigit(r), strings.ContainsRune(batchUnquoted, r):
		default:
			return string(r)
		}
	}
	return ""
}

// Batch files are the .bat and .cmd extensions in any case, nothing else.
func TestIsBatchFile_PositiveNegativeBoundary(t *testing.T) {
	for _, name := range []string{"pnpm.cmd", "PNPM.CMD", "x.Bat", "a.b.bat"} {
		if !isBatchFile(name) {
			t.Errorf("%q is a batch file", name)
		}
	}
	for _, name := range []string{"pnpm", "pnpm.exe", "pnpm.cmdx", "x.bat.exe", ".bat.ps1", ""} {
		if isBatchFile(name) {
			t.Errorf("%q is not a batch file", name)
		}
	}
}
