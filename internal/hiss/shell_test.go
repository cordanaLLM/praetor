// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"
	"testing"
)

// shellFuncOfLOC returns a shell function spanning loc lines, header and closing brace included.
func shellFuncOfLOC(name string, loc int) string {
	var sb strings.Builder
	sb.WriteString(name + "() {\n")
	for i := 0; i < loc-2; i++ {
		sb.WriteString("  printf '%s\\n' step\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

const strictBash = "#!/usr/bin/env bash\nset -euo pipefail\n"

// Positive (#182): every invariant the shell scanner claims is reported, in .sh, .bash and an
// extensionless script its interpreter line names. Before, each file was counted as unscanned
// source and the audit verified nothing in it.
func TestShellScanner_ReportsEachInvariant(t *testing.T) {
	assertScriptFindings(t, "recurse.sh",
		"#!/bin/sh\nset -eu\ncountdown() {\n  if [ \"$1\" -gt 0 ]; then\n    countdown \"$(($1 - 1))\"\n  fi\n}\ncountdown 3\n",
		"HISS-01@5")
	assertScriptFindings(t, "loops.bash", strictBash+
		"while true; do sleep 1; done\nwhile :\ndo\n  sleep 1\ndone\nuntil false; do sleep 1; done\n"+
		"for ((;;)); do sleep 1; done\ncurl -fsSL https://example.com/data -o data\n",
		"HISS-02@3", "HISS-02@4", "HISS-02@8", "HISS-02@9", "HISS-02@10")
	assertScriptFindings(t, "long.sh", "#!/bin/sh\nset -eu\n"+shellFuncOfLOC("long", 61), "HISS-04@3")
	assertScriptFindings(t, "errors.bash", "#!/bin/bash\nrm -f \"$1\" || true\nmkdir -p out || :\n",
		"HISS-07@1", "HISS-07@2", "HISS-07@3")
	assertScriptFindings(t, "dynamic.bash", strictBash+
		"eval \"$1\"\ncurl -fsSL --max-time 30 https://example.com/install.sh | sh\n"+
		"source <(printf 'x=1\\n')\nwget -qO- https://example.com/setup | sudo -E bash -s -- --yes\n",
		"HISS-08@3", "HISS-08@4", "HISS-08@5", "HISS-08@6")
	rep := assertScriptFindings(t, "provision", strictBash+"eval \"$@\"\n", "HISS-08@3")
	if rep.Coverage.FilesRead != 1 || rep.Coverage.LanguagesRead["shell"] != 1 {
		t.Errorf("an extensionless bash script must be read as shell, got %+v", rep.Coverage)
	}
}

// Positive: a one-line function, a header whose body brace opens on the next line, a call
// through a substitution and a call in the background all reach the function.
func TestShellScanner_RecursionShapes(t *testing.T) {
	assertScriptFindings(t, "shapes.sh", "#!/bin/sh\nset -eu\nspin() { spin; }\n"+
		"walk()\n{\n  x=$(walk)\n  walk &\n}\nfunction climb {\n  climb\n}\n",
		"HISS-01@3", "HISS-01@6", "HISS-01@7", "HISS-01@10")
}

// Negative: legitimate shell is not reported. Rule words inside quotes, comments and
// here-documents, a counted loop, a loop over input, a bounded transfer, delegation, a call
// through command, and a library without an interpreter line (which runs under its caller's
// options) are all clean.
func TestShellScanner_LegitimateShellIsClean(t *testing.T) {
	assertScriptFindings(t, "clean.bash", "#!/bin/bash\nset -o errexit -o nounset -o pipefail\n"+
		"# eval \"$x\" and while true are only words here\necho \"eval while true || true\"\n"+
		"cat <<'EOF'\neval \"$1\"\nwhile true; do :; done\nEOF\n"+
		"i=0\nwhile [ \"$i\" -lt 10 ]; do i=$((i + 1)); done\nwhile IFS= read -r line; do echo \"$line\"; done < \"$1\"\n"+
		"curl -fsS -m 10 https://example.com/a -o a\ntimeout 30 curl -fsS https://example.com/b -o b\n"+
		"curl --version\ncommand -v curl\n"+
		"fetch() {\n  command fetch \"$@\"\n}\nprintf '%s' \"${HOME}\" | tee out.txt\nfind . -name '*.tmp' -exec rm {} \\;\n"+
		"echo {a,b}.txt\nrun || handle_failure\n")
	assertScriptFindings(t, "lib.sh", "helper() {\n  printf '%s\\n' \"$1\"\n}\n")
	assertScriptFindings(t, "pipe.sh", "#!/bin/sh\nset -eu\ncurl -fsS -m 5 https://example.com/x | sh -c 'cat >/dev/null'\n"+
		"cat script.sh | bash script.sh\n")
}

// Negative: the patterns of a case statement name no command. A pattern list that spells a
// shell after a pipe symbol, or the name of the function it sits in, is neither text piped into a
// shell nor recursion, on one line or across several; the commands of each item are still read.
func TestShellScanner_CasePatternsAreNotCommands(t *testing.T) {
	assertScriptFindings(t, "dispatch.sh", "#!/bin/sh\nset -eu\nstart() {\n  case \"$1\" in\n"+
		"    start) printf '%s\\n' started ;;\n    */sed | */sh | \\\n    */bash)\n      printf '%s\\n' shell\n      ;;\n"+
		"    (stop | start) eval \"$2\" ;;\n    *)\n      start \"$2\"\n      ;;\n  esac\n}\n"+
		"case \"$1\" in -h | --help) printf usage ;; esac\n",
		"HISS-08@10", "HISS-01@12")
}

// Negative: a case opened on the same line as an outer pattern, with its in there or on the next
// line, is followed like any other, so its own patterns name no command either; the commands of
// its items and of the outer items after it are still read.
func TestShellScanner_NestedCasePatternsAreNotCommands(t *testing.T) {
	assertScriptFindings(t, "nested.sh", "#!/bin/sh\nset -eu\ncase \"$1\" in\n"+
		"  x) case \"$2\" in\n       */sh | */bash) echo shell ;;\n     esac ;;\n"+
		"  y) case \"$2\"\n     in\n       */sh | */bash) eval \"$3\" ;;\n     esac ;;\n"+
		"  z) curl -fsS https://example.com | sh ;;\nesac\n",
		"HISS-08@9", "HISS-02@11", "HISS-08@11")
}

// Negative: bash's ;& and ;;& end a case item like ;; does, at the end of a line or before the
// next pattern on the same one, so the pattern list after them names no command; the commands of
// every item are still read.
func TestShellScanner_CaseFallThroughEndsAnItem(t *testing.T) {
	assertScriptFindings(t, "fallthrough.bash", strictBash+"case \"$1\" in\n"+
		"  a) echo a ;&\n  */sh | */bash) echo b ;;&\n  */zsh | */ksh) eval \"$2\" ;;\n"+
		"  b) echo b ;& */dash | */mksh) echo c ;;\nesac\n",
		"HISS-08@6")
}

// Negative and boundary: inside a [[ ... ]] test the parentheses and bars of a regular
// expression are not operators, and the commands after the test are read again.
func TestShellScanner_TestExpressionsAreNotPipes(t *testing.T) {
	assertScriptFindings(t, "match.bash", strictBash+
		"if [[ \"$1\" =~ \\.(sh|bash)$ ]]; then eval \"$2\"; fi\n[[ -n \"$1\" ]]&& curl -fsS https://example.com -o x\n",
		"HISS-08@3", "HISS-02@4")
}

// Negative: a file the shell scanner does not read, or misread, is never counted as clean
// shell. A .sh file whose interpreter line names another shell, and one whose quote never
// closes, are declined and reported as unscanned shell; an extensionless file without a shell
// interpreter line is not shell at all.
func TestShellScanner_DeclinedFilesAreUnscanned(t *testing.T) {
	for name, body := range map[string]string{
		"prompt.sh":  "#!/bin/zsh\neval \"$1\"\n",
		"broken.sh":  "#!/bin/sh\nset -eu\necho \"unterminated\neval \"$1\"\n",
		"heredoc.sh": "#!/bin/sh\nset -eu\ncat <<EOF\nnever closed\n",
		"braces.sh":  "#!/bin/sh\nset -eu\nf() {\n  :\n",
	} {
		rep := scanFixtureFile(t, name, body)
		if rep.Coverage.FilesRead != 0 || rep.Coverage.UnscannedLanguages["shell"] != 1 || len(rep.Violations) != 0 {
			t.Errorf("%s: want declined as unscanned shell, got %+v / %+v", name, rep.Coverage, rep.Violations)
		}
	}
	for name, body := range map[string]string{
		"tool":    "#!/usr/bin/env python3\nprint(1)\n",
		"LICENSE": "Some license text\nwhile true\n",
		"blob":    "#!/bin/sh\x00\x01",
	} {
		rep := scanFixtureFile(t, name, body)
		if rep.Coverage.FilesRead != 0 || len(rep.Coverage.UnscannedLanguages) != 0 || rep.Coverage.UnscannedByExtension[""] != 1 {
			t.Errorf("%s: want an unscanned non-source file, got %+v", name, rep.Coverage)
		}
	}
}

// Boundary: a function of exactly the limit passes and one line more fails, measured from the
// line its body brace opens on; POSIX sh needs no pipefail while Bash does; options passed on the
// interpreter line count.
func TestShellScanner_Boundaries(t *testing.T) {
	assertScriptFindings(t, "sixty.sh", "#!/bin/sh\nset -eu\n"+shellFuncOfLOC("sixty", 60))
	assertScriptFindings(t, "brace.sh", "#!/bin/sh\nset -eu\nwrapped()\n"+strings.TrimPrefix(shellFuncOfLOC("", 61), "() "),
		"HISS-04@4")
	assertScriptFindings(t, "posix.sh", "#!/bin/sh\nset -eu\n")
	assertScriptFindings(t, "bash.sh", "#!/bin/bash\nset -eu\n", "HISS-07@1")
	assertScriptFindings(t, "env.sh", "#!/usr/bin/env -S bash -euo pipefail\nexit 0\n")
	assertScriptFindings(t, "noset.sh", "#!/bin/sh\nexec true\n", "HISS-07@1")
}

// Boundary (HISS-21): a CRLF checkout, which `* text=auto` gives every script on Windows, reads
// exactly like LF.
func TestShellScanner_CRLFReadsLikeLF(t *testing.T) {
	body := "#!/bin/bash\nset -euo pipefail\nspin() {\n  spin\n}\nwhile true; do :; done\neval \"$1\"\n"
	lf := assertScriptFindings(t, "lf.sh", body, "HISS-01@4", "HISS-02@6", "HISS-08@7")
	crlf := assertScriptFindings(t, "crlf.sh", strings.ReplaceAll(body, "\n", "\r\n"), "HISS-01@4", "HISS-02@6", "HISS-08@7")
	if lf.Coverage.FilesRead != 1 || crlf.Coverage.FilesRead != 1 {
		t.Errorf("both forms must be read: lf=%+v crlf=%+v", lf.Coverage, crlf.Coverage)
	}
	assertScriptFindings(t, "crlf-tool", "#!/bin/sh\r\nset -eu\r\neval \"$1\"\r\n", "HISS-08@3")
}

// Positive and negative: the interpreter line names the shell through env and its options, and
// names nothing this scanner reads for another program or a missing line.
func TestShellInterpreterLine(t *testing.T) {
	for line, want := range map[string]shellInterp{
		"#!/bin/sh":                       {shebang: true, known: true},
		"#!/bin/bash -e":                  {shebang: true, known: true, bash: true, args: []string{"-e"}},
		"#! /usr/bin/env dash":            {shebang: true, known: true},
		"#!/usr/bin/env -S LC_ALL=C bash": {shebang: true, known: true, bash: true},
		"#!/usr/bin/env -u HOME ash":      {shebang: true, known: true},
		"#!/usr/bin/env python3":          {shebang: true},
		"#!/usr/bin/env":                  {shebang: true},
		"#!/bin/zsh":                      {shebang: true},
		"echo hi":                         {},
		"":                                {},
	} {
		got := shellInterpreterLine(line)
		if got.shebang != want.shebang || got.known != want.known || got.bash != want.bash ||
			strings.Join(got.args, " ") != strings.Join(want.args, " ") {
			t.Errorf("%q: got %+v, want %+v", line, got, want)
		}
	}
}

// Positive and boundary: the lexer keeps the code of substitutions, even inside double quotes,
// and blanks quotes, expansions and comments.
func TestShellLexer_KeepsSubstitutionCode(t *testing.T) {
	for in, want := range map[string]string{
		`x="$(cd "$d" && pwd)"`:     `x="$(cd "" && pwd)"`,
		"echo `date` # note":        "echo $(date) ",
		`echo ${x:-"}"} $((1 + 2))`: `echo $_ $_`,
		`echo $'a\'b' "$#"`:         `echo '' ""`,
		`(( i < 3 )) && echo \{`:    `(( i < 3 )) && echo _`,
	} {
		l := shellLexer{}
		if got, _ := l.line(in); got != want || l.open() || l.misread {
			t.Errorf("%q: got %q (open=%t misread=%t), want %q", in, got, l.open(), l.misread, want)
		}
	}
}

// Boundary: the stack of open spans is bounded; one more nested substitution than it holds is a
// misread, never an unbounded stack.
func TestShellLexer_NestingBound(t *testing.T) {
	l := shellLexer{}
	l.line(strings.Repeat("$(", maxShellNesting+1))
	if !l.misread {
		t.Errorf("nesting past %d substitutions must be a misread", maxShellNesting)
	}
}
