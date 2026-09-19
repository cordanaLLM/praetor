package agenthook

import (
	"strings"
	"testing"

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
		{"LEFTHOOK", "0", Deny}, {"LEFTHOOK", "1", Allow}, {"LEFTHOOK", "00", Allow}, {"LEFTHOOK", "false", Allow},
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
