package adopt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// evasionCorpus is agenthook's replay corpus: the payloads praetor's own guard and the Go
// policy are held to, both directions (HISS-20).
var evasionCorpus = filepath.Join("..", "agenthook", "testdata", "pre-tool", "cases.json")

type evasionCase struct {
	Name     string          `json:"name"`
	Allow    bool            `json:"allow"`
	Operator bool            `json:"operator"`
	Payload  json.RawMessage `json:"payload"`
}

// emittedInterceptor writes the interceptor adoption renders and returns the interpreter
// and script path, or skips with the reason on a host without python3 (HISS-21).
func emittedInterceptor(t *testing.T) (string, string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH; the emitted interceptor cannot run on this leg")
	}
	script := filepath.Join(t.TempDir(), "block_evasion.py")
	mustWrite(t, script, buildBlockEvasionPY())
	return python, script
}

// evasionCommitSegmentCases preserves round-one rows and plants bounded shell spans.
func evasionCommitSegmentCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"chained tail", `git commit -m 'fix: message' && git log -1 | tail -n 1`, 0},
		{"chained tail with ref", `git commit -m 'fix: message' && git log -1 "$ref" | tail -n 1`, 0},
		{"quoted separator then flag", `git commit -m 'message && text' -n`, 2},
		{"bare short flag", `git commit -n`, 2},
		{"long flag", `git commit --no-` + `verify`, 2},
		{"am short flag", `git am -n patch`, 2},
		{"am long flag", `git am --no-` + `verify patch`, 2},
		{"unbalanced quote", `git commit -m 'unfinished`, 2},
		{"exempt span", `git commit -m 'message $name' && tail -n 1`, 0},
		{"quoted operator token", `git commit -m '&&' -n`, 2},
		{"escaped separator", `git commit -m message\&\&text -n`, 2},
		{"nested backtick quotes", "git commit -m \"`echo \"a;b\"`\" -n", 2},
		{"semicolon", `git commit -m 'fix' ; tail -n 1`, 0},
		{"or", `git commit -m 'fix' || tail -n 1`, 0},
		{"pipe", `git commit -m 'fix' | tail -n 1`, 0},
		{"background", `git commit -m 'fix' & tail -n 1`, 0},
		{"next commit", `git commit -m 'fix' && git commit -n`, 2},
		{"redirect pipe", `git commit -m x 2>&1 | tail -n 1`, 0},
		{"substitution message", `git commit -m "$(cat msg.txt)" && git log -1 | tail -n 1`, 0},
		{"redirect grep then log", `git commit -F msg.txt 2>&1 | grep -c ok; git log -1 | tail -n 1`, 0},
		{"heredoc message", "git commit -m \"$(cat <<'EOF'\nmessage ; && |\nEOF\n)\" && git log -1 | tail -n 1", 0},
		{"redirect then flag", `git commit -m x 2>&1 -n`, 2},
		{"substitution flag", `git commit "$(printf %s -n)"`, 2},
		// Message backticks remain data; positional backticks retain fallback.
		{"backtick message", "git commit -m \"`cat msg.txt`\" && tail -n 1", 0},
		{"backtick flag", "git commit \"`printf %s -n`\"", 2},
		{"nested substitution", `git commit -m "$(echo "$(cat msg.txt)")" && tail -n 1`, 0},
		{"nested substitution flag", `git commit "$(echo "$(printf %s -n)")"`, 2},
		// Outer newlines separate commands.
		{"newline", "git commit -m x\ngit log -1 | tail -n 1", 0},
		{"file redirections", `git commit -F msg.txt >>out 2>/dev/null <in && tail -n 1`, 0},
		{"descriptor input", `git commit -m x 3<&0 2>&- & tail -n 1`, 0},
		{"heredoc outer", "git commit -F - <<EOF\nmessage ' ; && |\nEOF\ntail -n 1", 0},
		{"heredoc tabs", "git commit -F - <<-EOF\n\tmessage\n\tEOF\ntail -n 1", 0},
		{"multiple heredocs", "git commit -F - <<A <<\"B\"\nfirst\nA\nsecond\nB\ntail -n 1", 0},
		{"substitution separator flag", `git commit "$(cat msg.txt; printf %s -n)"`, 2},
		{"unterminated substitution", `git commit -m "$(cat msg.txt" && tail -n 1`, 2},
		{"unterminated heredoc", "git commit -F - <<EOF\nmessage && tail -n 1", 2},
		{"state alias", `alias git=other; git commit -m x && tail -n 1`, 2},
		{"state export", `export X=1; git commit -m x && tail -n 1`, 2},
		{"state function", `function git() { other; }; git commit -m x && tail -n 1`, 2},
		// The nesting bound admits parsing; unquoted execution retains splitting.
		{"nesting at bound", "git commit -m " + strings.Repeat("$(", 16) + "cat msg.txt" + strings.Repeat(")", 16) + " && tail -1", 2},
		{"nesting past bound", "git commit -m " + strings.Repeat("$(", 17) + "cat msg.txt" + strings.Repeat(")", 17) + " && tail -n 1", 2},
		{"heredoc lines at bound", "git commit -F - <<EOF\n" + strings.Repeat("x\n", 255) + "EOF\ntail -n 1", 0},
		{"heredoc lines past bound", "git commit -F - <<EOF\n" + strings.Repeat("x\n", 256) + "EOF\ntail -n 1", 2},
	}
}

// evasionFallbackCases covers order engine-block-evasion-commit-span-20261114 round 3.
func evasionFallbackCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		// Order engine-block-evasion-commit-span-20261114 round 3: own syntax vetoes segmentation.
		{"own parameter", `git commit $flags -m x && tail -n 1`, 2},
		{"am parameter", `git am $flags patch && tail -n 1`, 2},
		{"own substitution", `git commit $(cat flags) -m x && tail -n 1`, 2},
		{"own escape", `git commit -m a\b && tail -n 1`, 2},
		{"own continuation", "git commit -m x\\\n && tail -n 1", 2},
		{"single quoted newline", "git commit -m 'a\nb' && tail -n 1", 2},
		{"double quoted newline", "git commit -m \"a\nb\" && tail -n 1", 2},
		{"later newline", "git commit -m x && printf \"a\nb\" | tail -n 1", 0},
		{"later escape", `git commit -m x && printf a\b | tail -n 1`, 0},
		{"later parameter", `git commit -m x && git log $flags | tail -n 1`, 0},
		{"quoted parameter", `git commit -m 'a$b' && tail -n 1`, 0},
		{"quoted escape", `git commit -m 'a\b' && tail -n 1`, 0},
		{"own input", `git commit -m a<b && tail -n 1`, 0},
		{"own output", `git commit -m a>b && tail -n 1`, 0},
		{"background log", `git commit -m x & git log -n 1`, 0},
		{"combined pipe", `git commit -m x |& head -n 3`, 0},
		{"unrelated quote", `printf 'unfinished`, 0},
		{"unrelated git quote", `git status 'unfinished`, 0},
		{"commit option quote", `git -C repo --no-pager commit -m 'unfinished`, 2},
		{"am option quote", `git -c x=y am 'unfinished`, 2},
		{"unicode commit quote", "git\u00a0commit -m 'unfinished", 2},
	}
}

// evasionRoundFourCases covers message operands, executable expansions and malformed shell input.
func evasionRoundFourCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"am bundled flag", `git am -cn patch`, 2},
		{"positional parameter", `git commit "$x" && git log -n 1`, 2},
		{"positional prefixed parameter", `git commit "-$suffix" && git log -n 1`, 2},
		{"positional substitution", `git commit "$(printf %s -n)" && git log -n 1`, 2},
		{"positional glob", `git commit * && git log -n 1`, 2},
		{"positional question glob", `git commit file? && git log -n 1`, 2},
		{"positional bracket glob", `git commit [ab] && git log -n 1`, 2},
		{"positional heredoc substitution", "git commit $(cat <<EOF\n-n\nEOF\n) && git log -n 1", 2},
		{"message heredoc flag text", "git commit -m \"$(cat <<EOF\n-n\nEOF\n)\" && git log -n 1", 0},
		{"stdin message flag text", "git commit -F - <<EOF\n-n\nEOF\ngit log -n 1", 0},
		{"stdin message parameter text", "git commit -F - <<EOF\ngit commit -n $name\nEOF\ngit log -n 1", 0},
		{"stdin message executable", "git commit -F - <<EOF\n$(git commit -n)\nEOF", 2},
		{"stdin message invalid execution", "git commit -F - <<EOF\n$(cat <<MISSING)\nEOF", 2},
		{"quoted stdin message expansion", "git commit -F - <<'EOF'\n$(printf %s -n)\nEOF\ngit log -n 1", 0},
		{"message short flag text", `git commit -m 'mention -n here'`, 0},
		{"message option operand", `git commit -m -n`, 0},
		{"message substitution flag text", `git commit -m "$(printf %s -n)" && git log -n 1`, 0},
		{"message positional flag", `git commit -m "$x" "$flags" && git log -n 1`, 2},
		{"message joined parameter", `git commit --message="$x" && git log -n 1`, 0},
		{"file parameter", `git commit -F "$x" && git log -n 1`, 0},
		{"file joined parameter", `git commit --file="$x" && git log -n 1`, 0},
		{"reuse parameter", `git commit -C "$x" && git log -n 1`, 0},
		{"reedit parameter", `git commit -c "$x" && git log -n 1`, 0},
		{"author parameter", `git commit --author "$x" && git log -n 1`, 0},
		{"date parameter", `git commit --date="$x" && git log -n 1`, 0},
		{"message inline flag text", `git commit -m'mention -n here'`, 0},
		{"message read only span", `git commit -m 'git log -n 1; done'`, 0},
		{"comment apostrophe", `git commit -m x # author's note`, 0},
		{"comment flag text", "git commit -m x # -n author's note\ngit log -n 1", 0},
		{"hash within word", `git commit path#name && git log -n 1`, 0},
		{"substitution comment", "git commit -m \"$(printf x # )\"; \"\\\n)\" -n", 2},
		{"substitution comment fallback", "git commit -m \"$(printf x # )\"; \"\\\n)\" && git log -n 1", 2},
		{"bare unmatched backtick", "git commit `unfinished", 2},
		{"quote inside substitution", `git commit -m "$(printf 'unfinished)"`, 2},
		{"heredoc without delimiter", "git commit -F - <<EOF\nmessage", 2},
		{"literal message nested commit", `git commit -m 'git commit -n'`, 0},
		{"executable nested commit", `git commit -m "$(git commit -n)"`, 2},
		{"executable nested message", `git commit -m "$(git commit -m 'mention -n here')"`, 0},
	}
}

// evasionRoundFiveCases covers process bodies, splitting operands and escaped comment boundaries.
func evasionRoundFiveCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"process input flag", `git commit <(printf %s -n)`, 2},
		{"process output flag", `git commit >(printf %s -n)`, 2},
		{"process am flag", `git am <(printf %s -n)`, 2},
		{"process file flag", `git commit -F <(printf %s -n)`, 2},
		{"process nested executable", `git commit -F <(git commit -n)`, 2},
		{"process quoted literal", `git commit -m '<(printf %s -n)'`, 0},
		{"process double quoted literal", `git commit -m ">(printf %s -n)"`, 0},
		{"splitting message execution", `git commit -m $(printf '%s %s' msg -n)`, 2},
		{"single word message execution", `git commit -m "$(printf '%s %s' msg -n)"`, 0},
		{"splitting nested execution", "git commit -m " + strings.Repeat("$(", 16) + "cat msg.txt" + strings.Repeat(")", 16) + " && tail -n 1", 2},
		{"splitting message parameter", `git commit -m $msg && git log -n 1`, 2},
		{"splitting file parameter", `git commit -F $file && git log -n 1`, 2},
		{"splitting reuse parameter", `git commit -C $msg && git log -n 1`, 2},
		{"splitting reedit parameter", `git commit -c $msg && git log -n 1`, 2},
		{"splitting author parameter", `git commit --author=$author && git log -n 1`, 2},
		{"splitting date parameter", `git commit --date $date && git log -n 1`, 2},
		{"splitting message glob", `git commit -m * && git log -n 1`, 2},
		{"splitting joined message", `git commit --message=$(printf '%s %s' msg -n)`, 2},
		{"splitting message backticks", "git commit -m `printf '%s %s' msg -n`", 2},
		{"single word reuse long", `git commit --reuse-message="$msg" && git log -n 1`, 0},
		{"single word reedit long", `git commit --reedit-message "$msg" && git log -n 1`, 0},
		{"substitution suffix hash flag", `git commit -m $(printf %s fix)#note -n`, 2},
		{"escaped space hash flag", `git commit -m fix\ #note -n`, 2},
		{"escaped tab hash flag", "git commit -m fix\\\t#note -n", 2},
		{"escaped operator hash flag", `git commit -m fix\;#note -n`, 2},
		{"unescaped space comment", `git commit -m fix #note -n`, 0},
		{"unescaped operator comment", `git commit -m fix;#note -n`, 0},
		{"even escapes comment", `git commit -m fix\\ #note -n`, 0},
		{"odd escapes hash flag", `git commit -m fix\\\ #note -n`, 2},
		{"nested escaped space hash", `git commit -m "$(printf fix\ #note -n)" -n`, 2},
	}
}

