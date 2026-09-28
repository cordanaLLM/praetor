package clientjson

import (
	"strings"
	"testing"
)

// bashHook leaves ExactLiteral false, the matcher reading of a client that tests every matcher
// as an unanchored regular expression; literalHook sets it, as Claude Code and Codex read one.
var (
	bashHook    = Hook{Event: "PreToolUse", Matcher: "^Bash$", Command: "engine hook pre-tool", Timeout: 15}
	literalHook = Hook{Event: "PreToolUse", Matcher: "^Bash$", Command: "engine hook pre-tool", Timeout: 15, ExactLiteral: true}
)

// Positive: a missing document, hooks section, event list and matcher group are all created,
// and the plan reports the command it added.
func TestPlanHooks_Positive_CreatesMissingSections(t *testing.T) {
	for name, existing := range map[string]string{
		"no file":     "",
		"no hooks":    `{"model": "x"}`,
		"no event":    `{"hooks": {"Stop": []}}`,
		"other group": `{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": []}]}}`,
	} {
		plan, err := PlanHooks(t.Context(), []byte(existing), []Hook{bashHook})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !plan.Changed || len(plan.Added) != 1 || plan.Added[0] != bashHook.Command || len(plan.Present) != 0 {
			t.Fatalf("%s: plan %+v", name, plan)
		}
		want := `"matcher": "^Bash$",` + "\n" + `        "hooks": [` + "\n" + `          {` + "\n" +
			`            "type": "command",` + "\n" + `            "command": "engine hook pre-tool",` + "\n" + `            "timeout": 15`
		if !strings.Contains(string(plan.Content), want) {
			t.Fatalf("%s: content lacks the new group:\n%s", name, plan.Content)
		}
	}
}

// Positive: a handler that already serves the hook, by exact command or by the ServedBy
// predicate over command and args, in a group whose matcher is the hook's or selects every
// tool (absent, "*", ".*"), leaves the input unchanged and is reported present.
func TestPlanHooks_Positive_ServedHookIsPresent(t *testing.T) {
	served := bashHook
	served.ServedBy = func(line string) bool { return strings.HasSuffix(line, "guard.py") }
	for _, existing := range []string{
		`{"hooks": {"PreToolUse": [{"matcher": "^Bash$", "hooks": [{"command": "python3", "args": ["-B", "guard.py"]}]}]}}`,
		`{"hooks": {"PreToolUse": [{"hooks": [{"type": "command", "command": "engine hook pre-tool"}]}]}}`,
		`{"hooks": {"PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "engine hook pre-tool"}]}]}}`,
		`{"hooks": {"PreToolUse": [{"matcher": ".*", "hooks": [{"command": "python3", "args": ["guard.py"]}]}]}}`,
	} {
		plan, err := PlanHooks(t.Context(), []byte(existing), []Hook{served})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Changed || string(plan.Content) != existing || len(plan.Present) != 1 || len(plan.Added) != 0 {
			t.Fatalf("plan %+v for %s", plan, existing)
		}
	}
}

// Negative: a serving handler under a matcher that does not cover the hook's, an unrelated
// tool, a half-anchored ^Bash (bashHook leaves ExactLiteral false) or a matcher that is not a
// string, does not run for exactly the guarded tool, so the hook is still registered in its
// own group.
func TestPlanHooks_Negative_HandlerUnderOtherMatcherServesNothing(t *testing.T) {
	served := bashHook
	served.ServedBy = func(line string) bool { return strings.HasSuffix(line, "guard.py") }
	for _, matcher := range []string{`"^(Edit|Write)$"`, `"^Bash"`, `7`} {
		existing := `{"hooks": {"PreToolUse": [{"matcher": ` + matcher + `, "hooks": [{"type": "command", "command": "engine hook pre-tool"},` +
			` {"command": "python3", "args": ["guard.py"]}]}]}}`
		plan, err := PlanHooks(t.Context(), []byte(existing), []Hook{served})
		if err != nil {
			t.Fatalf("matcher %s: %v", matcher, err)
		}
		if !plan.Changed || len(plan.Added) != 1 || len(plan.Present) != 0 {
			t.Fatalf("matcher %s: plan %+v", matcher, plan)
		}
		if !strings.Contains(string(plan.Content), `"matcher": "^Bash$"`) {
			t.Fatalf("matcher %s: no ^Bash$ group:\n%s", matcher, plan.Content)
		}
	}
}

