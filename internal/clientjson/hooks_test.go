package clientjson

import (
	"strings"
	"testing"
)

var bashHook = Hook{Event: "PreToolUse", Matcher: "^Bash$", Command: "engine hook pre-tool", Timeout: 15}

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
// predicate over command and args, leaves the input unchanged and is reported present.
func TestPlanHooks_Positive_ServedHookIsPresent(t *testing.T) {
	served := bashHook
	served.ServedBy = func(line string) bool { return strings.HasSuffix(line, "guard.py") }
	for _, existing := range []string{
		`{"hooks": {"PreToolUse": [{"matcher": "other", "hooks": [{"type": "command", "command": "engine hook pre-tool"}]}]}}`,
		`{"hooks": {"PreToolUse": [{"matcher": "^Bash$", "hooks": [{"command": "python3", "args": ["-B", "guard.py"]}]}]}}`,
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
