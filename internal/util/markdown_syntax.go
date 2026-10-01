package util

import "strings"

// MarkdownCodeSpan locates one inline code span in a text: Start is the index of the first
// backtick of its opening run, End the index just past its closing run, and Run the length
// of both runs. text[Start:End] is the span with its delimiters.
type MarkdownCodeSpan struct {
	Start, End, Run int
}

// MarkdownCodeSpans returns at most limit inline code spans of text, in order, following
// CommonMark: a span opens with a run of backticks and closes at the next run of exactly
// the same length. A run that is never closed is literal backticks, so it pairs with
// nothing and the runs after it pair among themselves; a lone double backtick or a fence
// delimiter run therefore never yields an empty span. text may hold several lines of one
// paragraph, since a code span runs across a soft line break.
//
// It is the repository's one inline code span reader (HISS-19): the documentation
// reference check (internal/docsref/markdown.go) reads command references with it, and the
// caveman scanner (internal/caveman/scan.go) masks and collects code spans with it.
func MarkdownCodeSpans(text string, limit int) []MarkdownCodeSpan {
	var spans []MarkdownCodeSpan
	for pos := 0; pos < len(text) && len(spans) < limit; {
		open := strings.IndexByte(text[pos:], '`')
		if open < 0 {
			break
		}
		start := pos + open
		run := backtickRun(text, start)
		end := closingBacktickRun(text, start+run, run)
		if end < 0 {
			pos = start + run
			continue
		}
		spans = append(spans, MarkdownCodeSpan{Start: start, End: end + run, Run: run})
		pos = end + run
	}
	return spans
}

// Content returns what the span renders in text, as CommonMark defines it: a line ending
// reads as a space, then one leading and one trailing space are stripped when both are
// present and the content is not only spaces.
func (s MarkdownCodeSpan) Content(text string) string {
	content := strings.ReplaceAll(text[s.Start+s.Run:s.End-s.Run], "\n", " ")
	if len(content) >= 2 && content[0] == ' ' && content[len(content)-1] == ' ' && strings.TrimSpace(content) != "" {
		return content[1 : len(content)-1]
	}
	return content
}

// backtickRun returns the length of the backtick run starting at index.
func backtickRun(text string, index int) int {
	run := 0
	for index+run < len(text) && text[index+run] == '`' {
		run++
	}
	return run
}

// closingBacktickRun returns the index of the next backtick run of exactly length run at or
// after from, or -1 when the span is never closed in text.
func closingBacktickRun(text string, from, run int) int {
	for pos := from; pos < len(text); {
		next := strings.IndexByte(text[pos:], '`')
		if next < 0 {
			return -1
		}
		candidate := pos + next
		length := backtickRun(text, candidate)
		if length == run {
			return candidate
		}
		pos = candidate + length
	}
	return -1
}

// MarkdownShell says how the lines of a fenced block are read as commands.
type MarkdownShell int

const (
	// ShellNone is any fence that is not a shell: YAML, JSON, Go, Mermaid, plain text, or a
	// fence without an info string. Its content is data, source or program output, where a
	// line is not a command a reader runs.
	ShellNone MarkdownShell = iota
	// ShellScript is a shell script: every line that is not blank or a comment is a command.
	ShellScript
	// ShellSession is a terminal transcript: only a line after the "$ " prompt is a command;
	// every other line is the output it printed.
	ShellSession
)

// shellFences maps the fence languages read as commands to how they are read.
var shellFences = map[string]MarkdownShell{
	"bash": ShellScript, "sh": ShellScript, "shell": ShellScript, "zsh": ShellScript,
	"fish": ShellScript, "powershell": ShellScript, "pwsh": ShellScript, "ps1": ShellScript,
	"console": ShellSession, "shell-session": ShellSession, "terminal": ShellSession,
}

// MarkdownFenceLanguage returns the first word of an opening fence's info string,
// lower-cased and without the braces and dot of the "{.bash}" attribute form. trimmed is
// the whitespace-trimmed opening line and marker its delimiter run (MarkdownFence.Marker).
func MarkdownFenceLanguage(trimmed, marker string) string {
	info := strings.Fields(strings.TrimPrefix(trimmed, marker))
	if len(info) == 0 {
		return ""
	}
	return strings.ToLower(strings.Trim(info[0], "{}."))
}

// MarkdownShellFence returns how a fence of language lang (MarkdownFenceLanguage) reads its
// lines. It is the one table of command fences: the documentation reference check reads
// the commands it verifies from these fences, and the caveman clarity floor holds a rewrite
// to the same commands.
func MarkdownShellFence(lang string) MarkdownShell {
	return shellFences[lang]
}

// MarkdownShellCommand reports whether trimmed, one whitespace-trimmed line inside a fence
// read as shell, starts a command, and returns that command without a "$ " prompt. Blank
// lines and "#" comments are never commands, and in a session only a line after the prompt
// is one; a script line may carry the prompt too. A line that continues a command ending in
// a backslash is the caller's to track: this reads one line alone.
func MarkdownShellCommand(shell MarkdownShell, trimmed string) (string, bool) {
	if shell == ShellNone || trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	command, prompted := strings.CutPrefix(trimmed, "$ ")
	if shell == ShellSession && !prompted {
		return "", false
	}
	return command, true
}

// MarkdownFrontMatterEnd returns the index of the first line after a YAML front matter
// block that opens the document, or 0 when the document has none or the block is never
// closed: a "---" first line through the next "---" line, trailing blanks ignored, as
// markdownlint and the agent skill loaders read it. lines are the document's lines without
// their line endings.
func MarkdownFrontMatterEnd(lines []string) int {
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return i + 1
		}
	}
	return 0
}