// evasionRoundSevenCases covers argument shifts after splitting expansions and quoted boundaries.
func evasionRoundSevenCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"empty substitution shifts message", `git commit -m $(printf '') -m -n`, 2},
		{"empty variable shifts message", `git commit -m $msg -m -n`, 2},
		{"empty backtick shifts message", "git commit -m `printf ''` -m -n", 2},
		{"glob shifts message", `git commit -m * -m -n`, 2},
		{"positional substitution shifts message", `git commit $(printf '') -m -n`, 2},
		{"positional variable shifts message", `git commit $msg -m -n`, 2},
		{"positional backtick shifts message", "git commit `printf ''` -m -n", 2},
		{"positional glob shifts message", `git commit * -m -n`, 2},
		{"attached substitution shifts message", `git commit --message=$(printf '') -m -n`, 2},
		{"file substitution shifts message", `git commit -F $(printf '') -m -n`, 2},
		{"am substitution retains operands", `git am $(printf '') > -n`, 2},
		{"am variable retains operands", `git am $patch > -n`, 2},
		{"am backtick retains operands", "git am `printf ''` > -n", 2},
		{"am glob retains operands", `git am * > -n`, 2},
		{"quoted empty substitution message", `git commit -m "$(printf '')" -m 'mention -n here'`, 0},
		{"quoted empty variable message", `git commit -m "$msg" -m 'mention -n here'`, 0},
		{"quoted empty backtick message", "git commit -m \"`printf ''`\" -m 'mention -n here'", 0},
		{"quoted glob message", `git commit -m "*" -m 'mention -n here'`, 0},
		{"uncertainty resets at separator", `git commit -m $msg; git commit -m 'mention -n here'`, 2},
	}
}

// evasionRoundEightCases covers runtime splitting, quoted arrays and scalar masking boundaries.
func evasionRoundEightCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"runtime splitting message parameter", `git commit -m $msg && git log -n 1`, 2},
		{"quoted positional array message", `git commit -m "$@" && git log -n 1`, 2},
		{"quoted braced positional array message", `git commit -m "${@}" && git log -n 1`, 2},
		{"quoted array message", `git commit -m "${arr[@]}" && git log -n 1`, 2},
		{"quoted prefix names message", `git commit -m "${!prefix@}" && git log -n 1`, 2},
		{"quoted array keys message", `git commit -m "${!arr[@]}" && git log -n 1`, 2},
		{"quoted array slice message", `git commit -m "${arr[@]:1}" && git log -n 1`, 2},
		{"quoted array shifts message", `git commit -m "${arr[@]}" -m -n`, 2},
		{"quoted runtime message parameter", `git commit -m "$msg" && git log -n 1`, 0},
		{"quoted positional star message", `git commit -m "$*" && git log -n 1`, 0},
		{"quoted braced positional star message", `git commit -m "${*}" && git log -n 1`, 0},
		{"quoted array star message", `git commit -m "${arr[*]}" && git log -n 1`, 0},
		{"quoted array length message", `git commit -m "${#arr[@]}" && git log -n 1`, 0},
		{"quoted prefix star message", `git commit -m "${!prefix*}" && git log -n 1`, 0},
		{"quoted substitution positional array", `git commit -m "$(printf '%s' "$@")" && git log -n 1`, 0},
		{"quoted substitution array message", `git commit -m "$(printf '%s' "${arr[@]}")" && git log -n 1`, 0},
	}
}

// evasionRoundNineCases covers splitting before a subcommand and command-word masking.
func evasionRoundNineCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"pre-subcommand positional array", `set -- . commit -n -m; git -C "$@" commit -m -n`, 2},
		{"pre-subcommand quoted array", `arr=(. commit -n -m); git -C "${arr[@]}" commit -m -n`, 2},
		{"pre-subcommand splitting scalar", `var='. commit -n -m'; git -C $var commit -m -n`, 2},
		{"command word scalar", `$HOME/bin/git commit -m 'mention -n here'`, 0},
		{"indented command word scalar", `  $HOME/bin/git commit -m 'mention -n here'`, 0},
		{"pre-subcommand quoted scalar", `git -C "$dir" commit -m 'mention -n here'`, 0},
		{"pre-subcommand uncertainty resets", `git -C $dir commit -m x; git commit -m 'mention -n here'`, 2},
	}
}

// evasionRoundTenCases removes later flags from the reviewer's runtime-only examples.
func evasionRoundTenCases() []struct {
	name, command string
	want          int
} {
	previous := evasionRoundEightCases()
	tail := " && git log -n 1"
	values := strings.Join([]string{"msg", "-n"}, " ")
	variable := "msg='" + values + "'; "
	positional := "set -- " + values + "; "
	cases := []struct {
		name, command string
		want          int
	}{
		{"runtime-only message parameter", variable + strings.TrimSuffix(previous[0].command, tail), 2},
		{"runtime-only positional array", positional + strings.TrimSuffix(previous[1].command, tail), 2},
		{"runtime-only quoted message parameter", variable + strings.TrimSuffix(previous[8].command, tail), 0},
		{"runtime-only quoted positional scalar", positional + strings.TrimSuffix(previous[9].command, tail), 0},
		{"runtime-only quoted positional message", positional + `msg="$*"; ` + strings.TrimSuffix(previous[8].command, tail), 0},
	}
	for _, tc := range previous {
		tc.name = "standalone " + tc.name
		tc.command = strings.TrimSuffix(tc.command, tail)
		cases = append(cases, tc)
	}
	for _, tc := range evasionRoundNineCases()[:3] {
		tc.name = "standalone " + tc.name
		tc.command = strings.TrimSuffix(tc.command, " -m -n")
		cases = append(cases, tc)
	}
	return append(cases, evasionRoundTenCasesControls()...)
}

// evasionRoundTenCasesControls plants literal arguments and separator-local uncertainty controls.
func evasionRoundTenCasesControls() []struct {
	name, command string
	want          int
} {
	previous := evasionRoundSevenCases()
	reset := strings.Replace(evasionRoundNineCases()[6].command, "commit -m x;", "status;", 1)
	return []struct {
		name, command string
		want          int
	}{
		{"standalone substitution", strings.TrimSuffix(previous[0].command, " -m -n"), 2},
		{"standalone glob", strings.TrimSuffix(previous[3].command, " -m -n"), 2},
		{"standalone am substitution", strings.TrimSuffix(previous[10].command, " > -n"), 2},
		{"literal commit arguments", strings.Join([]string{"git", "commit", "-m", "msg"}, " "), 0},
		{"read-only uncertainty resets", reset, 0},
	}
}

// TestEmittedEvasionCommitSegments keeps skip flags within their shell command.
func TestEmittedEvasionCommitSegments(t *testing.T) {
	python, script := emittedInterceptor(t)
	cases := append(evasionCommitSegmentCases(), evasionRedirectCases()...)
	cases = append(append(cases, evasionFallbackCases()...), evasionRoundFourCases()...)
	cases = append(cases, evasionRoundFiveCases()...)
	cases = append(cases, evasionRoundSevenCases()...)
	cases = append(cases, evasionRoundEightCases()...)
	cases = append(cases, evasionRoundNineCases()...)
	for _, tc := range append(cases, evasionRoundTenCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			got, stderr := runInterceptorOutput(t, python, script, nil, nil, tc.command)
			t.Logf("emitted rc=%d want=%d", got, tc.want)
			if got != tc.want {
				t.Errorf("exit %d, want %d", got, tc.want)
			}
			if tc.want != 0 && strings.TrimSpace(stderr) != evasionSegmentRefusal(tc.name) {
				t.Errorf("refusal %q, want %q", stderr, evasionSegmentRefusal(tc.name))
			}
		})
	}
}

// splittingSegmentCase enumerates rows whose argument lists can split at runtime.
func splittingSegmentCase(name string) bool {
	name = strings.TrimPrefix(name, "standalone ")
	return slices.Contains([]string{
		"nesting at bound", "own parameter",
		"am parameter", "own substitution",
		"positional glob", "positional question glob",
		"positional bracket glob", "positional heredoc substitution",
		"process input flag", "process output flag",
		"process am flag", "process file flag",
		"process nested executable", "splitting message execution",
		"splitting nested execution", "splitting message parameter",
		"splitting file parameter", "splitting reuse parameter",
		"splitting reedit parameter", "splitting author parameter",
		"splitting date parameter", "splitting message glob",
		"splitting joined message", "splitting message backticks",
		"substitution suffix hash flag", "empty substitution shifts message",
		"empty variable shifts message", "empty backtick shifts message",
		"glob shifts message", "positional substitution shifts message",
		"positional variable shifts message", "positional backtick shifts message",
		"positional glob shifts message", "attached substitution shifts message",
		"file substitution shifts message", "am substitution retains operands",
		"am variable retains operands", "am backtick retains operands",
		"am glob retains operands", "uncertainty resets at separator",
		"runtime splitting message parameter", "quoted positional array message",
		"quoted braced positional array message", "quoted array message",
		"quoted prefix names message", "quoted array keys message",
		"quoted array slice message", "quoted array shifts message",
		"pre-subcommand positional array", "pre-subcommand quoted array",
		"pre-subcommand splitting scalar", "pre-subcommand uncertainty resets",
		"runtime-only message parameter", "runtime-only positional array",
		"substitution", "glob",
		"am substitution",
	}, name)
}

// evasionSegmentRefusal names the exact refusal required by malformed or flagged rows.
func evasionSegmentRefusal(name string) string {
	if splittingSegmentCase(name) {
		return agenthook.InvalidInputRefusal + "argument list unknown at scan time: write arguments literally or quote expansion"
	}
	errors := map[string]string{
		"unbalanced quote": "unbalanced quote", "commit option quote": "unbalanced quote",
		"am option quote": "unbalanced quote", "unicode commit quote": "unbalanced quote",
		"quote inside substitution": "unbalanced quote",
		"unbalanced double quote":   "unbalanced shell stack", "unterminated backtick": "unbalanced shell stack",
		"unterminated substitution": "unbalanced shell stack", "bare unmatched backtick": "unbalanced shell stack",
		"unterminated heredoc": "unterminated here-document", "heredoc without delimiter": "unterminated here-document",
		"stdin message invalid execution": "unterminated here-document",
		"nesting past bound":              "nesting bound exceeded: 16",
		"heredoc pending past bound":      "pending here-document bound exceeded: 16",
		"heredoc lines past bound":        "here-document body-line bound exceeded: 256",
	}
	if suffix, ok := errors[name]; ok {
		return agenthook.InvalidInputRefusal + "invalid shell command: " + suffix
	}
	rule := agenthook.BuiltinRules()[1]
	if name == "long flag" || name == "am long flag" {
		rule = agenthook.BuiltinRules()[0]
	}
	return rule.RefusalPrefix() + rule.Source
}

// evasionRedirectCases holds redirection variants that must never split their own flags.
func evasionRedirectCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"combined output", `git commit -m x &>out && tail -n 1`, 0},
		{"combined output flag", `git commit -m x &>out -n`, 2},
		{"clobber output", `git commit -m x >|out && tail -n 1`, 0},
		{"clobber output flag", `git commit -m x >|out -n`, 2},
		{"descriptor expansion", `git commit -m x >&"$fd" && tail -n 1`, 0},
		{"descriptor expansion flag", `git commit -m x >&"$fd" -n`, 2},
		{"multiline message flag", "git commit -m 'one\ntwo && three' -n", 2},
		{"unbalanced double quote", `git commit -m "unfinished`, 2},
		{"unterminated backtick", "git commit -m `cat msg.txt && tail -n 1", 2},
		{"heredoc own flag", "git commit -F - -n <<EOF\nmessage\nEOF\ntail -1", 2},
		{"single quoted substitution", `git commit -m '$(cat msg.txt)' && tail -n 1`, 0},
		{"state set", `set -a; git commit -m x && tail -n 1`, 2},
		{"heredoc queued chain", "git commit -F - <<EOF && tail -n 1\nmessage\nEOF", 0},
		{"heredoc queued flag", "git commit -F - <<EOF -n && tail -1\nmessage\nEOF", 2},
		{"heredoc queued pipeline", "git commit -F - <<EOF | tail -n 1\nmessage\nEOF", 0},
		{"heredoc nested context", "git commit -F - <<OUT -m \"$(cat <<IN\ninside\nIN\n)\" && tail -n 1\noutside\nOUT", 0},
		{"heredoc pending at bound", "git commit -F -" + strings.Repeat(" <<EOF", 16) + "\n" + strings.Repeat("EOF\n", 16) + "tail -n 1", 0},
		{"heredoc pending past bound", "git commit -F -" + strings.Repeat(" <<EOF", 17) + "\n" + strings.Repeat("EOF\n", 17) + "tail -n 1", 2},
		// Parameter expansions within message data preserve segmentation.
		{"substitution braces", `git commit -m "$(echo ${msg})" && tail -n 1`, 0},
	}
}

