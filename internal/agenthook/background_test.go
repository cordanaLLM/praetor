// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestBackground_Negative_BareOperators verifies bare background jobs are denied only when enforcement is enabled.
func TestBackground_Negative_BareOperators(t *testing.T) {
	for _, enforce := range []bool{true, false} {
		policy, err := BuildPolicy(config.HookSettings{Background: config.BackgroundSettings{Enforce: enforce}})
		if err != nil {
			t.Fatal(err)
		}
		for name, command := range map[string]string{
			"trailing": "sleep 5 &", "build": "make build &",
			"subshell": "(launch.sh > log 2>&1 &)", "group": "{ job & }", "list": "a & b",
			"adjacent": "job&", "newline": "echo ok\njob &\necho next",
			"substitution": `echo "$(job &)"`, "backtick": "echo `job &`",
			"after heredoc": "cat <<EOF\nquoted & data\nEOF\njob &",
		} {
			t.Run(name+"/enforce="+map[bool]string{true: "true", false: "false"}[enforce], func(t *testing.T) {
				got := policy.Command(command)
				if enforce {
					requireDeniedContaining(t, command, got, "bare-background", "hooks.background.enforce",
						"client's background-task mechanism", "systemd user unit")
				} else if got.Outcome != Allow {
					t.Fatalf("unset enforcement %q: %+v", command, got)
				}
			})
		}
	}
}

// TestBackground_Positive_DataAndOtherOperators verifies ampersands used as data or other shell operators remain allowed.
func TestBackground_Positive_DataAndOtherOperators(t *testing.T) {
	policy, err := BuildPolicy(config.HookSettings{Background: config.BackgroundSettings{Enforce: true}})
	if err != nil {
		t.Fatal(err)
	}
	for name, command := range map[string]string{
		"and": "a && b", "duplicate fd": "cmd > f 2>&1", "duplicate output": "cmd >& f",
		"redirect": `bash -c 'cmd &> f'`, "append": `zsh -c 'cmd &>> f'`, "pipe": "cmd |& tee f",
		"double quotes": `echo "a & b"`, "single quotes": `echo 'x&y'`,
		"url": `curl "https://h/p?a=1&b=2"`, "escape": `echo x\&y`, "comment": "echo ok # job &",
		"heredoc": "cat <<EOF\njob &\nEOF", "quoted heredoc": "cat <<'EOF'\njob &\nEOF",
		"tab heredoc": "cat <<-EOF\n\tjob &\n\tEOF", "input fd": "cmd <&0",
		"case":  "case x in x) echo ok ;& y) echo next ;;& z) echo end ;; esac",
		"empty": "", "crlf": "echo ok\r\necho '&'\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := policy.Command(command); got.Outcome != Allow {
				t.Fatalf("%q: %+v", command, got)
			}
		})
	}
}

