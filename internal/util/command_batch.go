package util

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrBatchFileArgument refuses a batch-file command whose script path or argument cannot reach
// the batch file, and the program it forwards to, unchanged through cmd.exe.
var ErrBatchFileArgument = errors.New("util: value cannot pass through cmd.exe to a batch file unchanged")

// batchCommandPrefix starts every batch-file command line (batchCommandLine). /d skips the
// AutoRun commands the registry may add, /e:on enables the command extensions the percent guard
// needs (npm shims need them too, for %~dp0), /v:off turns delayed expansion off so a '!' stays
// literal, and /s /c removes exactly the outer pair of quotes around the rest and runs it.
const batchCommandPrefix = `cmd.exe /d /e:on /v:off /s /c "`

// batchPercentGuard is written before every '%'. cmd.exe expands %NAME% and %NAME:...% on the
// command line even inside quotes, and a caret cannot escape it there. Written as %%cd:~,% the
// first '%' stays literal (an empty name) and %cd:~,% expands to an empty substring of the
// built-in cd variable, so the text reads back as one '%' and no name can form across it. It
// needs the command extensions /e:on enables. Rust's standard library writes the same guard
// for batch-file arguments (library/std/src/sys/args/windows.rs, append_bat_arg).
const batchPercentGuard = "%%cd:~,"

// batchUnquoted lists the ASCII punctuation a batch-file argument may hold without quotes. A
// backslash is safe unquoted: only a quote, which is refused, gives it meaning.
const batchUnquoted = `#$*+-./:?@\_`

// isBatchFile reports whether path names a Windows batch file, which CreateProcess starts
// through cmd.exe: its extension is .bat or .cmd in any case.
func isBatchFile(path string) bool {
	ext := filepath.Ext(path)
	return strings.EqualFold(ext, ".bat") || strings.EqualFold(ext, ".cmd")
}

// batchCommandLine returns the whole command line that runs the batch file script with args
// through cmd.exe, for SysProcAttr.CmdLine. os/exec quotes arguments for CommandLineToArgvW,
// which cmd.exe does not use: Go quotes only for a space, a tab or a quote, so a caret, an
// ampersand or a parenthesis in the script path or an argument reached cmd.exe bare (#508,
// #538). The os/exec documentation names batch files as the case for building CmdLine yourself.
//
// The script path is always quoted and every argument that holds anything but letters, digits,
// other non-ASCII characters and batchUnquoted is quoted too, so cmd.exe reads each one as
// literal text. A quoted region stays literal however often cmd.exe parses it, so the result
// does not depend on how many times the batch file re-reads its arguments: an npm or corepack
// shim forwarding %* to node and a batch file passing %1 or "%~1" on hand the program the
// same argv (TestRunCommandBytes_Positive_BatchFileForwardsArgvUnchanged). Two constructs
// still change them, and only inside the batch file: CALL, which doubles every caret, and
// delayed expansion enabled there, which expands !NAME!.
//
// Refused with ErrBatchFileArgument: an empty script, a script path ending in a separator, and
// a script path or argument holding a quote, a carriage return, a line feed or NUL. A line
// break ends the command, cmd.exe toggles quoting at every quote, and the program the batch
// file forwards to may read a doubled quote either as a quote or as the end of the argument
// (os.commandLineToArgv follows the pre-2008 rule, the current C runtime the later one).
func batchCommandLine(script string, args []string) (string, error) {
	if err := checkBatchScript(script); err != nil {
		return "", err
	}
	var line strings.Builder
	line.WriteString(batchCommandPrefix)
	line.WriteByte('"')
	writeBatchText(&line, script)
	line.WriteByte('"')
	for index, arg := range args {
		if reason := batchValueDefect(arg); reason != "" {
			return "", fmt.Errorf("%w: argument %d %q holds %s", ErrBatchFileArgument, index+1, arg, reason)
		}
		line.WriteByte(' ')
		writeBatchArgument(&line, arg)
	}
	line.WriteByte('"')
	return line.String(), nil
}

// checkBatchScript refuses a script path batchCommandLine cannot quote: an empty one, one
// ending in a separator, and one holding a character batchValueDefect names.
func checkBatchScript(script string) error {
	if script == "" {
		return fmt.Errorf("%w: empty batch file path", ErrBatchFileArgument)
	}
	if strings.HasSuffix(script, `\`) || strings.HasSuffix(script, "/") {
		return fmt.Errorf("%w: batch file path %q ends in a separator", ErrBatchFileArgument, script)
	}
	if reason := batchValueDefect(script); reason != "" {
		return fmt.Errorf("%w: batch file path %q holds %s", ErrBatchFileArgument, script, reason)
	}
	return nil
}

// batchValueDefect names the first character of value that no quoting carries through cmd.exe
// unchanged, or returns "" when there is none.
func batchValueDefect(value string) string {
	index := strings.IndexAny(value, "\"\r\n\x00")
	if index < 0 {
		return ""
	}
	return fmt.Sprintf("%q at byte %d", value[index:index+1], index)
}

// writeBatchArgument writes arg as one argument, quoted when batchNeedsQuotes says so. Inside
// the quotes the backslashes that end the argument are doubled, because CommandLineToArgvW
// and the C runtime read 2n backslashes before a quote as n and n before it as an escape.
func writeBatchArgument(line *strings.Builder, arg string) {
	if !batchNeedsQuotes(arg) {
		writeBatchText(line, arg)
		return
	}
	line.WriteByte('"')
	writeBatchText(line, arg)
	line.WriteString(strings.Repeat(`\`, len(arg)-len(strings.TrimRight(arg, `\`))))
	line.WriteByte('"')
}

// batchNeedsQuotes reports whether arg must be quoted: it is empty (unquoted it would vanish),
// it ends in a backslash (so "%~1" re-quoting in the batch file cannot escape the closing
// quote), or it holds a control character or ASCII punctuation outside batchUnquoted.
func batchNeedsQuotes(arg string) bool {
	if arg == "" || strings.HasSuffix(arg, `\`) {
		return true
	}
	return strings.IndexFunc(arg, func(r rune) bool {
		if unicode.IsControl(r) {
			return true
		}
		return r < utf8.RuneSelf && !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(batchUnquoted, r)
	}) >= 0
}

// writeBatchText writes text with batchPercentGuard before every '%'.
func writeBatchText(line *strings.Builder, text string) {
	for index := range len(text) {
		if text[index] == '%' {
			line.WriteString(batchPercentGuard)
		}
		line.WriteByte(text[index])
	}
}