// evasionSegmentMutants builds independent regressions of the retained parsing defences.
func evasionSegmentMutants() []struct {
	name, before, after, command string
	want                         int
} {
	return append([]struct {
		name, before, after, command string
		want                         int
	}{
		{"brace subcommand uncertainty removed", `self.git_command, self.arguments_uncertain = "unknown", True`, `pass`, evasionDelimiterBraceCases()[16].command, 0},
		{"partial brace subcommand uncertainty removed", `self.git_command, self.arguments_uncertain = "unknown", True`, `pass`, evasionDelimiterBraceCases()[17].command, 0},
		{"brace command uncertainty removed", `self.git_command, self.arguments_uncertain = "unknown", True`, `pass`, evasionDelimiterBraceCases()[18].command, 0},
		{"partial brace command uncertainty removed", `self.git_command, self.arguments_uncertain = "unknown", True`, `pass`, evasionDelimiterBraceCases()[19].command, 0},
		{"brace uncertainty removed", `glob = ch in "*?[" or ch in "{}" and self.brace_splitting(ch)`, `glob = ch in "*?["`, evasionDelimiterBraceCases()[0].command, 0},
		{"partial delimiter reader removed", "delimiter, quoted, width = shell_here_doc_delimiter(word)",
			"match = re.match(r\"(\\\"[^\\\"]*\\\"|'[^']*'|[-A-Za-z0-9_]+)\", word)\ndelimiter = match[0].strip(\"\\\"'\")\nquoted, width = delimiter != match[0], match.end()",
			evasionDelimiterBraceCases()[1].command, 0},
		{"literal quote reader removed", "delimiter, quoted, width = shell_here_doc_delimiter(word)",
			"match = re.match(r\"(\\\"[^\\\"]*\\\"|'[^']*'|[-A-Za-z0-9_]+)\", word)\ndelimiter = match[0].strip(\"\\\"'\")\nquoted, width = delimiter != match[0], match.end()",
			evasionDelimiterBraceCases()[2].command, 0},
		{"body continuation removed", "if expandable and found and escapes % 2:", "if False:", evasionDelimiterBraceCases()[3].command, 0},

		{"old span", "for segment in segments:", "for segment in [(text, bool(re.search(READ_ONLY_VETO, text)))]:",
			`git commit -m 'fix' && git log -1 "$ref" | tail -n 1`, 2},
		{"exempt unsplit", "segments, READ_ONLY_EXEMPT_1, exempt)", "segments, None, exempt)",
			`git commit -m 'message $name' && tail -n 1`, 2},
		{"redirection veto", `if not any(ch in command for ch in "{}<") and not commit_command(`,
			"if re.search(r\"[<>]\", command):\n    return [(command, True)], []\n" + `if not any(ch in command for ch in "{}<") and not commit_command(`,
			`git commit -m x 2>&1 | tail -n 1`, 2},
		{"positional expansion", "self.uncertain |= self.word_expansion", "self.uncertain |= False",
			`git commit "$x" && git log -n 1`, 0},
		{"process execution", "if self.command.startswith((\"<(\", \">(\"), self.position):", "if False:",
			`git commit <(printf %s -n)`, 0},
		{"runtime-only message parameter", "if self.git_command and self.arguments_uncertain and not self.stack:",
			"if False:", evasionRoundTenCases()[0].command, 0},
		{"runtime-only positional array", "if self.git_command and self.arguments_uncertain and not self.stack:",
			"if False:", evasionRoundTenCases()[1].command, 0},
		{"argument uncertainty", "self.arguments_uncertain |= self.command_word_seen and self.word_splitting",
			"self.arguments_uncertain |= False", `git commit -m $(printf '') -m -n`, 0},
		{"pre-subcommand uncertainty", "self.arguments_uncertain |= self.command_word_seen and self.word_splitting",
			"self.arguments_uncertain |= bool(self.git_command) and self.word_splitting",
			evasionRoundNineCases()[0].command, 0},
		{"escaped boundary", "if not self.word_boundary():",
			"if self.position > 0 and self.command[self.position - 1] not in \" \\t\\n;|&(\":",
			`git commit -m fix\ #note -n`, 0},
	}, evasionRoundSixMutants()...)
}

// evasionRoundSixMutants builds regressions for round-six here-string, heredoc and brace defences.
func evasionRoundSixMutants() []struct {
	name, before, after, command string
	want                         int
} {
	return []struct {
		name, before, after, command string
		want                         int
	}{
		{"prefix-only here-string read", `if end < len(text) and text[end] not in " \t\n;&|<>()":`,
			`if False:`, "bash <<< 'echo '\"$name\"", 0},
		{"here-string fragments not joined",
			`r"^(?:[^ \t\n'\"\\$\x60;&|<>()]+|'[^']*'|\"(?:[^\"\\$\x60]|\\[\s\S])*\"|\\[\s\S])+"`,
			`r"^(?:[^ \t\n'\"\\$\x60;&|<>()]+|'[^']*'|\"[^\"\\$\x60]*\"|\\[^\n])+"`,
			"bash <<<'echo \"<<'\"\\\"\"", 2},
		{"here-string continuation kept", "if following == \"\\n\":\n    return \"\", True",
			"if False:\n    return \"\", True", "bash <<< \"cat <<E\\\nOF\nhello\nEOF\"", 2},
		{"tab-stripped heredoc tabs kept",
			"if document[1]:\n    body = \"\\n\".join(line.lstrip(\"\\t\") for line in body.split(\"\\n\"))",
			"if False:\n    body = \"\"",
			"bash <<-'OUTER'\n\tcat <<'INNER'\n\thello\n\tINNER\n\tOUTER", 2},
		{"leading redirection not skipped", "        if not stage[2]:", "        if True:",
			"<<'EOF' cat > notes.txt\nO'Brien <draft>\nEOF", 2},
		{"output redirection ignored", "if stage[3]:\n    return False",
			"if False:\n    return False",
			"cat <<'EOF' > notes.txt | sh\nO'Brien <draft>\nEOF", 2},
		{"every brace pair treated as splitting",
			`glob = ch in "*?[" or ch in "{}" and self.brace_splitting(ch)`,
			`glob = ch in "*?[{}"`, `git commit -m fix{docs}`, 2},
	}
}

// replaceEvasionMutant indents each continuation relative to the matched source line.
func replaceEvasionMutant(rendered, before, after string) string {
	first, _, _ := strings.Cut(before, "\n")
	offset := strings.Index(rendered, first)
	if offset < 0 {
		return rendered
	}
	if strings.Contains(before, "\n") || strings.Contains(after, "\n") {
		start := strings.LastIndex(rendered[:offset], "\n") + 1
		line, _, _ := strings.Cut(rendered[start:], "\n")
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		before = strings.ReplaceAll(before, "\n", "\n"+indent)
		after = strings.ReplaceAll(after, "\n", "\n"+indent)
	}
	return strings.Replace(rendered, before, after, 1)
}

// TestEmittedEvasionSegmentMutants proves both whole-command matching paths regress.
func TestEmittedEvasionSegmentMutants(t *testing.T) {
	python, fixed := emittedInterceptor(t)
	t.Run("compilation defence", func(t *testing.T) { testEvasionMutantCompilation(t, python) })
	t.Run("interpreter exits", testInterceptorExitCodes)
	for _, tc := range evasionSegmentMutants() {
		t.Run(tc.name, func(t *testing.T) {
			rendered := buildBlockEvasionPY()
			mutant := replaceEvasionMutant(rendered, tc.before, tc.after)
			if mutant == rendered {
				t.Fatal("mutation did not change the rendering")
			}
			script := filepath.Join(t.TempDir(), "mutant_block_evasion.py")
			mustWrite(t, script, mutant)
			if err := compileEvasionMutant(t, python, script, tc.name); err != nil {
				t.Fatal(err)
			}
			code := runInterceptor(t, python, script, nil, nil, tc.command)
			fixedCode := runInterceptor(t, python, fixed, nil, nil, tc.command)
			t.Logf("mutant hook rc=%d; fixed hook rc=%d", code, fixedCode)
			if code != tc.want {
				t.Errorf("mutant exit %d, want %d", code, tc.want)
			}
			if fixedCode != 2-tc.want {
				t.Errorf("fixed exit %d, want %d", fixedCode, 2-tc.want)
			}
		})
	}
}

// compileEvasionMutant checks Python syntax before a fixture can assert policy behaviour.
func compileEvasionMutant(t *testing.T, python, script, name string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), interceptorWallClock)
	defer cancel()
	envCtx, err := util.WithCommandEnvironment(ctx, []string{"PATH=" + os.Getenv("PATH"), "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")})
	if err != nil {
		return fmt.Errorf("mutant compiler environment: %w", err)
	}
	res, err := util.RunCommandBytes(envCtx, "", python, util.MaxCommandOutputBytes, "-m", "py_compile", script)
	if err != nil {
		return fmt.Errorf("mutant does not compile: %s: %w: %s", name, err, interceptorStderr(t, res.Stderr))
	}
	return nil
}

// testEvasionMutantCompilation plants syntax and indentation errors beside valid Python.
func testEvasionMutantCompilation(t *testing.T, python string) {
	t.Helper()
	for _, tc := range []struct {
		name, source, diagnostic string
	}{
		{"valid", "pass\n", ""},
		{"syntax", "if True\n    pass\n", "line 1"},
		{"indentation", "pass\n    pass\n", "line 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "planted.py")
			mustWrite(t, script, tc.source)
			err := compileEvasionMutant(t, python, script, tc.name)
			if tc.diagnostic == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "mutant does not compile: "+tc.name) ||
				!strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("compiler diagnostic = %v; want named refusal and %s", err, tc.diagnostic)
			}
		})
	}
}

// interceptorExitError accepts only the emitted hook's allow (0) and refusal (2) statuses.
func interceptorExitError(code int, stderr string) error {
	if code == 0 || code == 2 && !strings.Contains(stderr, "Traceback") {
		return nil
	}
	return fmt.Errorf("interceptor crashed instead of refusing: exit %d: %s", code, stderr)
}

// testInterceptorExitCodes checks allow/refuse and planted crashes without traceback text.
func testInterceptorExitCodes(t *testing.T) {
	t.Helper()
	for _, code := range []int{0, 2, 1, -1, 3} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			err := interceptorExitError(code, "IndentationError: unexpected indent")
			if code == 0 || code == 2 {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "interceptor crashed") {
				t.Fatalf("exit %d: crash diagnostic = %v", code, err)
			}
		})
	}
}

// TestEmittedEvasionRefusalMutant rejects an unrelated refusal with the correct exit status.
func TestEmittedEvasionRefusalMutant(t *testing.T) {
	python, fixed := emittedInterceptor(t)
	rendered := buildBlockEvasionPY()
	mutant := strings.Replace(rendered, `raise Blocked(reason.decode(errors="ignore"))`,
		`raise Blocked(INVALID_INPUT_REFUSAL + "unrelated refusal")`, 1)
	if mutant == rendered {
		t.Fatal("refusal mutation did not change the rendering")
	}
	script := filepath.Join(t.TempDir(), "mutant_block_evasion.py")
	mustWrite(t, script, mutant)
	const command = "git commit -n"
	code, stderr := runInterceptorOutput(t, python, script, nil, nil, command)
	fixedCode, fixedStderr := runInterceptorOutput(t, python, fixed, nil, nil, command)
	want := evasionSegmentRefusal("bare short flag")
	if code != 2 || fixedCode != 2 || strings.TrimSpace(fixedStderr) != want {
		t.Fatalf("mutant rc=%d fixed rc=%d fixed refusal=%q", code, fixedCode, fixedStderr)
	}
	if strings.TrimSpace(stderr) == want {
		t.Fatalf("unrelated refusal passed exact comparison: %q", stderr)
	}
}

