package agenthook

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
)

func TestPolicyCommandBuiltinRules(t *testing.T) {
	builtin := policy(t)
	for _, tc := range []struct {
		command, invariant string
	}{
		{"git push --no-verify origin x", "HISS"},
		{"git commit -m x -n", "HISS"},
		{"env LEFTHOOK=0 git push", "HISS"},
		{"SKIP=all git commit", "HISS"},
		{"git config core.hooksPath=/dev/null", "HISS"},
		{"rm -r .git/hooks", "HISS"},
		{"git commit -an -m x", "HISS"},
		{"git -C sub commit -n", "HISS"},
		{"env LEFTHOOK=false git push", "HISS"},
		{"git config core.hooksPath hooks-off", "HISS"},
		{"chmod 000 .git/hooks", "HISS"},
		{"lefthook uninstall --aggressive", "HISS"},
		{"standardsctl conform /srv/dev", "DEV-01"},
		{"praetorctl needs  epic dev/", "DEV-01"},
	} {
		verdict := builtin.Command(tc.command)
		if verdict.Outcome != Deny || !strings.HasPrefix(verdict.Reason, "[BLOCKED BY "+tc.invariant+"] ") {
			t.Errorf("%q: %+v", tc.command, verdict)
		}
		if strings.Contains(verdict.Reason, tc.command) {
			t.Errorf("%q: the reason echoes the command", tc.command)
		}
	}
	for _, command := range []string{
		"git commit -s -m 'docs: explain the no-verify rule'", "git commit --name-only", "LEFTHOOK=1 git commit",
		"rm -rf build/hooks", "praetorctl adopt ~/dev/org/repo", "praetorctl audit ~/dev", "ls /dev/null",
		"git config core.hooksPath", "git log -n 3 --grep commit", "grep -rn hooks .git/hooks", "lefthook run pre-commit",
	} {
		if verdict := builtin.Command(command); verdict.Outcome != Allow || verdict.Reason != "" {
			t.Errorf("%q denied: %+v", command, verdict)
		}
	}
}

// TestPolicyWordBoundaryIsTheStricterOne pins a measured difference: RE2's word boundary
// is ASCII, Python's is Unicode, so a flag followed by a non-ASCII letter is denied here
// and allowed by the Python guard. The difference is on the closed side and stays.
func TestPolicyWordBoundaryIsTheStricterOne(t *testing.T) {
	if verdict := policy(t).Command("git commit -né"); verdict.Outcome != Deny {
		t.Errorf("non-ASCII letter after the short flag: %+v", verdict)
	}
	for _, rule := range policy(t).rules {
		compiled := rule.pattern.String()
		if strings.Contains(rule.source, `\s`) && !strings.Contains(compiled, pythonSpace) {
			t.Errorf("rule %q lost Python's whitespace class", rule.source)
		}
		if strings.Contains(compiled, "["+pythonSpace) {
			t.Errorf("rule %q uses the class inside a bracket expression", rule.source)
		}
	}
}