// Negative: sections of the wrong type, a malformed document and more hooks than MaxHooks fail
// the plan.
func TestPlanHooks_Negative_RefusesWrongShapes(t *testing.T) {
	for _, existing := range []string{`{"hooks": []}`, `{"hooks": "x"}`, `{"hooks": {"PreToolUse": {}}}`,
		`{"hooks": {"PreToolUse": null}}`, `{"hooks": {"PreToolUse": "x"}}`, `[]`, `{`} {
		if plan, err := PlanHooks(t.Context(), []byte(existing), []Hook{bashHook}); err == nil {
			t.Errorf("%s accepted: %+v", existing, plan)
		}
	}
	hooks := make([]Hook, MaxHooks+1)
	if _, err := PlanHooks(t.Context(), nil, hooks); err == nil {
		t.Error("more than MaxHooks accepted")
	}
}

// Boundary: planning over a plan's own output adds nothing, MaxHookGroups groups are scanned
// while one more is refused, and non-object entries serve nothing and are kept.
func TestPlanHooks_Boundary_IdempotentAndGroupBound(t *testing.T) {
	first, err := PlanHooks(t.Context(), nil, []Hook{bashHook})
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanHooks(t.Context(), first.Content, []Hook{bashHook})
	if err != nil || second.Changed || string(second.Content) != string(first.Content) {
		t.Fatalf("second plan %+v, %v", second, err)
	}
	groups := func(n int) []byte {
		return []byte(`{"hooks": {"PreToolUse": [` + strings.TrimSuffix(strings.Repeat(`{"matcher": "x", "hooks": []},`, n), ",") + `]}}`)
	}
	if _, err := PlanHooks(t.Context(), groups(MaxHookGroups), []Hook{bashHook}); err != nil {
		t.Fatalf("%d groups: %v", MaxHookGroups, err)
	}
	if _, err := PlanHooks(t.Context(), groups(MaxHookGroups+1), []Hook{bashHook}); err == nil {
		t.Fatalf("%d groups accepted", MaxHookGroups+1)
	}
	odd, err := PlanHooks(t.Context(), []byte(`{"hooks": {"PreToolUse": [7, {"matcher": "^Bash$", "hooks": "x"}]}}`), []Hook{bashHook})
	if err != nil || !odd.Changed || !strings.Contains(string(odd.Content), "7,") {
		t.Fatalf("odd entries plan %+v, %v", odd, err)
	}
}

// groupDocument is a PreToolUse hook file holding one group with matcher and the handlers.
func groupDocument(matcher, handlers string) string {
	return `{"hooks": {"PreToolUse": [{"matcher": "` + matcher + `", "hooks": [` + handlers + `]}]}}`
}

const (
	// evasionAdapter is the adopted interceptor as an adopter registers it by hand.
	evasionAdapter = `{"type": "command", "command": "python3", "args": [".config/agent/hooks/block_evasion.py"]}`
	// lintHandler is an unrelated adopter handler that serves no hook.
	lintHandler = `{"type": "command", "command": "lint.sh"}`
)

// servedByAdapter is hook served by the adopted interceptor as well as by its own command.
func servedByAdapter(hook Hook, matcher string) Hook {
	hook.Matcher = matcher
	hook.ServedBy = func(line string) bool { return strings.HasSuffix(line, "block_evasion.py") }
	return hook
}

// Positive: where the client reads a literal matcher as the exact tool name, Bash and ^Bash$
// select one tool. A handler serving the hook under the other spelling leaves the file alone
// (the adopter's Bash group running block_evasion.py for a ^Bash$ row, a ^run_shell_command$
// group for a run_shell_command row), and a missing handler joins the equivalent group, which
// keeps its own spelling, instead of opening a second group for the same tool.
func TestPlanHooks_Positive_LiteralAndAnchoredNameAreOneSelection(t *testing.T) {
	for want, group := range map[string]string{"^Bash$": "Bash", "run_shell_command": "^run_shell_command$"} {
		existing := groupDocument(group, evasionAdapter)
		plan, err := PlanHooks(t.Context(), []byte(existing), []Hook{servedByAdapter(literalHook, want)})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Changed || string(plan.Content) != existing || len(plan.Present) != 1 || len(plan.Added) != 0 {
			t.Fatalf("hook %s, group %s: plan %+v", want, group, plan)
		}
	}
	for want, group := range map[string]string{"^Bash$": "Bash", "Bash": "^Bash$"} {
		hook := literalHook
		hook.Matcher = want
		plan, err := PlanHooks(t.Context(), []byte(groupDocument(group, lintHandler)), []Hook{hook})
		if err != nil {
			t.Fatal(err)
		}
		content := string(plan.Content)
		if !plan.Changed || strings.Count(content, `"matcher"`) != 1 || !strings.Contains(content, `"matcher": "`+group+`"`) ||
			strings.Index(content, "lint.sh") > strings.Index(content, hook.Command) {
			t.Fatalf("hook %s, group %s: handler not appended to the one group:\n%s", want, group, content)
		}
	}
}