// TestEmittedEvasionQueueBoundMutant proves over-budget executable work fails closed.
func TestEmittedEvasionQueueBoundMutant(t *testing.T) {
	python, _ := emittedInterceptor(t)
	const bound = "        if len(queue) > len(command) + 1:\n" +
		"            raise ValueError(\n" +
		"                f\"invalid shell command: executable span bound exceeded: {len(command) + 1}\"\n" +
		"            )\n"
	rendered := strings.Replace(buildBlockEvasionPY(), "queue.extend(nested)",
		"queue.extend([queue[0]] * (len(command) + 1))", 1)
	mutant := strings.Replace(rendered, bound, "", 1)
	if mutant == rendered {
		t.Fatal("queue-bound mutation did not change the rendering")
	}
	const command = "git commit -m x"
	for _, tc := range []struct {
		name, source string
		want         int
	}{
		{"bounded", rendered, 2}, {"unbounded", mutant, 0},
	} {
		script := filepath.Join(t.TempDir(), tc.name+".py")
		mustWrite(t, script, tc.source)
		code, stderr := runInterceptorOutput(t, python, script, nil, nil, command)
		if code != tc.want {
			t.Errorf("%s rc=%d, want %d: %s", tc.name, code, tc.want, stderr)
		}
		if tc.want == 2 && !strings.Contains(stderr, "executable span bound exceeded") {
			t.Errorf("unrelated bound refusal: %s", stderr)
		}
	}
}

// readRootedText reads a UTF-8 text file under dir through os.OpenRoot.
func readRootedText(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := openFixtureRoot(t, dir).ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("read %s/%s: %v", dir, rel, err)
	}
	return string(data)
}

// regenerateCanonicalEvasionGuard updates the shared segmentation block of a canonical guard.
func regenerateCanonicalEvasionGuard(t *testing.T, guard string) string {
	t.Helper()
	marker := "# Every match of the privilege rule"
	guard = removeGuardSegments(t, guard, marker)
	guard = strings.Replace(guard, marker, interceptorConsumers()+evasionCommandSegments+marker, 1)
	if !strings.Contains(guard, "    raw_command = command_str\n") {
		guard = strings.Replace(guard, "    command_str = join_line_continuations(command_str)",
			"    raw_command = command_str\n    command_str = join_line_continuations(command_str)", 1)
	}
	guard = strings.Replace(guard, "segments = command_segments(command_str)",
		"segments = command_segments(raw_command, READ_ONLY_EXEMPT_PATTERNS[0])", 1)
	if !strings.Contains(guard, "segments = command_segments(raw_command, READ_ONLY_EXEMPT_PATTERNS[0])") {
		guard = strings.Replace(guard, "    judged = without_read_only_words(command_str)",
			"    try:\n        segments = command_segments(raw_command, READ_ONLY_EXEMPT_PATTERNS[0])\n"+
				"    except ValueError as error:\n        sys.stderr.write(str(error) + \"\\n\")\n        return False\n"+
				"    judged = without_read_only_words(command_str)", 1)
	}
	const currentCall = "        exempt = pattern in READ_ONLY_EXEMPT_PATTERNS\n" +
		"        text = judged if exempt else command_str\n" +
		"        if search_command(\n" +
		"            pattern, text, segments, READ_ONLY_EXEMPT_PATTERNS[0], exempt\n" +
		"        ):\n" +
		"            reason = ("
	for _, oldCall := range [2]string{
		"        text = judged if pattern in READ_ONLY_EXEMPT_PATTERNS else command_str\n" +
			"        if search_command(pattern, text, segments, READ_ONLY_EXEMPT_PATTERNS[0]):\n" +
			"            reason = (",
		"        text = judged if pattern in READ_ONLY_EXEMPT_PATTERNS else command_str\n" +
			"        if search(pattern, text):\n" +
			"            reason = (",
	} {
		guard = strings.Replace(guard, oldCall, currentCall, 1)
	}
	guard = strings.Replace(guard, "    for pattern in TOPOLOGY_PATTERNS:\n        if re.search(pattern, command_str):",
		"    for pattern in TOPOLOGY_PATTERNS:\n        if search_full_inputs(pattern, command_str, segments, False):", 1)
	guard = strings.Replace(guard, "sys.stderr.write(str(error) + \"\\n\")",
		"sys.stderr.write(\""+agenthook.InvalidInputRefusal+"\" + str(error) + \"\\n\")", 1)
	return guard
}

// checkCanonicalEvasionGuard verifies that the canonical guard matches the rendered segments and table.
func checkCanonicalEvasionGuard(guard, rendered string) error {
	shared := interceptorConsumers() + evasionCommandSegments
	if !strings.Contains(guard, shared) || !strings.Contains(rendered, shared) ||
		!strings.Contains(guard, "pattern, text, segments, READ_ONLY_EXEMPT_PATTERNS[0], exempt") {
		return errors.New("engine guard segmentation differs; regenerate with PRAETOR_UPDATE_EMITTED_FIXTURES=1")
	}
	return nil
}

// TestEmittedEvasionGuardSegments regenerates shared segmentation without replacing admission.
func TestEmittedEvasionGuardSegments(t *testing.T) {
	path := filepath.Join("..", "..", filepath.FromSlash(evasionHookFile))
	guard := readRootedText(t, filepath.Join("..", ".."), evasionHookFile)
	guard = strings.ReplaceAll(guard, "\r\n", "\n")
	if os.Getenv(updateEmittedFixturesEnv) == "1" {
		recordMinimalPythonRendering(t)
		guard = regenerateCanonicalEvasionGuard(t, guard)
		mustWrite(t, path, guard)
		TestEmittedHookFixturesMatchTheRendering(t)
		current := readRootedText(t, priorEvasionFixtures, currentEvasionFixture)
		if !strings.Contains(current, "def command_segments(") {
			mustWrite(t, filepath.Join(priorEvasionFixtures, "before-command-segments.block_evasion.py.txt"), current)
		}
	}
	if err := checkCanonicalEvasionGuard(guard, buildBlockEvasionPY()); err != nil {
		t.Fatal(err)
	}
}

// removeGuardSegments removes the previous shared parser before regeneration.
func removeGuardSegments(t *testing.T, guard, marker string) string {
	t.Helper()
	start := strings.Index(guard, "HEREDOC_CONSUMERS = {\n")
	if start < 0 {
		start = strings.Index(guard, "def commit_command(")
	}
	if start < 0 {
		start = strings.Index(guard, "def command_segments(")
	}
	if start < 0 {
		return guard
	}
	end := strings.Index(guard[start:], marker)
	if end < 0 {
		t.Fatal("engine guard segment boundary missing")
	}
	return guard[:start] + guard[start+end:]
}

// interceptorModule is what the emitted script declares at module level, as Python reads it
// back from the adjacent literals the layout splits long values into: RULES as (pattern,
// refusal) pairs and the read-only exemption.
type interceptorModule struct {
	Rules      [][2]string         `json:"RULES"`
	Prefilters map[string][]string `json:"RULE_PREFILTERS"`
	Exempt     []string            `json:"READ_ONLY_EXEMPT"`
	Words      string              `json:"READ_ONLY_WORDS"`
	Veto       string              `json:"READ_ONLY_VETO"`
	Consumers  map[string]string   `json:"HEREDOC_CONSUMERS"`
}

// loadInterceptorModule loads the emitted script as a module, without running main.
func loadInterceptorModule(t *testing.T, python, script string) interceptorModule {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), interceptorWallClock)
	defer cancel()
	const program = "import json, runpy, sys; module = runpy.run_path(sys.argv[1]); " +
		`module.setdefault("RULE_PREFILTERS", {}); module.setdefault("HEREDOC_CONSUMERS", {}); ` +
		"print(json.dumps({name: module[name] for name in sys.argv[2:]}))"
	names := []string{"RULES", "READ_ONLY_EXEMPT", "READ_ONLY_WORDS", "READ_ONLY_VETO", "RULE_PREFILTERS", "HEREDOC_CONSUMERS"}
	args := append([]string{"-B", "-c", program, script}, names...)
	res, err := util.RunCommandBytes(ctx, "", python, util.MaxCommandOutputBytes, args...)
	if err != nil {
		t.Fatalf("load the emitted script as a module: %v", err)
	}
	var module interceptorModule
	if err := json.Unmarshal(res.Stdout, &module); err != nil {
		t.Fatalf("decode the emitted script's module values: %v", err)
	}
	return module
}

// interceptorRules returns the RULES of the emitted script (loadInterceptorModule).
func interceptorRules(t *testing.T, python, script string) [][2]string {
	t.Helper()
	return loadInterceptorModule(t, python, script).Rules
}

// The emitted interceptor carries every declared prefilter from the Go policy
// without changing any pattern or refusal in the previous unfiltered rendering.
func TestInterceptorPrefiltersMatchTheGoPolicy(t *testing.T) {
	python, script := emittedInterceptor(t)
	want := map[string][]string{}
	for _, rule := range agenthook.BuiltinRules() {
		if len(rule.RequiredAny) > 0 {
			want[rule.Source] = rule.RequiredAny
		}
	}
	current := loadInterceptorModule(t, python, script)
	previous := renderedModule(t, python, string(readFixtureDir(t, priorEvasionFixtures)["unfiltered-privilege.block_evasion.py"]))
	if !reflect.DeepEqual(current.Rules, previous.Rules) {
		t.Fatal("prefilter changed the rule text or refusals")
	}
	if !reflect.DeepEqual(current.Prefilters, want) || len(current.Prefilters) == 0 {
		t.Errorf("prefilters %v, want %v", current.Prefilters, want)
	}
}

// wrappedListItems returns the lines of the named list in a rendered interceptor that are not
// one bare name and a comma. A pattern too long for one line is laid out as adjacent literals,
// and inside a list that reads as two items with the comma between them forgotten: CodeQL
// reports it in every adopter (py/implicit-string-concatenation-in-list, #1219).
func wrappedListItems(t *testing.T, rendering, list string) []string {
	t.Helper()
	_, rest, found := strings.Cut(rendering, "\n"+list+" = [\n")
	body, _, closed := strings.Cut(rest, "]\n")
	if !found || !closed || body == "" {
		t.Fatalf("the rendering has no %s list", list)
	}
	var wrapped []string
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if !bareNameItem.MatchString(line) {
			wrapped = append(wrapped, line)
		}
	}
	return wrapped
}

// bareNameItem matches one list item that is a module-level name alone.
var bareNameItem = regexp.MustCompile(`^    [A-Z][A-Z0-9_]*,$`)

// TestEmittedInterceptorCarriesTheReadOnlyExemption holds the read-only exemption the
// interceptor declares to the engine's, as Python reads it: the exempt rule sources in order,
// the read-only command pattern and the veto. Positive: every pattern text is the engine's, so
// naming the exempt patterns outside the list changed no pattern. Boundary: the list itself
// holds names alone. Negative: the same check finds the wrapped pattern in the rendering
// before #1219, so it can fail.
func TestEmittedInterceptorCarriesTheReadOnlyExemption(t *testing.T) {
	python, script := emittedInterceptor(t)
	module := loadInterceptorModule(t, python, script)
	var exempt []string
	for _, rule := range agenthook.BuiltinRules() {
		if rule.ReadOnlyExempt {
			exempt = append(exempt, rule.Source)
		}
	}
	if len(exempt) == 0 || !slices.Equal(module.Exempt, exempt) {
		t.Errorf("READ_ONLY_EXEMPT is %q, the engine's exempt rules are %q", module.Exempt, exempt)
	}
	if module.Words != agenthook.ReadOnlyWords || module.Veto != agenthook.ReadOnlyVeto {
		t.Errorf("READ_ONLY_WORDS %q and READ_ONLY_VETO %q are not the engine's", module.Words, module.Veto)
	}
	if wrapped := wrappedListItems(t, buildBlockEvasionPY(), "READ_ONLY_EXEMPT"); len(wrapped) != 0 {
		t.Errorf("READ_ONLY_EXEMPT holds items that are not one name: %q", wrapped)
	}
	previous := string(readFixtureDir(t, priorEvasionFixtures)[wrappedExemptFixture])
	if wrapped := wrappedListItems(t, previous, "READ_ONLY_EXEMPT"); len(wrapped) == 0 {
		t.Errorf("the check found no wrapped pattern in %s, which holds one", wrappedExemptFixture)
	}
}

// wrappedExemptFixture and namedExemptFixture are the renderings on either side of #1219: the
// last one that wrapped an exempt pattern inside the list and anchored the veto with a caret,
// and the first one that names each exempt pattern and anchors with \A.
const (
	wrappedExemptFixture = "first-word-anchor.block_evasion.py"
	namedExemptFixture   = "scanner-clean.block_evasion.py"
)

// renderedModule loads the text of one interceptor rendering as a module.
func renderedModule(t *testing.T, python, text string) interceptorModule {
	t.Helper()
	script := filepath.Join(t.TempDir(), "block_evasion.py")
	mustWrite(t, script, text)
	return loadInterceptorModule(t, python, script)
}

