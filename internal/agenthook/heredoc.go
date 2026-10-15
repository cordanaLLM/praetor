// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"regexp"
	"strings"
)

// The heredoc rule (#1091): a commit message or an issue body passed as a heredoc is text, so a
// skip flag it quotes passes no flag to anything. Every rule judged the whole command, so
// `git commit -F - <<'EOF'` whose message quoted the skip flag was refused as evasion.
// withoutHeredocBodies drops such a body before the rules run, but only where the body cannot be
// executed: the line holding the operator is one simple command (no character HeredocVeto names)
// whose program HeredocReaders names, and an unquoted delimiter's body holds no command
// substitution. Anything else, a heredoc fed to a shell or an interpreter included, is judged
// whole. The Python guards carry these expressions byte for byte (parity_test.go).
const (
	// HeredocOperator matches one heredoc operator and its delimiter: <<, an optional dash, then
	// the delimiter single-quoted (group 2), double-quoted (group 3) or bare (group 5, with a
	// quoting backslash in group 4). A here-string (<<<) is no heredoc.
	HeredocOperator = `(?:^|[^<])<<(-?)[ \t]*(?:\x27([A-Za-z0-9_.-]+)\x27|\x22([A-Za-z0-9_.-]+)\x22|(\\?)([A-Za-z0-9_.-]+))`
	// HeredocReaders matches an operator line whose program, after any variable assignments,
	// reads the heredoc as text: a commit or tag message, an issue or pull request body, a file.
	HeredocReaders = `^[ \t]*(?:[A-Za-z_][A-Za-z0-9_]*=[^ \t]*[ \t]+)*(?:git|gh|glab|cat|tee)(?:[ \t]|$)`
	// HeredocQuoted matches the quoted words of an operator line that expand nothing: single-quoted
	// text, and double-quoted text without $, a backtick or a backslash. HeredocVeto judges the
	// line without them, so a title such as "fix(hooks): x" is a word, not a subshell.
	HeredocQuoted = `\x27[^\x27]*\x27|\x22[^\x22$\x60\\]*\x22`
	// HeredocVeto matches an operator line that is more than one simple command, or whose words
	// are computed, so its heredoc may reach another program.
	HeredocVeto = `[|;&(){}$\x60]`
	// HeredocSubstitution matches a command substitution an unquoted heredoc body expands.
	HeredocSubstitution = `\$\(|\x60`
)

var (
	heredocOperator     = regexp.MustCompile(HeredocOperator)
	heredocReaders      = regexp.MustCompile(HeredocReaders)
	heredocQuoted       = regexp.MustCompile(HeredocQuoted)
	heredocVeto         = regexp.MustCompile(HeredocVeto)
	heredocSubstitution = regexp.MustCompile(HeredocSubstitution)
)

// withoutHeredocBodies returns command without the bodies and delimiter lines of the heredocs a
// text reader consumes (see HeredocReaders); every other line, the operator lines included, is
// kept. A heredoc that does not end, or that another program may read, keeps its body.
func withoutHeredocBodies(command string) string {
	lines := strings.Split(command, "\n")
	kept := make([]string, 0, len(lines))
	for index := 0; index < len(lines); {
		line := lines[index]
		kept = append(kept, line)
		index++
		operators := heredocOperator.FindAllStringSubmatch(line, -1)
		if len(operators) == 0 || heredocVeto.MatchString(heredocQuoted.ReplaceAllString(line, "")) || !heredocReaders.MatchString(line) {
			continue
		}
		if end, ok := heredocBodiesEnd(lines, index, operators); ok {
			index = end
		}
	}
	return strings.Join(kept, "\n")
}

// heredocBodiesEnd returns the index of the first line after the bodies of operators, which
// start at lines[start], and whether every body ends and holds nothing the shell executes.
func heredocBodiesEnd(lines []string, start int, operators [][]string) (int, bool) {
	index := start
	for _, operator := range operators {
		dash, quoted := operator[1] == "-", operator[2] != "" || operator[3] != "" || operator[4] != ""
		delimiter := operator[2] + operator[3] + operator[5]
		body := make([]string, 0)
		for ; index < len(lines); index++ {
			candidate := strings.TrimSuffix(lines[index], "\r")
			if dash {
				candidate = strings.TrimLeft(candidate, "\t")
			}
			if candidate == delimiter {
				break
			}
			body = append(body, lines[index])
		}
		if index == len(lines) {
			return start, false
		}
		if !quoted && heredocSubstitution.MatchString(strings.Join(body, "\n")) {
			return start, false
		}
		index++
	}
	return index, true
}