// Negative: only the anchored literal is equivalent. A pattern that selects other tools as
// well (Bash.*, a half-anchored ^Bash or Bash$), another tool list and another case do not
// cover ^Bash$, so the hook gets its own group. For a client that tests every matcher as an
// unanchored regular expression, a ^NAME$ group selects less than a bare NAME hook, so it
// serves none (^run_shell_command$ for run_shell_command) and takes no handler for it.
func TestPlanHooks_Negative_OnlyTheAnchoredLiteralIsEquivalent(t *testing.T) {
	cases := []struct {
		group, want string
		hook        Hook
	}{
		{"^(Edit|Write)$", "^Bash$", literalHook}, {"Bash.*", "^Bash$", literalHook}, {"^Bash", "^Bash$", literalHook},
		{"Bash$", "^Bash$", literalHook}, {"^bash$", "^Bash$", literalHook}, {"^Bash$", "Bash", bashHook},
		{"^run_shell_command$", "run_shell_command", bashHook},
	}
	for _, c := range cases {
		plan, err := PlanHooks(t.Context(), []byte(groupDocument(c.group, evasionAdapter)), []Hook{servedByAdapter(c.hook, c.want)})
		if err != nil {
			t.Fatalf("group %s: %v", c.group, err)
		}
		if !plan.Changed || len(plan.Added) != 1 || strings.Count(string(plan.Content), `"matcher"`) != 2 {
			t.Fatalf("group %s, hook %s (exact literal %v): plan %+v\n%s", c.group, c.want, c.hook.ExactLiteral, plan, plan.Content)
		}
	}
	shell := bashHook
	shell.Matcher = "run_shell_command"
	plan, err := PlanHooks(t.Context(), []byte(groupDocument("^run_shell_command$", lintHandler)), []Hook{shell})
	if err != nil || strings.Count(string(plan.Content), `"matcher"`) != 2 {
		t.Fatalf("unanchored-regex client joined ^run_shell_command$ for run_shell_command: %v\n%s", err, plan.Content)
	}
}

// Boundary: a metacharacter inside the name is not normalised (Ba.h and ^Ba.h$ stay apart), an
// empty anchored name ^$ is not the absent matcher, the match-all spellings still cover every
// hook, and the appended handler carries the hook's timeout while the existing one keeps its
// literal.
func TestPlanHooks_Boundary_LiteralEquivalenceEdges(t *testing.T) {
	for _, pair := range [][2]string{{"Ba.h", "^Ba.h$"}, {"^Ba.h$", "Ba.h"}, {"^$", ""}} {
		hook := literalHook
		hook.Matcher = pair[1]
		plan, err := PlanHooks(t.Context(), []byte(groupDocument(pair[0], lintHandler)), []Hook{hook})
		if err != nil || len(plan.Added) != 1 || strings.Count(string(plan.Content), `"hooks": [`) != 2 {
			t.Fatalf("group %q, hook %q: %v\n%s", pair[0], pair[1], err, plan.Content)
		}
	}
	for _, matcher := range matchAllMatchers {
		existing := groupDocument(matcher, `{"type": "command", "command": "engine hook pre-tool"}`)
		if plan, err := PlanHooks(t.Context(), []byte(existing), []Hook{literalHook}); err != nil || plan.Changed {
			t.Fatalf("match-all %q: %+v, %v", matcher, plan, err)
		}
	}
	existing := groupDocument("Bash", `{"type": "command", "command": "lint.sh", "timeout": 15000}`)
	plan, err := PlanHooks(t.Context(), []byte(existing), []Hook{literalHook})
	if err != nil || !strings.Contains(string(plan.Content), `"timeout": 15000`) || !strings.Contains(string(plan.Content), `"timeout": 15`+"\n") {
		t.Fatalf("timeouts changed: %v\n%s", err, plan.Content)
	}
}