// TestScannerCleanRenderingKeptThePolicy compares the renderings on either side of #1219, both
// kept as earlier texts, as Python reads them. Positive: the rules, the exempt patterns and
// the read-only command pattern are the same texts. Boundary: the veto differs in its start
// anchor and in nothing else. Negative: one character less in an exempt pattern is a
// difference, so the comparison can fail.
func TestScannerCleanRenderingKeptThePolicy(t *testing.T) {
	python, _ := emittedInterceptor(t)
	fixtures := readFixtureDir(t, priorEvasionFixtures)
	before := renderedModule(t, python, string(fixtures[wrappedExemptFixture]))
	after := renderedModule(t, python, string(fixtures[namedExemptFixture]))
	if len(before.Exempt) == 0 || !slices.Equal(before.Exempt, after.Exempt) {
		t.Errorf("an exempt pattern changed:\nbefore %q\nafter  %q", before.Exempt, after.Exempt)
	}
	if !reflect.DeepEqual(before.Rules, after.Rules) || before.Words != after.Words {
		t.Errorf("the rules or the read-only command pattern changed between %s and %s", wrappedExemptFixture, namedExemptFixture)
	}
	if anchored := strings.Replace(before.Veto, "(?:^|", `(?:\A|`, 1); anchored == before.Veto || anchored != after.Veto {
		t.Errorf("the veto changed beyond its start anchor:\nbefore %q\nafter  %q", before.Veto, after.Veto)
	}
	const exempt = `READ_ONLY_EXEMPT_2 = r"SKIP=.*git"`
	shortened := strings.Replace(string(fixtures[namedExemptFixture]), exempt, strings.TrimSuffix(exempt, `t"`)+`"`, 1)
	if planted := renderedModule(t, python, shortened); slices.Equal(before.Exempt, planted.Exempt) {
		t.Errorf("a shortened exempt pattern passed the comparison: %q", planted.Exempt)
	}
}

// runInterceptor feeds stdin (and optional argv) to the emitted script under a clean
// environment plus extra, and returns its exit code.
func runInterceptor(t *testing.T, python, script string, stdin []byte, extra []string, args ...string) int {
	t.Helper()
	code, _ := runInterceptorOutput(t, python, script, stdin, extra, args...)
	return code
}

// interceptorWallClock is the ceiling on one interceptor run, a hook timeout's worth of
// wall-clock time. The scan-bound test bounds the matcher's work instead (scanPassBudget).
const interceptorWallClock = 30 * time.Second