// TestBuiltinRules pins the exported list adoption renders into its interceptor: every
// built-in rule in evaluation order, each embeddable in a Python raw string, and a fresh
// slice per call so a caller cannot change the policy.
func TestBuiltinRules(t *testing.T) {
	rules := BuiltinRules()
	if len(rules) != len(builtinEvasion)+1 || rules[len(rules)-1] != (BuiltinRule{Source: builtinDevRoot, Invariant: "DEV-01"}) {
		t.Fatalf("unexpected rule list: %+v", rules)
	}
	exempt := 0
	for index, rule := range rules[:len(builtinEvasion)] {
		want := BuiltinRule{Source: builtinEvasion[index], Invariant: "HISS", ReadOnlyExempt: slices.Contains(readOnlyExemptRules, rule.Source)}
		if rule != want {
			t.Errorf("rule %d: %+v", index, rule)
		}
		if rule.ReadOnlyExempt {
			exempt++
		}
	}
	if exempt != 2 || !rules[1].ReadOnlyExempt || !rules[3].ReadOnlyExempt {
		t.Errorf("the read-only exemption covers %d rules, want the short skip flag and the skip variable", exempt)
	}
	for _, rule := range rules {
		if strings.ContainsAny(rule.Source, "\"\n") || strings.HasSuffix(rule.Source, `\`) {
			t.Errorf("rule %q cannot be written as a Python raw string", rule.Source)
		}
		if _, ok := builtinMessages[rule.Invariant]; !ok {
			t.Errorf("rule %q has no message for %s", rule.Source, rule.Invariant)
		}
	}
	rules[0].Source = "changed"
	if BuiltinRules()[0].Source == "changed" {
		t.Error("BuiltinRules must return a fresh slice")
	}
	values, names := LefthookDisableValues(), LefthookNarrowingVariables()
	values[0], names[0] = "changed", "changed"
	if LefthookDisableValues()[0] == "changed" || LefthookNarrowingVariables()[0] == "changed" {
		t.Error("the environment lists must be returned as copies")
	}
}

func TestNewPolicyOperatorDenyList(t *testing.T) {
	compiled := policy(t, `\bterraform\s+destroy\b`, organisationContainerPattern)
	if verdict := compiled.Command("terraform destroy -auto-approve"); verdict.Outcome != Deny ||
		!strings.HasPrefix(verdict.Reason, "[BLOCKED BY operator] ") {
		t.Errorf("operator pattern ignored: %+v", verdict)
	}
	if verdict := compiled.Command("praetorctl adopt ~/dev/scratch"); verdict.Outcome != Deny {
		t.Errorf("organisation container allowed: %+v", verdict)
	}
	if verdict := compiled.Command("terraform plan"); verdict.Outcome != Allow {
		t.Errorf("operator pattern over-matches: %+v", verdict)
	}
	for name, list := range map[string][]string{
		"empty pattern": {""}, "does not compile": {"("}, "lookahead is not RE2": {"(?=x)"},
		"oversized pattern": {strings.Repeat("a", config.MaxCommandPolicyPatternBytes+1)},
		"oversized list":    make([]string, config.MaxCommandPolicyDenyPatterns+1),
	} {
		if compiled, err := NewPolicy(list); err == nil || compiled != nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestNewPolicyBounds(t *testing.T) {
	full := make([]string, config.MaxCommandPolicyDenyPatterns)
	for index := range full {
		full[index] = strings.Repeat("a", config.MaxCommandPolicyPatternBytes)
	}
	if _, err := NewPolicy(full); err != nil {
		t.Fatalf("list and patterns at the bound refused: %v", err)
	}
	for _, list := range [][]string{nil, {}} {
		compiled, err := NewPolicy(list)
		if err != nil || compiled.Command("git status").Outcome != Allow {
			t.Fatalf("empty operator list: %v", err)
		}
	}
	var absent *Policy
	if verdict := absent.Command("git status"); verdict.Outcome != Deny {
		t.Errorf("a missing policy allowed a command: %+v", verdict)
	}
}

func TestEnvironment(t *testing.T) {
	values := map[string]string{}
	getenv := func(key string) string { return values[key] }
	if verdict := Environment(getenv); verdict.Outcome != Allow {
		t.Errorf("clean environment denied: %+v", verdict)
	}
	for _, tc := range []struct {
		key, value string
		outcome    Outcome
	}{
		{"LEFTHOOK", "0", Deny}, {"LEFTHOOK", "1", Allow}, {"LEFTHOOK", "00", Allow}, {"LEFTHOOK", "false", Deny},
		{"LEFTHOOK", "False", Allow}, {"LEFTHOOK", "true", Allow},
		{"LEFTHOOK_EXCLUDE", "lint", Deny}, {"LEFTHOOK_SKIP", "pre-push", Deny}, {"LEFTHOOK_VERBOSE", "1", Allow},
	} {
		values = map[string]string{tc.key: tc.value}
		if verdict := Environment(getenv); verdict.Outcome != tc.outcome {
			t.Errorf("%s=%s: %+v", tc.key, tc.value, verdict)
		}
	}
	if verdict := Environment(nil); verdict.Outcome != Deny {
		t.Errorf("no environment allowed: %+v", verdict)
	}
}

// TestRefusalWording pins the one wording source both Python engines print (BUG-1014): a
// rule's deny reason is its RefusalPrefix followed by its source, for every built-in rule and
// for an operator rule; an invariant without built-in wording has no prefix; the scan-bound
// refusal names both bounds; and every text passes the caveman runtime check the register
// census applies to praetor's own guard.
func TestRefusalWording(t *testing.T) {
	builtin := policy(t)
	for i, rule := range BuiltinRules() {
		if compiled := builtin.rules[i]; compiled.source != rule.Source || compiled.prefix != rule.RefusalPrefix() {
			t.Errorf("rule %d: compiled %q with prefix %q, exported %+v", i, compiled.source, compiled.prefix, rule)
		}
	}
	for command, rule := range map[string]BuiltinRule{
		"git push --no-verify": BuiltinRules()[0], "praetorctl adopt /srv/dev": BuiltinRules()[len(builtinEvasion)],
	} {
		if verdict := builtin.Command(command); verdict.Reason != rule.RefusalPrefix()+rule.Source {
			t.Errorf("%q: reason %q", command, verdict.Reason)
		}
	}
	for _, rule := range BuiltinRules() {
		if prefix := rule.RefusalPrefix(); !strings.HasPrefix(prefix, "[BLOCKED BY "+rule.Invariant+"] ") || !strings.HasSuffix(prefix, "; pattern: ") {
			t.Errorf("rule %q: prefix %q", rule.Source, prefix)
		}
	}
	if prefix := (BuiltinRule{Source: "x", Invariant: "operator"}).RefusalPrefix(); prefix != "" {
		t.Errorf("an invariant without built-in wording got a prefix: %q", prefix)
	}
	if verdict := policy(t, `\bterraform\b`).Command("terraform plan"); verdict.Reason != rulePrefix("operator", operatorMessage)+`\bterraform\b` {
		t.Errorf("operator reason: %q", verdict.Reason)
	}
	scan := ScanBoundRefusal()
	if !strings.Contains(scan, strconv.Itoa(MaxScanChars)+" characters, "+strconv.Itoa(MaxScanLineChars)+" per line") {
		t.Errorf("scan-bound refusal does not name both bounds: %q", scan)
	}
	texts := []string{scan, InvalidInputRefusal + "x", NarrowingRefusal, rulePrefix("operator", operatorMessage) + "x"}
	for _, value := range LefthookDisableValues() {
		texts = append(texts, LefthookDisabledRefusal(value))
	}
	for _, rule := range BuiltinRules() {
		texts = append(texts, rule.RefusalPrefix()+"x")
	}
	for _, text := range texts {
		if report := caveman.CheckRuntime(text, caveman.Options{Kind: caveman.KindMessage}); !report.Passed() {
			t.Errorf("%q fails the caveman runtime check: %+v", text, report.Findings)
		}
	}
}

// TestEnvironmentRefusals: the environment check prints the exported refusals, the boundary
// being a LEFTHOOK value that differs from a disabling one only by case.
func TestEnvironmentRefusals(t *testing.T) {
	for _, value := range LefthookDisableValues() {
		verdict := Environment(func(key string) string { return map[string]string{"LEFTHOOK": value}[key] })
		if verdict.Outcome != Deny || verdict.Reason != LefthookDisabledRefusal(value) {
			t.Errorf("LEFTHOOK=%s: %+v", value, verdict)
		}
	}
	for _, name := range LefthookNarrowingVariables() {
		verdict := Environment(func(key string) string { return map[string]string{name: "x"}[key] })
		if verdict.Outcome != Deny || verdict.Reason != NarrowingRefusal {
			t.Errorf("%s: %+v", name, verdict)
		}
	}
	if verdict := Environment(func(key string) string { return map[string]string{"LEFTHOOK": "False"}[key] }); verdict.Outcome != Allow {
		t.Errorf("LEFTHOOK=False does not disable Lefthook: %+v", verdict)
	}
}