// TestSameSelection: positive, equal matchers and, for an exact literal client, NAME against
// ^NAME$ in either order; negative, the bare and anchored name for an unanchored-regex client
// and every pattern that is not ^NAME$ of the other; boundary, an empty or metacharacter name.
func TestSameSelection(t *testing.T) {
	for _, c := range []struct {
		have, want  string
		exact, same bool
	}{
		{"^Bash$", "^Bash$", false, true}, {"", "", true, true}, {"Bash", "^Bash$", true, true},
		{"^run_shell_command$", "run_shell_command", true, true}, {"Bash", "^Bash$", false, false},
		{"^run_shell_command$", "run_shell_command", false, false}, {"Bash.*", "^Bash$", true, false},
		{"^Bash", "Bash", true, false}, {"^(Edit|Write)$", "Edit|Write", true, false},
		{"^$", "", true, false}, {"^Ba.h$", "Ba.h", true, false}, {"^Bash$", "Bash$", true, false},
	} {
		if got := sameSelection(c.have, c.want, c.exact); got != c.same {
			t.Errorf("sameSelection(%q, %q, %v) = %v, want %v", c.have, c.want, c.exact, got, c.same)
		}
	}
}

// Positive: every client runs a bare NAME group for the tool NAME, so a handler serving a
// ^NAME$ hook under the bare name leaves the file alone whatever the matcher reading: the
// exact literal one (literalHook) and the unanchored regular expression one (bashHook), where
// the bare name selects more tools than the hook, not fewer.
func TestPlanHooks_Positive_BareNameGroupCoversAnchoredHook(t *testing.T) {
	engine := `{"type": "command", "command": "engine hook pre-tool"}`
	for _, hook := range []Hook{literalHook, bashHook} {
		for group, want := range map[string]string{"Bash": "^Bash$", "run_shell_command": "^run_shell_command$"} {
			for _, handler := range []string{evasionAdapter, engine} {
				existing := groupDocument(group, handler)
				plan, err := PlanHooks(t.Context(), []byte(existing), []Hook{servedByAdapter(hook, want)})
				if err != nil {
					t.Fatal(err)
				}
				if plan.Changed || string(plan.Content) != existing || len(plan.Present) != 1 || len(plan.Duplicates) != 0 {
					t.Fatalf("group %s, hook %s (exact literal %v): plan %+v", group, want, hook.ExactLiteral, plan)
				}
			}
		}
	}
}

// Boundary: a hook served twice, under the bare and the anchored name (by another command line
// or an identical copy) or twice in one group, is reported present once, in file order, and
// duplicate once, and the file is left as it was. A
// bare NAME group that only covers the hook takes no handler for a client that tests matchers
// as unanchored regular expressions: a missing handler joins only a group of the same selection.
func TestPlanHooks_Boundary_DuplicateServingHandlers(t *testing.T) {
	engine := `{"type": "command", "command": "engine hook pre-tool"}`
	hook := servedByAdapter(bashHook, "^run_shell_command$")
	bareThenAnchored := func(second string) string {
		return `{"hooks": {"PreToolUse": [{"matcher": "run_shell_command", "hooks": [` + engine + `]},` +
			` {"matcher": "^run_shell_command$", "hooks": [` + second + `]}]}}`
	}
	for name, tc := range map[string]struct{ existing, duplicate string }{
		"bare then anchored": {bareThenAnchored(evasionAdapter), "block_evasion.py"},
		"identical copy":     {bareThenAnchored(engine), hook.Command},
		"one group":          {groupDocument("^run_shell_command$", engine+", "+lintHandler+", "+evasionAdapter), "block_evasion.py"},
	} {
		plan, err := PlanHooks(t.Context(), []byte(tc.existing), []Hook{hook})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if plan.Changed || string(plan.Content) != tc.existing || len(plan.Added) != 0 ||
			len(plan.Present) != 1 || plan.Present[0] != hook.Command ||
			len(plan.Duplicates) != 1 || !strings.HasSuffix(plan.Duplicates[0], tc.duplicate) {
			t.Fatalf("%s: plan %+v", name, plan)
		}
	}
	plan, err := PlanHooks(t.Context(), []byte(groupDocument("Bash", lintHandler)), []Hook{bashHook})
	if err != nil || !plan.Changed || strings.Count(string(plan.Content), `"matcher"`) != 2 || len(plan.Duplicates) != 0 {
		t.Fatalf("regex client joined a covering bare group: %v\n%s", err, plan.Content)
	}
}