// runInterceptorOutput runs the emitted interceptor and returns its exit code and its stderr
// as LF text.
func runInterceptorOutput(t *testing.T, python, script string, stdin []byte, extra []string, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), interceptorWallClock)
	defer cancel()
	envCtx, err := util.WithCommandEnvironment(ctx, append([]string{"PATH=" + os.Getenv("PATH"), "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}, extra...))
	if err != nil {
		t.Fatalf("environment: %v", err)
	}
	stdinCtx, err := util.WithCommandStdin(envCtx, stdin)
	if err != nil {
		t.Fatalf("stdin: %v", err)
	}
	cmdArgs := append([]string{"-B", script}, args...)
	res, err := util.RunCommandBytes(stdinCtx, "", python, util.MaxCommandOutputBytes, cmdArgs...)
	stderr := res.Stderr
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if err := interceptorExitError(exitErr.ExitCode(), string(stderr)); err != nil {
			t.Fatal(err)
		}
		return exitErr.ExitCode(), interceptorStderr(t, stderr)
	}
	if err != nil {
		t.Fatalf("run interceptor: %v", err)
	}
	return 0, interceptorStderr(t, stderr)
}

// interceptorStderr reads the interceptor's stderr as text (testsupport.PythonText): CPython
// writes it with CRLF line endings on Windows, and the engine's refusals end in LF.
func interceptorStderr(t *testing.T, stderr []byte) string {
	t.Helper()
	text, err := testsupport.PythonText(stderr)
	if err != nil {
		t.Errorf("interceptor stderr %q: %v", stderr, err)
		return string(stderr)
	}
	return string(text)
}

// TestEmittedInterceptorReplaysTheEngineCorpus pins BUG-807 and BUG-808: the emitted script
// carries the engine's rules, so it allows and denies what the engine does, and it refuses
// every malformed shape with the blocking exit 2. Operator rules are repository data and
// are not shipped, so an operator-only case is allowed here.
func TestEmittedInterceptorReplaysTheEngineCorpus(t *testing.T) {
	python, script := emittedInterceptor(t)
	data, err := os.ReadFile(evasionCorpus)
	if err != nil {
		t.Fatal(err)
	}
	var cases []evasionCase
	if err := json.Unmarshal(data, &cases); err != nil || len(cases) < 60 {
		t.Fatalf("corpus unreadable or shrunk: %d cases, %v", len(cases), err)
	}
	for _, tc := range cases {
		want := 2
		if tc.Allow || tc.Operator {
			want = 0
		}
		if got := runInterceptor(t, python, script, tc.Payload, nil); got != want {
			t.Errorf("%s: exit %d, want %d", tc.Name, got, want)
		}
	}
}

// TestEmittedInterceptorRefusesUnreadableInput covers the byte-level shapes JSON cannot
// express: each used to be allowed or to crash with the non-blocking exit 1.
func TestEmittedInterceptorRefusesUnreadableInput(t *testing.T) {
	python, script := emittedInterceptor(t)
	valid := []byte(`{"tool_input":{"command":"git status"}}`)
	boundary := append(bytes.Clone(valid), bytes.Repeat([]byte(" "), agenthook.MaxInputBytes-len(valid))...)
	for name, tc := range map[string]struct {
		stdin []byte
		want  int
	}{
		"empty stdin":                {nil, 2},
		"raw text":                   {[]byte("git status"), 2},
		"JSON array":                 {[]byte(`[{"tool_input":{"command":"git status"}}]`), 2},
		"cursor shape":               {[]byte(`{"command":"git status","cwd":"/w"}`), 2},
		"invalid UTF-8":              {[]byte{0xff}, 2},
		"deeply nested":              {append(bytes.Repeat([]byte("["), 5000), bytes.Repeat([]byte("]"), 5000)...), 2},
		"exactly one mebibyte":       {boundary, 0},
		"one mebibyte plus one":      {append(bytes.Clone(boundary), ' '), 2},
		"denial with trailing space": {append([]byte(`{"tool_input":{"command":"lefthook uninstall"}}`), bytes.Repeat([]byte(" "), 16)...), 2},
	} {
		if got := runInterceptor(t, python, script, tc.stdin, nil); got != tc.want {
			t.Errorf("%s: exit %d, want %d", name, got, tc.want)
		}
	}
}

// TestEmittedInterceptorEnvironmentAndArguments covers the environment check, rendered from
// agenthook's lists, and the argv entry point.
func TestEmittedInterceptorEnvironmentAndArguments(t *testing.T) {
	python, script := emittedInterceptor(t)
	benign := []byte(`{"tool_input":{"command":"go test ./..."}}`)
	for variable, want := range map[string]int{
		"LEFTHOOK=0": 2, "LEFTHOOK=false": 2, "LEFTHOOK=1": 0, "LEFTHOOK=False": 0,
		"LEFTHOOK_EXCLUDE=lint": 2, "LEFTHOOK_SKIP=pre-push": 2, "LEFTHOOK_VERBOSE=1": 0,
	} {
		if got := runInterceptor(t, python, script, benign, []string{variable}); got != want {
			t.Errorf("%s: exit %d, want %d", variable, got, want)
		}
	}
	if got := runInterceptor(t, python, script, nil, nil, "rm", "-rf", ".git/hooks"); got != 2 {
		t.Errorf("argv denial: exit %d", got)
	}
	if got := runInterceptor(t, python, script, nil, nil, "git", "status"); got != 0 {
		t.Errorf("argv allow: exit %d", got)
	}
}

// TestBuildBlockEvasionPY_NoOperatorData asserts the rendered script carries every built-in
// rule and no organisation-folder alternation (ADR-0011: operator data is configured, never
// shipped as engine rules). praetor's own guard names none either
// (TestPythonGuardCarriesTheBuiltinDevRootRule in internal/agenthook).
func TestBuildBlockEvasionPY_NoOperatorData(t *testing.T) {
	python, path := emittedInterceptor(t)
	rules := interceptorRules(t, python, path)
	builtins := agenthook.BuiltinRules()
	if len(rules) != len(builtins) {
		t.Fatalf("rendered script carries %d rules, the engine %d", len(rules), len(builtins))
	}
	for i, rule := range builtins {
		if rules[i][0] != rule.Source || !strings.HasPrefix(rules[i][1], "[BLOCKED BY "+rule.Invariant+"] ") {
			t.Errorf("rule %d: rendered %q, engine %q (%s)", i, rules[i], rule.Source, rule.Invariant)
		}
	}
	script := buildBlockEvasionPY()
	for _, operator := range []string{"TOPOLOGY_PATTERNS", "/dev/("} {
		if strings.Contains(script, operator) {
			t.Errorf("rendered script ships operator data %q", operator)
		}
	}
	if strings.Contains(script, "{{") {
		t.Error("rendered script has an unfilled placeholder")
	}
}

// scanPassBudget bounds the work the emitted interceptor's matcher may spend on one command
// inside the scan bounds, in passes. One pass is what the matcher spends on a benign command
// at the bound (benignBoundCommand), every rule reading it once. Both are measured in the same
// run on the same interpreter (scanCostProbe), so the budget means the same work on every
// runner; a fixed CPU-time budget of 3 s failed on a hosted runner about 3.3 times slower than
// a workstation (#829). The costliest shapes here, the find lines and `$` runs before a brace
// after `git am`, cost about 10 passes; a rule that backtracks superlinearly through the bound
// costs hundreds (TestScanPassBudgetRefusesPlantedSuperlinearMatchers).
const scanPassBudget = 50

// scanCostProbe measures the emitted interceptor's matcher in its own process. It loads the
// script as a module, so main does not run, and times check_command with CPU time: the pass
// on the reference command in at least three batches of at least 0.2 s each (or runs where
// the probe allows), keeping the cheapest; then each command `runs` times, keeping the least
// time with the verdict (2 for a refusal). The least reading is the one Python's timeit
// documentation recommends: higher readings come from interference, not from the code.
const scanCostProbe = `import json, runpy, sys, time

module = runpy.run_path(sys.argv[1])
check, blocked = module["check_command"], module["Blocked"]
request = json.loads(sys.stdin.buffer.read().decode("utf-8"))


def timed(command):
    start = time.process_time()
    try:
        check(command)
    except blocked:
        return time.process_time() - start, 2
    return time.process_time() - start, 0


def one_pass(command, runs=3):
    best = None
    for _ in range(runs):
        start, count = time.process_time(), 0
        while count < 100000:
            timed(command)
            count += 1
            spent = time.process_time() - start
            if spent >= 0.2:
                break
        best = spent / count if best is None else min(best, spent / count)
    return best


def least(command, runs):
    readings = [timed(command) for _ in range(runs)]
    return [min(reading[0] for reading in readings), readings[0][1]]


costs = [least(command, request["runs"]) for command in request["commands"]]
print(json.dumps({"pass": one_pass(request["reference"], max(3, request["runs"])), "costs": costs}))
`

// scanCosts is what scanCostProbe reports: the CPU seconds of one pass and, per command, the
// least CPU seconds over its runs and its verdict.
type scanCosts struct {
	Pass  float64      `json:"pass"`
	Costs [][2]float64 `json:"costs"`
}

// passes is the cost of command index in passes.
func (c scanCosts) passes(index int) float64 { return c.Costs[index][0] / c.Pass }

// measureScanCosts runs scanCostProbe against script for the commands, each timed runs times.
func measureScanCosts(t *testing.T, python, script string, commands []string, runs int) scanCosts {
	t.Helper()
	input, err := json.Marshal(map[string]any{"reference": benignBoundCommand(), "commands": commands, "runs": runs})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*interceptorWallClock)
	defer cancel()
	stdinCtx, err := util.WithCommandStdin(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	res, err := util.RunCommandBytes(stdinCtx, "", python, util.MaxCommandOutputBytes, "-B", "-c", scanCostProbe, script)
	if err != nil {
		t.Fatalf("measure the interceptor's scan costs: %v", err)
	}
	var costs scanCosts
	if err := json.Unmarshal(res.Stdout, &costs); err != nil || costs.Pass <= 0 || len(costs.Costs) != len(commands) {
		t.Fatalf("scan cost probe output %q: %v", string(res.Stdout), err)
	}
	return costs
}

// scanShape is one command the scan-bound tests send and the verdict the interceptor owes it.
type scanShape struct {
	name    string
	command string
	want    int
}

// scanBudgetFindings returns one line per shape whose verdict differs from its want or whose
// cost exceeds scanPassBudget passes.
func scanBudgetFindings(shapes []scanShape, costs scanCosts) []string {
	var findings []string
	for index, shape := range shapes {
		if verdict := int(costs.Costs[index][1]); verdict != shape.want {
			findings = append(findings, fmt.Sprintf("%s: verdict %d, want %d", shape.name, verdict, shape.want))
		}
		if passes := costs.passes(index); passes > scanPassBudget {
			findings = append(findings, fmt.Sprintf("%s: %.1f passes (%.3f s of CPU time, one pass %.4f s), over the budget of %d",
				shape.name, passes, costs.Costs[index][0], costs.Pass, scanPassBudget))
		}
	}
	return findings
}

// boundLine is prefix followed by unit repeated, cut to width characters.
func boundLine(prefix, unit string, width int) string {
	return (prefix + strings.Repeat(unit, width/len(unit)+1))[:width]
}

// boundLines fills the scan bound with lines of unit repeated, each one character short of
// agenthook.MaxScanLineChars so the line break fits.
func boundLines(unit string) string {
	return boundLinesWithFrame("", unit, "")
}

// boundLinesWithFrame fills the scan bound with framed lines, including their closing suffix.
func boundLinesWithFrame(prefix, unit, suffix string) string {
	line := boundLine(prefix, unit, agenthook.MaxScanLineChars-1-len(suffix)) + suffix + "\n"
	return strings.Repeat(line, agenthook.MaxScanChars/agenthook.MaxScanLineChars)
}

// benignBoundCommand fills the scan bound with lines of x: every rule reads it once and none
// matches. Its cost is the pass scanPassBudget counts in.
func benignBoundCommand() string { return boundLines("x") }

// scanBoundShapes are the commands TestEmittedInterceptorScanBounds sends: the line and command
// bounds, exactly at and one over, and the costliest shapes measured against the rules, each
// filling the bound. Those are the find line and lines of `sed -i `, which cost cubic time
// before the two rules started at the first command word of a line, and `$` runs before a
// brace inside an open double quote after `git am` and after SKIP= (the costliest shapes the
// reviews of an earlier quote-aware scan measured). Git am dollar runs remain literal printf
// data, so argument uncertainty cannot short-circuit the matcher workload. A line break turns
// the read-only exemption off for the skip-variable rule (agenthook.ReadOnlyVeto). The short-flag rule now scans each
// outer command independently, including across newlines. Single-line shapes at the line
// bound include read-only command words, separators back to back, a
// separator before blanks, a Git name before blanks.
//
// Git global options repeated to the line bound (` -git`, ` --a=git`, ` -C /git`) put a git
// word inside every option of a chain. The commit rule restarted at each one and read the rest
// of the chain again, quadratic in the line, about 300 passes; no anchor keeps that rule's
// language, so the interceptor's search walks each option run once instead (#1283).
func scanBoundShapes() []scanShape {
	width := agenthook.MaxScanLineChars
	blanks := strings.Repeat(" \t", width)
	full := benignBoundCommand()
	return []scanShape{
		{"pathological find line", strings.Repeat("find .git/hooks ", 1024), 2},
		{"line at the bound", strings.Repeat("x", width), 0},
		{"line over the bound", strings.Repeat("x", width+1), 2},
		{"command at the bound", full, 0},
		{"command over the bound", full + "x", 2},
		{"find lines", boundLines("find .git/hooks "), 0},
		{"in-place edit lines", boundLines("sed -i "), 0},
		{"git option chains", boundLines(" -git"), 0},
		{"git option values naming git", boundLines(" --a=git"), 0},
		{"git directory options naming git", boundLines(" -C /git"), 0},
		{"am before odd dollar runs", boundLinesWithFrame(`printf '%s' '`, `git am "$$$$$$$$$${`, `'`), 0},
		{"am before even dollar run", boundLinesWithFrame(`printf '%s' '`, `git am "$$$${`, `'`), 0},
		{"skip variable dollar runs", boundLines(`SKIP="$$$$$${`), 0},
		{"read-only words to the end", boundLine("git commit -m x; git log", " -n", width), 0},
		{"read-only words on lines", boundLines("git commit -m x; git log -n 5 "), 0},
		{"separators back to back", boundLine("", "git commit -m x || ", width), 0},
		{"separator before blanks", boundLine("git commit -m x;", blanks, width-1) + "x", 0},
		{"read-only name after blanks", boundLine("git commit -m x;git", blanks, width-1) + "x", 0},
	}
}

// TestEmittedInterceptorScanBounds pins the scan bounds of the emitted script. Python's re
// backtracks: one 16 KiB find..hooks line held the find rule for 16 s, past a harness's hook
// timeout, and a harness that lets a timed-out hook through turns the stall into an evasion.
// A command over agenthook's bounds is refused at once, and each scanBoundShapes command gets
// its verdict, end to end through stdin and in the matcher's own process, within
// scanPassBudget passes. The bound counts work, not seconds, so a slower or loaded runner does
// not fail it; the wall-clock ceilings are the runs' contexts.
func TestEmittedInterceptorScanBounds(t *testing.T) {
	python, script := emittedInterceptor(t)
	shapes := scanBoundShapes()
	commands := make([]string, 0, len(shapes))
	for _, shape := range shapes {
		payload, err := json.Marshal(map[string]any{"tool_input": map[string]string{"command": shape.command}})
		if err != nil {
			t.Fatal(err)
		}
		if got := runInterceptor(t, python, script, payload, nil); got != shape.want {
			t.Errorf("%s: exit %d, want %d", shape.name, got, shape.want)
		}
		commands = append(commands, shape.command)
	}
	costs := measureScanCosts(t, python, script, commands, 3)
	for index, shape := range shapes {
		t.Logf("%s: %.1f passes", shape.name, costs.passes(index))
	}
	for _, finding := range scanBudgetFindings(shapes, costs) {
		t.Error(finding)
	}
}

// plantedInterceptor adds pattern after declarations have bound the prefilter keys,
// so inserting the plant does not change which real rule each prefilter belongs to.
func plantedInterceptor(t *testing.T, pattern string) string {
	t.Helper()
	rendered := buildBlockEvasionPY()
	planted := strings.Replace(rendered, "\nclass Blocked(Exception):", "\nRULES.insert(0, (r\""+pattern+"\", HISS_REFUSAL))\n\nclass Blocked(Exception):", 1)
	if planted == rendered {
		t.Fatal("the rendered interceptor has no Blocked class before which to plant a rule")
	}
	return planted
}

// TestScanPassBudgetRefusesPlantedSuperlinearMatchers proves the budget (rule 13): the same
// measurement refuses an interceptor with a superlinear rule planted ahead of the real ones,
// on the shape that rule backtracks through, a shape the real interceptor answers within the
// budget (TestEmittedInterceptorScanBounds). The plants are a nested quantifier over the
// dollar runs, which tries every way to split each run, and the find rule as it stood before
// #829, cubic in the line length. Neither matches its shape, so the verdict stays an allow.
func TestScanPassBudgetRefusesPlantedSuperlinearMatchers(t *testing.T) {
	python, _ := emittedInterceptor(t)
	shapes := map[string]scanShape{}
	for _, shape := range scanBoundShapes() {
		shapes[shape.name] = shape
	}
	// On a loaded CI Linux runner (run 39938932140), the ten-dollar run measured 48.9 passes,
	// failing scanPassBudget (50). A twelve-dollar run inside the scan bound holds a runner-proof
	// margin (around 750 passes on host, >= 10x budget) against runner interference.
	shapes["planted dollar runs"] = scanShape{
		name:    "planted dollar runs",
		command: boundLinesWithFrame(`printf '%s' '`, `git am "$$$$$$$$$$$${`, `'`),
		want:    0,
	}
	// The find plant on find lines held 128 to 131 passes on host but dropped to 89.2 on
	// a loaded runner (1.8x budget). A shape with 135 find words, 115 .git/hooks paths and
	// trailing space-dash pairs inside the scan bound measured 244 to 253 passes on host
	// (about five budgets), so the runner drop seen on the dollar plant (about 3x) still
	// leaves it above the budget.
	shapes["planted find lines"] = scanShape{
		name: "planted find lines",
		command: boundLines(strings.Repeat("find ", 135) +
			strings.Repeat(".git/hooks", 115) +
			strings.Repeat(" -", 111)),
		want: 0,
	}
	for _, tc := range []struct{ name, pattern, shape string }{
		{"nested quantifier over the dollar runs", `(\$+\$?)+\{x`, "planted dollar runs"},
		{"find rule before #829", `\bfind\b[^\n]*(?i:\.git[/\\]hooks)[^\n]*\s-(delete|exec|execdir|ok)\b`, "planted find lines"},
	} {
		script := filepath.Join(t.TempDir(), "planted_block_evasion.py")
		mustWrite(t, script, plantedInterceptor(t, tc.pattern))
		shape := shapes[tc.shape]
		costs := measureScanCosts(t, python, script, []string{shape.command}, 1)
		findings := scanBudgetFindings([]scanShape{shape}, costs)
		t.Logf("%s on %s: %.1f passes (plant %.4f s, pass %.6f s)", tc.name, tc.shape, costs.passes(0), costs.Costs[0][0], costs.Pass)
		// Plant the original execution path too: removing only the prefilter
		// inflates the benign denominator and hides matcher work from the budget.
		unfiltered := filepath.Join(t.TempDir(), "unfiltered_block_evasion.py")
		mustWrite(t, unfiltered, plantedInterceptor(t, tc.pattern)+"\nRULE_PREFILTERS.clear()\n")
		old := measureScanCosts(t, python, unfiltered, []string{shape.command}, 1)
		t.Logf("without prefilter: %s on %s: %.1f passes (plant %.4f s, pass %.6f s)", tc.name, tc.shape, old.passes(0), old.Costs[0][0], old.Pass)
		if int(old.Costs[0][1]) != shape.want {
			t.Errorf("unfiltered control changed the verdict: %v", old.Costs[0])
		}
		if len(findings) != 1 || !strings.Contains(findings[0], "over the budget") {
			t.Errorf("%s: the budget did not refuse the planted rule on %s: %q", tc.name, tc.shape, findings)
		}
	}
}

// TestEmittedInterceptorRefusesInTheEngineWords pins BUG-1014 for the interceptor adoption
// renders: each refusal prints agenthook's text character for character, for an evasion flag,
// the dev-root rule, a command over the scan bound, a disabled or narrowed Lefthook run and
// unreadable input (whose parser error after the shared prefix is Python's own). A command
// exactly at the scan bound and a benign one print nothing.
func TestEmittedInterceptorRefusesInTheEngineWords(t *testing.T) {
	python, script := emittedInterceptor(t)
	builtin, err := agenthook.NewPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := func(command string) []byte {
		data, err := json.Marshal(map[string]any{"tool_input": map[string]string{"command": command}})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	benign := payload("go test ./...")
	for _, tc := range []struct {
		name  string
		stdin []byte
		env   []string
		want  string
	}{
		{"evasion flag", payload("git push --no-verify"), nil, builtin.Command("git push --no-verify").Reason + "\n"},
		{"privilege refusal bounded", payload("sudo -n true"), nil, builtin.Command("sudo -n true").Reason + "\n"},
		{"dev root", payload("praetorctl adopt /srv/dev"), nil, builtin.Command("praetorctl adopt /srv/dev").Reason + "\n"},
		{"scan bound exceeded", payload(strings.Repeat("x", agenthook.MaxScanLineChars+1)), nil, agenthook.ScanBoundRefusal() + "\n"},
		{"scan bound exactly", payload(strings.Repeat("x", agenthook.MaxScanLineChars)), nil, ""},
		{"benign", benign, nil, ""},
		{"lefthook disabled", benign, []string{"LEFTHOOK=false"}, agenthook.LefthookDisabledRefusal("false") + "\n"},
		{"lefthook narrowed", benign, []string{"LEFTHOOK_SKIP=lint"}, agenthook.NarrowingRefusal + "\n"},
	} {
		code, stderr := runInterceptorOutput(t, python, script, tc.stdin, tc.env)
		if stderr != tc.want || (code == 0) != (tc.want == "") {
			t.Errorf("%s: exit %d\ngot  %q\nwant %q", tc.name, code, stderr, tc.want)
		}
	}
	if _, stderr := runInterceptorOutput(t, python, script, []byte("not json"), nil); !strings.HasPrefix(stderr, agenthook.InvalidInputRefusal) {
		t.Errorf("unreadable input: %q lacks the engine's prefix", stderr)
	}
}

// TestEmittedInterceptorRefusesInTheEngineWordsUnderTheWindowsNewline runs the emitted
// interceptor with CPython's Windows newline translation on its standard streams
// (testsupport.WindowsNewlinePython), so a run on any host shows what the Windows leg sees:
// a refusal still reads as the engine's text, and an allowed command still prints nothing.
func TestEmittedInterceptorRefusesInTheEngineWordsUnderTheWindowsNewline(t *testing.T) {
	python, script := emittedInterceptor(t)
	launcher := testsupport.WindowsNewlinePython(t, script)
	builtin, err := agenthook.NewPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	benign := []byte(`{"tool_input":{"command":"go test ./..."}}`)
	for _, tc := range []struct {
		name  string
		stdin []byte
		env   []string
		want  string
	}{
		{"evasion flag", []byte(`{"tool_input":{"command":"git push --no-verify"}}`), nil, builtin.Command("git push --no-verify").Reason + "\n"},
		{"lefthook narrowed", benign, []string{"LEFTHOOK_SKIP=lint"}, agenthook.NarrowingRefusal + "\n"},
		{"benign", benign, nil, ""},
	} {
		code, stderr := runInterceptorOutput(t, python, launcher, tc.stdin, tc.env)
		if stderr != tc.want || (code == 0) != (tc.want == "") {
			t.Errorf("%s: exit %d\ngot  %q\nwant %q", tc.name, code, stderr, tc.want)
		}
	}
}

// TestEmittedInterceptorSearchesLikeTheEngineGuard pins #1283: the interceptor adoption renders
// carries the engine guard's option-run search byte for byte and judges every rule through it,
// so an adopter's interceptor walks a git option run once, as praetor's own guard does.
func TestEmittedInterceptorSearchesLikeTheEngineGuard(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(evasionHookFile)))
	if err != nil {
		t.Fatal(err)
	}
	// A core.autocrlf checkout writes the guard with CRLF; the rendering is LF (#1369).
	guard, _ := util.NormalizeLineEndings(string(data))
	start, end := strings.Index(guard, "# The git rule with an option run"), strings.Index(guard, "def audit_command")
	if start < 0 || end <= start {
		t.Fatalf("the engine guard has no option-run search before audit_command")
	}
	rendered := buildBlockEvasionPY()
	if !strings.Contains(rendered, guard[start:end]) {
		t.Fatal("the rendered interceptor does not carry the engine guard's option-run search byte for byte")
	}
	if !strings.Contains(rendered, "if search(pattern, text):") || strings.Contains(rendered, "        if re.search(pattern, text):\n") {
		t.Fatal("the rendered interceptor does not judge its rules through search")
	}
}