// TestBackground_Boundary_UnreadableAndLineLimit verifies unreadable commands are denied and the line limit is inclusive.
func TestBackground_Boundary_UnreadableAndLineLimit(t *testing.T) {
	policy, err := BuildPolicy(config.HookSettings{Background: config.BackgroundSettings{Enforce: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{`echo "open`, "cat <<EOF\nbody", strings.Repeat("\n", admissionMaxLines)} {
		requireDeniedContaining(t, command, policy.Command(command), "bare-background", "command unreadable")
	}
	if got := policy.Command(strings.Repeat("\n", admissionMaxLines-1)); got.Outcome != Allow {
		t.Fatalf("exact line limit: %+v", got)
	}
}

// TestBackground_ReviewCases plants payload, heredoc, expansion and comment evasions.
func TestBackground_ReviewCases(t *testing.T) {
	refused := map[string]string{
		"sh payload": `sh -c 'sleep 5 &'`, "bash payload": `bash -lc "sleep 5 &"`,
		"dash payload": `dash -c 'sleep 5 &'`, "zsh payload": `zsh -c 'sleep 5 &'`,
		"other shell": `ksh -c 'sleep 5 &'`, "eval": `eval 'sleep 5 &'`,
		"nested shell":         `bash -c "sh -c 'sleep 5 &'"`,
		"wrapper":              `env X=1 /bin/bash -c 'sleep 5 &'`,
		"command wrapper":      `command bash -c 'sleep 5 &'`,
		"substituted shell":    `echo "$(bash -c 'sleep 5 &')"`,
		"heredoc substitution": "cat <<EOF\n$(sleep 5 &)\nEOF",
		"heredoc backtick":     "cat <<EOF\n`sleep 5 &`\nEOF",
		"heredoc nested":       "cat <<EOF\n${v:-$(sleep 5 &)}\nEOF",
		"heredoc quotes data":  "cat <<EOF\n'$(sleep 5 &)'\nEOF",
		"heredoc multiline":    "cat <<EOF\n$(sleep\n5 &)\nEOF",
		"parameter":            `echo ${v:-$(sleep 5 &)}`,
		"parameter backtick":   "echo ${v:-`sleep 5 &`}",
		"arithmetic expansion": `echo $((1 + $(sleep 5 &)))`,
		"arithmetic command":   `(( a = $(sleep 5 &) ))`,
		"unknown redirect":     `job &>out`, "POSIX redirect": `dash -c 'job &>out'`,
		"POSIX append":             `sh -c 'job &>>out'`,
		"comment continuation":     "# comment \\\njob &",
		"escaped comment boundary": "echo x\\ # comment \\\njob &",
		"dynamic payload":          `sh -c "$SCRIPT"`, "eval args": `eval 'sleep' '5 &'`,
		"shell stdin":                 `printf 'job &' | sh`,
		"shell redirection args":      `sh </dev/null -c 'job &'`,
		"leading shell redirection":   `>/dev/null sh -c 'job &'`,
		"shell argument substitution": `sh -c "${SCRIPT}"`,
		"dynamic executable":          `$SHELL -c 'job &'`,
		"function payload":            `f(){ sh -c 'job &'; }; f`,
		"nested POSIX redirect":       `bash -c 'sh -c "job &>out"'`,
	}
	for name, command := range refused {
		t.Run(name, func(t *testing.T) { checkBackgroundReview(t, command, true, "bare-background") })
	}
	allowed := []string{
		`(( a & b ))`, `echo $(( a & b ))`, `(( a = (b & c) ))`,
		`echo 'sh -c "sleep 5 &"'`, `printf '%s' 'eval job &'`,
		`bash -c 'echo "a & b"'`, `eval 'echo "a & b"'`,
		"cat <<'EOF'\n$(sleep 5 &)\nEOF", "cat <<\\EOF\n`job &`\nEOF",
		"cat <<EOF\njob &\nEOF", "cat <<EOF\n\\$(job &)\nEOF",
		"echo '# comment'\ntrue", "echo foo\\\nbar", `bash -c '(( a & b ))'`, `command -v sh`, `echo ba#sh`,
		`bash -c 'eval "job &>out"'`, `echo "dynamic $DATA &"`,
	}
	for _, command := range allowed {
		t.Run(command, func(t *testing.T) { checkBackgroundReview(t, command, false, "") })
	}
}

// checkBackgroundReview exercises both enforcement states for every review fixture.
func checkBackgroundReview(t *testing.T, command string, denied bool, reason string) {
	t.Helper()
	for _, enforce := range []bool{true, false} {
		policy, err := BuildPolicy(config.HookSettings{Background: config.BackgroundSettings{Enforce: enforce}})
		if err != nil {
			t.Fatal(err)
		}
		got := policy.Command(command)
		if enforce && denied {
			requireDeniedContaining(t, command, got, reason)
		} else if got.Outcome != Allow {
			t.Fatalf("enforce=%t command=%q: %+v", enforce, command, got)
		}
	}
}

// TestBackground_ReviewSyntax plants adjacent redirections and inherited quote contexts.
func TestBackground_ReviewSyntax(t *testing.T) {
	for _, command := range []string{
		`echo "${v:-'$(job &)'}"`, `echo "${v:-'` + "`job &`" + `'}"`,
		"sh<<'EOF'\njob &\nEOF", `sh>out -c 'job &'`, `sh<<<'job &'`,
		`sh 2>&1 -c 'job &'`, `2>out sh -c 'job &'`,
		`flock lock -c 'job &'`, `env -S "sh -c 'job &'"`,
		`env -a unused sh -c 'job &'`,
		`env '-a' unused sh -c 'job &'`, `env 'X=1' sh -c 'job &'`,
		`env X=$VALUE true`, `env -C $VALUE true`, `praetorctl 'admit' --label test -- sh -c 'job &'`,
		`praetorctl admit --label test -- sh -c 'job &'`,
	} {
		t.Run(command, func(t *testing.T) { checkBackgroundReview(t, command, true, "bare-background") })
	}
	for _, command := range []string{
		`echo ${v:-'$(job &)'}`, `echo "${v:-'a & b'}"`,
		`echo 'sh<<EOF job &'`, `echo a\>b`, `bash -c 'cmd&>out'`,
		`praetorctl admit --label test -- true`,
		`env printf '%s' '-S a & b'`, `flock lock printf '%s' '-c a & b'`,
		`env -C ./ sh -c 'echo "a & b"'`, `env '-C' ./ sh -c 'echo "a & b"'`, `systemd-run --user --scope -- sleep 5`,
	} {
		t.Run(command, func(t *testing.T) { checkBackgroundReview(t, command, false, "") })
	}
}

// TestBackground_ReviewBounds checks inclusive bounds before joining or parsing input.
func TestBackground_ReviewBounds(t *testing.T) {
	for _, tc := range []struct {
		name, command, reason string
		denied                bool
	}{
		{"over bytes", strings.Repeat("x", contextopt.MaxSourceBytes+1), "background byte bound", true},
		{"exact bytes", strings.Repeat("x", contextopt.MaxSourceBytes), "", false},
		{"over lines LF", strings.Repeat("\\\n", admissionMaxLines), "background physical-line bound", true},
		{"over lines CRLF", strings.Repeat("\\\r\n", admissionMaxLines), "background physical-line bound", true},
		{"exact lines", strings.Repeat("\\\n", admissionMaxLines-1) + "true", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) { checkBackgroundReview(t, tc.command, tc.denied, tc.reason) })
	}

	nested := "job &"
	for range admissionMaxWrappers + 1 {
		nested = "bash -c " + util.QuotePOSIXShellArg(nested)
	}
	checkBackgroundReview(t, nested, true, "background inspection depth bound")
}

// TestBackground_ReviewQuotedContinuations preserves a quoted comment's physical newline.
func TestBackground_ReviewQuotedContinuations(t *testing.T) {
	checkBackgroundReview(t, "bash -c '# comment \\\nsleep 5 &'", true, "bare-background")
	checkBackgroundReview(t, "bash -c \"echo o\\\nk\"", false, "")
}

// TestBackground_ReviewArithmetic inspects substitutions without inventing executable words.
func TestBackground_ReviewArithmetic(t *testing.T) {
	for _, command := range []string{
		`(( a = $(printf 1) ))`, `echo $(( $(printf 1) + 1 ))`,
		"(( a = `printf 1` ))", `bash -c '(( a = $(printf 1) ))'`,
		`echo a$((1))b`,
	} {
		t.Run(command, func(t *testing.T) { checkBackgroundReview(t, command, false, "") })
	}
	for _, command := range []string{
		`(( a = $(sleep 5 &) ))`, `echo $(( $(sleep 5 &) + 1 ))`,
		`(( a = $(bash -c 'sleep 5 &') ))`,
		`a$((1))b -c 'sleep 5 &'`,
	} {
		t.Run(command, func(t *testing.T) { checkBackgroundReview(t, command, true, "bare-background") })
	}
}

// TestBackground_ReviewBraceExecutables refuses expanding names while retaining literal data.
func TestBackground_ReviewBraceExecutables(t *testing.T) {
	for _, command := range []string{
		`bash -c "bas{h..h} -c 'sleep 5 &'"`, `bas{h..h} -c 'sleep 5 &'`,
		`b{a,}sh -c 'sleep 5 &'`, `env bas{h..h} -c 'sleep 5 &'`,
		`b{a{z},a}sh -c 'sleep 5 &'`, `{bash,sh} -c 'sleep 5 &'`,
		`{ bash -c 'sleep 5 &'; }`,
	} {
		t.Run(command, func(t *testing.T) { checkBackgroundReview(t, command, true, "bare-background") })
	}
	for _, command := range []string{
		`echo {1..3}`, `echo {a,b}`, `bas'{h..h}' -c 'sleep 5 &'`,
		`"b{a,}sh" -c 'sleep 5 &'`, `bas\{h..h\} -c 'sleep 5 &'`,
		`b{a}sh -c 'sleep 5 &'`, `{ echo ok; }`,
		`bash -c 'echo {1..3}'`,
	} {
		t.Run(command, func(t *testing.T) { checkBackgroundReview(t, command, false, "") })
	}
}