// evasionDelimiterBraceCases covers exact heredoc words, body continuations and unknown brace arguments.
func evasionDelimiterBraceCases() []struct {
	name, command string
	want          int
} {
	return append([]struct {
		name, command string
		want          int
	}{
		{"brace continuation", "git commit -m {message,\\\n-n}", 2},
		{"partial delimiter", "cat <<'true'X\ndata\ntrueX\ngit commit -n -m msg\ntrue", 2},
		{"literal delimiter quotes", "cat <<\"'true'\"\ndata\n'true'\ngit commit -n -m msg\ntrue", 2},
		{"body continuation", "cat <<true\ntr\\\nue\ngit commit -n -m msg\ntrue", 2},
		{"skip control", "git commit -n -m msg", 2},
		{"message control", "git commit -m msg", 0},
		{"unsupported delimiter expansion", "cat <<$end\ndata\n$end\ngit commit -m msg", 2},
		{"unterminated delimiter quote", "cat <<'EOF\ngit commit -m msg", 2},
		{"unknown brace word", "git commit -m {msg,other}", 2},
		{"unterminated brace word", "git commit -m {msg,-n", 2},
		{"quoted brace word", "git commit -m '{msg,-n}'", 0},
		{"escaped delimiter", "git commit -F - <<\\EOF\n-n\nEOF", 0},
		{"quoted body continuation", "cat <<'true'\ntr\\\nue\ntrue\ngit commit -m msg", 0},
		{"even body escapes", "cat <<true\ntr\\\\\nue\ntrue\ngit commit -m msg", 0},
		{"continued delimiter data", "git commit -F - <<EOF\ntext\\\nEOF\n-n\nEOF", 0},
		{"continued delimiter tabs", "git commit -F - <<-EOF\n\tE\\\nOF\ngit commit -n\nEOF", 2},
		{"brace subcommand", `git {commit,-n} -m msg`, 2},
		// codespell:ignore-next-line ommit
		{"partial brace subcommand", `git c{ommit,-n} -m msg`, 2},
		{"brace command", `{git,commit,-n,-m,msg}`, 2},
		{"partial brace command", `g{it,} commit -n -m msg`, 2},
		{"quoted brace subcommand", `git '{commit,-n}' -m msg`, 0},
		{"quoted brace message", `git commit -m '{a,b}'`, 0},
		{"non git brace operand", `echo {a,b}`, 0},
		// codespell:ignore-next-line ommit
		{"brace subcommand after option", `git -C . c{ommit,-n} -m msg`, 2},
		{"brace option value", `git -C {.,other} commit -m msg`, 2},
		{"quoted brace option value", `git -C '{.,other}' commit -m msg`, 0},
		{"double quoted brace subcommand", `git "{commit,-n}" -m msg`, 0},
		{"double quoted brace message", `git commit -m "{a,b}"`, 0},
		{"quoted brace command", `"g{it,}" commit -n -m msg`, 0},
		{"escaped brace command", `g\{it,\} commit -n -m msg`, 0},
		{"brace assignment control", `echo '{a,b}'; result=$?; echo done`, 0},
		{"non git subcommand brace operand", `git status {a,b}`, 0},
		{"brace group control", `{ echo ready; }`, 0},
		{"brace group commit control", `{ git commit -m msg; }`, 0},
		{"brace group skip flag", `{ git commit -n -m msg; }`, 2},
		{"bare brace subcommand", `git { -m msg`, 2},
		{"assignment brace data", `VALUE={a,b}`, 0},
		{"brace command after assignment", `VALUE={a,b} {git,commit,-n,-m,msg}`, 2},
		{"brace command inside group", `{ {git,commit,-n,-m,msg}; }`, 2},
		{"brace option hides subcommand", `git -c {x.y=z,commit,-n} -m msg`, 2},
		{"quoted option hides no subcommand", `git -c '{x.y=z,commit,-n}' -m msg`, 0},
		{"non git operand names git", `echo git {commit,-n} -m msg`, 0},
		{"git data operand names git", `git log git {commit,-n} -m msg`, 0},
	}, evasionRoundSixDelimiterBraceCases()...)
}

// evasionRoundSixDelimiterBraceCases covers literal message braces, expanding braces and inner delimiter quotes.
func evasionRoundSixDelimiterBraceCases() []struct {
	name, command string
	want          int
} {
	return []struct {
		name, command string
		want          int
	}{
		{"literal brace pair in commit message", `git commit -m fix{docs}`, 0},
		{"brace comma list in commit message", `git commit -m fix{docs,tests}`, 2},
		{"brace numeric sequence in commit message", `git commit -m fix{1..3}`, 2},
		{"brace letter sequence in commit message", `git commit -m fix{a..c}`, 2},
		{"nested braces with comma in commit message", `git commit -m fix{{a,b}}`, 2},
		{"parameter expansion brace in commit message", `git commit -m fix${docs}`, 2},
		{"unclosed brace in commit message", `git commit -m fix{docs`, 2},
		{"single-quoted inner delimiter quotes", "cat <<'\"EOF\"'\ndata\n\"EOF\"\ngit commit -n -m msg\nEOF", 2},
	}
}

// TestEmittedDelimiterBraceRegression replays exact delimiter and brace cases through adoption output.
func TestEmittedDelimiterBraceRegression(t *testing.T) {
	python, script := emittedInterceptor(t)
	for _, tc := range evasionDelimiterBraceCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, stderr := runInterceptorOutput(t, python, script, nil, nil, tc.command)
			t.Logf("hook rc=%d want=%d", got, tc.want)
			if got != tc.want {
				t.Errorf("rc=%d want=%d: %s", got, tc.want, stderr)
			}
		})
	}
}

// TestEmittedDelimiterBracePriorReplay records both verdicts of the regression and its two controls.
func TestEmittedDelimiterBracePriorReplay(t *testing.T) {
	python, fixed := emittedInterceptor(t)
	prior := filepath.Join(priorEvasionFixtures, "before-exact-heredoc-brace.block_evasion.py.txt")
	for i, tc := range evasionDelimiterBraceCases()[:6] {
		t.Run(tc.name, func(t *testing.T) {
			before := runInterceptor(t, python, prior, nil, nil, tc.command)
			after := runInterceptor(t, python, fixed, nil, nil, tc.command)
			wantBefore := tc.want
			if i < 4 {
				wantBefore = 0
			}
			t.Logf("prior rc=%d fixed rc=%d", before, after)
			if before != wantBefore || after != tc.want {
				t.Fatalf("prior rc=%d want=%d fixed rc=%d want=%d", before, wantBefore, after, tc.want)
			}
		})
	}
}

// TestEmittedBraceCommandPriorReplay checks the prior rendering and quoted/data controls.
func TestEmittedBraceCommandPriorReplay(t *testing.T) {
	python, fixed := emittedInterceptor(t)
	prior := filepath.Join(priorEvasionFixtures, "before-brace-command.block_evasion.py.txt")
	// Four command regressions followed by their three unchanged controls.
	for _, tc := range evasionDelimiterBraceCases()[16:23] {
		t.Run(tc.name, func(t *testing.T) {
			before := runInterceptor(t, python, prior, nil, nil, tc.command)
			after := runInterceptor(t, python, fixed, nil, nil, tc.command)
			t.Logf("prior rc=%d fixed rc=%d", before, after)
			if before != 0 || after != tc.want {
				t.Fatalf("prior rc=%d want=0 fixed rc=%d want=%d", before, after, tc.want)
			}
		})
	}
}

// evasionHeredocFeedCases reads the acceptance corpus shared with the Go and canonical tests.
func evasionHeredocFeedCases(t *testing.T) []struct {
	Name, Command string
	Want          int
} {
	t.Helper()
	root := openFixtureRoot(t, filepath.Join("..", "agenthook", "testdata"))
	data, err := root.ReadFile("heredoc-consumers.json")
	if err != nil {
		t.Fatalf("read heredoc consumer corpus: %v", err)
	}
	var cases []struct {
		Name, Command string
		Want          int
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("decode heredoc consumer corpus: %v", err)
	}
	return cases
}

// TestEmittedEvasionHeredocFeed replays the shared consumer acceptance corpus.
func TestEmittedEvasionHeredocFeed(t *testing.T) {
	python, script := emittedInterceptor(t)
	for _, tc := range evasionHeredocFeedCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			got, stderr := runInterceptorOutput(t, python, script, nil, nil, tc.Command)
			t.Logf("emitted rc=%d want=%d", got, tc.Want)
			if got != tc.Want {
				t.Errorf("emitted rc=%d want=%d: %s", got, tc.Want, stderr)
			}
		})
	}
}

// TestEmittedEvasionHeredocPriorReplay preserves the 22 acceptance verdicts of the prior hook.
func TestEmittedEvasionHeredocPriorReplay(t *testing.T) {
	python, fixed := emittedInterceptor(t)
	prior := filepath.Join(priorEvasionFixtures, "before-heredoc-consumers.block_evasion.py.txt")
	for _, tc := range evasionHeredocFeedCases(t)[:22] {
		t.Run(tc.Name, func(t *testing.T) {
			wantBefore := tc.Want
			if tc.Want == 2 && tc.Name != "bash quoted no-verify" {
				wantBefore = 0
			}
			before := runInterceptor(t, python, prior, nil, nil, tc.Command)
			after := runInterceptor(t, python, fixed, nil, nil, tc.Command)
			t.Logf("prior rc=%d fixed rc=%d", before, after)
			if before != wantBefore || after != tc.Want {
				t.Errorf("prior rc=%d want=%d; fixed rc=%d want=%d", before, wantBefore, after, tc.Want)
			}
		})
	}
}

// TestEmittedEvasionHeredocConsumers checks the rendered table against Go policy.
func TestEmittedEvasionHeredocConsumers(t *testing.T) {
	python, script := emittedInterceptor(t)
	got := loadInterceptorModule(t, python, script).Consumers
	if !reflect.DeepEqual(got, agenthook.HeredocConsumers()) {
		t.Errorf("consumer table differs: %v", got)
	}
}

// TestEmittedEvasionHeredocConsumerMutants proves each rendered ownership barrier matters.
func TestEmittedEvasionHeredocConsumerMutants(t *testing.T) {
	python, _ := emittedInterceptor(t)
	source := buildBlockEvasionPY()
	table := regexp.MustCompile(`(?s)HEREDOC_CONSUMERS = \{.*?\}\n`)
	for _, tc := range []struct {
		name, replacement, command string
		want                       int
	}{
		{"empty", "HEREDOC_CONSUMERS = {}\n", "bash <<'EOF'\ngit commit -n -m x\nEOF", 0},
		{"data as shells", `HEREDOC_CONSUMERS = {"*": "shell"}` + "\n", "git commit -F - <<'EOF'\ngit commit -n -m x\nEOF", 2},
		{"unknown as data", `HEREDOC_CONSUMERS = {"*": "data"}` + "\n", "python3 - <<'EOF'\ngit commit -n -m x\nEOF", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := table.ReplaceAllLiteralString(source, tc.replacement)
			if mutated == source {
				t.Fatalf("heredoc consumer mutation %s did not change the rendering", tc.name)
			}
			script := filepath.Join(t.TempDir(), "mutant.py")
			mustWrite(t, script, mutated)
			got, stderr := runInterceptorOutput(t, python, script, nil, nil, tc.command)
			t.Logf("mutant rc=%d want=%d", got, tc.want)
			if got != tc.want {
				t.Errorf("mutant rc=%d want=%d: %s", got, tc.want, stderr)
			}
		})
	}
}

// minimalPythonImports pins the origin/main hook imports: reduced standard libraries on
// minimal Python installations cannot be assumed to provide additional modules.
var minimalPythonImports = []string{"json", "os", "re", "sys"}

// interceptorImportViolations reads Python imports statically, including nested and aliased imports.
func interceptorImportViolations(t *testing.T, source string, allowed []string) []string {
	t.Helper()
	python := testsupport.PythonInterpreter(t)
	const program = `import ast, json, sys
allowed = set(sys.argv[1:])
nodes = list(ast.walk(ast.parse(sys.stdin.read())))
names = set()
for index in range(len(nodes)):
    node = nodes[index]
    if isinstance(node, ast.Import):
        names.update(node.names[index].name for index in range(len(node.names)))
    elif isinstance(node, ast.ImportFrom):
        names.add("." * node.level + (node.module or ""))
print(json.dumps(sorted(name for name in names if name not in allowed)))
`
	ctx, err := util.WithCommandStdin(testsupport.DeadlineContext(t), []byte(source))
	if err != nil {
		t.Fatalf("set import checker input: %v", err)
	}
	args := append([]string{"-B", "-c", program}, allowed...)
	result, err := util.RunCommandBytes(ctx, "", python, util.MaxCommandOutputBytes, args...)
	if err != nil {
		t.Fatalf("read interceptor imports: %v: %s", err, result.Stderr)
	}
	var violations []string
	if err := json.Unmarshal(result.Stdout, &violations); err != nil {
		t.Fatalf("decode import violations: %v", err)
	}
	return violations
}

// TestEmittedEvasionMinimalImports holds both shipped hooks to the minimal Python allow-list.
func TestEmittedEvasionMinimalImports(t *testing.T) {
	for name, source := range map[string]string{
		"emitted":   buildBlockEvasionPY(),
		"canonical": mustRead(t, filepath.Join("..", "..", filepath.FromSlash(evasionHookFile))),
	} {
		t.Run(name, func(t *testing.T) {
			allowed := slices.Clone(minimalPythonImports)
			if name == "canonical" {
				// origin/main's canonical hook also uses these repository integrations.
				allowed = append(allowed, "pathlib", "shutil", "subprocess")
			}
			if got := interceptorImportViolations(t, source, allowed); len(got) != 0 {
				t.Errorf("imports outside minimal Python allow-list: %v", got)
			}
		})
	}
}

// TestEmittedEvasionImportAllowListRejectsUnknown plants every static import shape, never importing it.
func TestEmittedEvasionImportAllowListRejectsUnknown(t *testing.T) {
	for source, want := range map[string]string{
		"import shlex\n": "shlex", "from shlex import split\n": "shlex",
		"import os, shlex as words\n": "shlex", "if True:\n    import shlex\n": "shlex",
		"import shlex.submodule\n": "shlex.submodule", "import json.tool\n": "json.tool",
		"from json.tool import main\n": "json.tool", "from .json import loads\n": ".json",
	} {
		t.Run(source, func(t *testing.T) {
			if got := interceptorImportViolations(t, source, minimalPythonImports); !slices.Equal(got, []string{want}) {
				t.Errorf("planted import accepted: %v", got)
			}
		})
	}
}

// TestEmittedEvasionMinimalPython replays document consumers with shlex explicitly unavailable.
func TestEmittedEvasionMinimalPython(t *testing.T) {
	python, script := emittedInterceptor(t)
	wrapper := filepath.Join(t.TempDir(), "minimal_python.py")
	mustWrite(t, wrapper, `import builtins, sys
original_import = builtins.__import__
def minimal_import(name, *args, **kwargs):
    """Reproduce the reduced interpreter without hiding unrelated import failures."""
    if name == "shlex":
        raise ModuleNotFoundError("minimal Python has no shlex")
    return original_import(name, *args, **kwargs)
builtins.__import__ = minimal_import
sys.argv = sys.argv[1:]
with open(sys.argv[0], encoding="utf-8") as source:
    exec(compile(source.read(), sys.argv[0], "exec"), {"__name__": "__main__", "__file__": sys.argv[0]})
`)
	for _, name := range []string{"bash quoted", "cat piped to bash", "commit -F - message data"} {
		found := false
		for _, tc := range evasionHeredocFeedCases(t) {
			if tc.Name != name {
				continue
			}
			found = true
			t.Run(name, func(t *testing.T) {
				got, stderr := runInterceptorOutput(t, python, wrapper, nil, nil, script, tc.Command)
				t.Logf("minimal Python rc=%d want=%d", got, tc.Want)
				if got != tc.Want {
					t.Errorf("minimal Python rc=%d want=%d: %s", got, tc.Want, stderr)
				}
			})
		}
		if !found {
			t.Fatalf("minimal Python corpus case missing: %s", name)
		}
	}
}

// recordMinimalPythonRendering preserves the prior hook and computes the new digest on explicit updates.
func recordMinimalPythonRendering(t *testing.T) {
	t.Helper()
	recordEvasionRenderingAt(t, priorEvasionFixtures, "hooks.go", buildBlockEvasionPY())
}

// recordEvasionRenderingAt archives an outgoing rendering in fixturesDir and extends hooksPath.
func recordEvasionRenderingAt(t *testing.T, fixturesDir, hooksPath, current string) {
	t.Helper()
	previous := readRootedText(t, fixturesDir, currentEvasionFixture)
	if previous != current {
		previousDigest := fixtureDigest(t, "outgoing rendering", []byte(previous))
		archiveName := "before-" + previousDigest[:12] + ".block_evasion.py.txt"
		prior := filepath.Join(fixturesDir, archiveName)
		if _, err := os.Stat(prior); errors.Is(err, os.ErrNotExist) {
			mustWrite(t, prior, previous)
		} else if err != nil {
			t.Fatalf("stat prior rendering archive %s: %v", archiveName, err)
		} else if readRootedText(t, fixturesDir, archiveName) != previous {
			t.Fatalf("prior rendering archive %s already contains different bytes", archiveName)
		}
	}
	digest := fixtureDigest(t, "minimal Python rendering", []byte(current))
	if _, exists := priorEvasionHookDigests[digest]; exists {
		return
	}
	source := readRootedText(t, filepath.Dir(hooksPath), filepath.Base(hooksPath))
	if strings.Contains(source, fmt.Sprintf("\t%q:", digest)) {
		return
	}
	const marker = "var priorEvasionHookDigests = map[string]string{\n"
	if strings.Count(source, marker) != 1 {
		t.Fatal("prior evasion digest table boundary missing or ambiguous")
	}
	entry := fmt.Sprintf("\t%q: %q,\n", digest, "bounded word unquoting without optional Python modules")
	mustWrite(t, hooksPath, strings.Replace(source, marker, marker+entry, 1))
}

// TestEmittedEvasionGuardUpdater verifies 4-arg upgrades, digest archiving and consumer sync on scratch copies.
func TestEmittedEvasionGuardUpdater(t *testing.T) {
	canonical := strings.ReplaceAll(readRootedText(t, filepath.Join("..", ".."), evasionHookFile), "\r\n", "\n")
	rendered := buildBlockEvasionPY()
	t.Run("upgrade_four_argument_call", func(t *testing.T) {
		const currentCall = "        exempt = pattern in READ_ONLY_EXEMPT_PATTERNS\n" +
			"        text = judged if exempt else command_str\n" +
			"        if search_command(\n" +
			"            pattern, text, segments, READ_ONLY_EXEMPT_PATTERNS[0], exempt\n" +
			"        ):\n" +
			"            reason = ("
		const priorCall = "        text = judged if pattern in READ_ONLY_EXEMPT_PATTERNS else command_str\n" +
			"        if search_command(pattern, text, segments, READ_ONLY_EXEMPT_PATTERNS[0]):\n" +
			"            reason = ("
		priorGuard := strings.Replace(canonical, currentCall, priorCall, 1)
		if priorGuard == canonical {
			t.Fatal("failed to construct prior four-argument guard")
		}
		upgraded := regenerateCanonicalEvasionGuard(t, priorGuard)
		if err := checkCanonicalEvasionGuard(upgraded, rendered); err != nil || upgraded != canonical {
			t.Fatalf("four-argument guard was not upgraded to canonical form: %v", err)
		}
		if rerun := regenerateCanonicalEvasionGuard(t, upgraded); rerun != canonical {
			t.Fatal("rerun modified the current canonical guard form")
		}
	})
	t.Run("archive_outgoing_rendering", func(t *testing.T) {
		assertScratchRenderingArchive(t, rendered)
	})
	t.Run("canonical_consumer_table", func(t *testing.T) {
		stale := strings.Replace(canonical, interceptorConsumers(), "HEREDOC_CONSUMERS = {\n    \"*\": \"shell\",\n}\n\n\n", 1)
		if stale == canonical {
			t.Fatal("failed to construct stale consumer table in canonical guard")
		}
		if err := checkCanonicalEvasionGuard(stale, rendered); err == nil {
			t.Fatal("checkCanonicalEvasionGuard accepted drifted HEREDOC_CONSUMERS table")
		}
		refreshed := regenerateCanonicalEvasionGuard(t, stale)
		if err := checkCanonicalEvasionGuard(refreshed, rendered); err != nil || refreshed != canonical {
			t.Fatalf("updater did not refresh HEREDOC_CONSUMERS in canonical guard: %v", err)
		}
	})
}

// assertScratchRenderingArchive runs the rendering archive updater twice in t.TempDir.
func assertScratchRenderingArchive(t *testing.T, rendered string) {
	t.Helper()
	scratch := t.TempDir()
	fixturesDir := filepath.Join(scratch, "evasion")
	originals := readFixtureDir(t, priorEvasionFixtures)
	for name, data := range originals {
		mustWrite(t, filepath.Join(fixturesDir, name), string(data))
	}
	hooksPath := filepath.Join(scratch, "hooks.go")
	originalHooks := readRootedText(t, ".", "hooks.go")
	mustWrite(t, hooksPath, originalHooks)
	next := strings.Replace(rendered, "BLOCK_EXIT = 2", "BLOCK_EXIT = 2  # scratch update", 1)
	recordEvasionRenderingAt(t, fixturesDir, hooksPath, next)
	recordEvasionRenderingAt(t, fixturesDir, hooksPath, next)
	outgoingDigest := fixtureDigest(t, "outgoing rendering", originals[currentEvasionFixture])
	archiveName := "before-" + outgoingDigest[:12] + ".block_evasion.py.txt"
	if got := readRootedText(t, fixturesDir, archiveName); got != string(originals[currentEvasionFixture]) {
		t.Fatalf("archive %s does not hold outgoing rendering", archiveName)
	}
	for name, want := range originals {
		if got := readRootedText(t, fixturesDir, name); got != string(want) {
			t.Fatalf("historical fixture %s modified", name)
		}
	}
	nextDigest := fixtureDigest(t, "next rendering", []byte(next))
	updatedHooks := readRootedText(t, scratch, "hooks.go")
	if strings.Count(updatedHooks, fmt.Sprintf("\t%q:", nextDigest)) != 1 {
		t.Fatalf("digest %s not recorded once in scratch hooks.go", nextDigest)
	}
	for digest := range priorEvasionHookDigests {
		if !strings.Contains(updatedHooks, fmt.Sprintf("\t%q:", digest)) {
			t.Fatalf("existing digest %s removed from scratch hooks.go", digest)
		}
	}
}

// TestEmittedEvasionShellWords replays the Go reference corpus, including malformed-word refusals.
func TestEmittedEvasionShellWords(t *testing.T) {
	python, script := emittedInterceptor(t)
	corpus := filepath.Join("..", "agenthook", "testdata", "shell-words.json")
	const program = `import json, runpy, sys
hook = runpy.run_path(sys.argv[1])
with open(sys.argv[2], encoding="utf-8") as source:
    cases = json.load(source)
for index in range(len(cases)):
    case = cases[index]
    try:
        got = hook["shell_unquote_word"](case["word"])
    except ValueError:
        got = None
    if got != case["want"]:
        sys.exit(case["name"] + ": got " + repr(got) + ", want " + repr(case["want"]))
`
	result, err := util.RunCommandBytes(testsupport.DeadlineContext(t), "", python,
		util.MaxCommandOutputBytes, "-B", "-c", program, script, corpus)
	if err != nil {
		t.Fatalf("replay emitted shell words: %v: %s", err, result.Stderr)
	}
}
