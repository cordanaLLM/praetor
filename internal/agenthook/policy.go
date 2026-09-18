package agenthook

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// denyRule is one compiled pattern with its source, the invariant it enforces and its wording.
type denyRule struct {
	pattern   *regexp.Regexp
	source    string
	invariant string
	message   string
}

// pythonSpace is what `\s` matches in Python's `re` on text. RE2's `\s` is ASCII only and
// has no vertical tab, so a one-to-one port of the built-in rules spells the class out.
const pythonSpace = `[\s\v\x1c-\x1f\x{85}\p{Z}]`

// builtinRule compiles a built-in source with Python's whitespace class. The sources use
// `\s` outside bracket expressions only; a test compiles every rule.
func builtinRule(source, invariant, message string) denyRule {
	return denyRule{regexp.MustCompile(strings.ReplaceAll(source, `\s`, pythonSpace)), source, invariant, message}
}

const (
	evasionMessage  = "attempted verification evasion; every commit, push and tool call passes the verification gates"
	topologyMessage = "adoption or needs target is the workstation dev root; repositories are leaf directories inside an organisation folder"
	operatorMessage = "command matches the operator command policy"
)

// builtinEvasion are the engine's evasion patterns, ported one to one from the Python
// guard (`.config/agent/hooks/block_evasion.py`). They judge the command text; judging
// the act instead is tracked separately and happens on this implementation only.
var builtinEvasion = []string{
	`--no-verify\b`,
	`\bgit\s+commit\b[^\n]*\s-n\b`,
	`LEFTHOOK=0\b`,
	`SKIP=.*git`,
	`core\.hooksPath\s*=\s*/dev/null`,
	`rm\s+(-rf?\s+)?\.git/hooks`,
}

// builtinDevRoot is the generic half of the topology rule: it names no organisation.
// Organisation containers are operator data and arrive through the operator deny list.
const builtinDevRoot = `(?i)(standardsctl|praetorctl)\s+(adopt|conform|bootstrap|needs\s+(scan|report|migrate|epic))\b.*\bdev/?(\s|$)`

// Policy is the compiled command policy: built-in rules first, operator rules after.
type Policy struct {
	rules []denyRule
}

// NewPolicy compiles the built-in rules plus the operator deny patterns (RE2). It fails
// on a pattern that is empty, oversized or does not compile, and on a list over the bound;
// a policy is never built from a partially accepted list. The bound and the per-pattern
// checks are config's (`config.ValidateCommandPolicyDeny`), so this package keeps no copy.
func NewPolicy(operatorDeny []string) (*Policy, error) {
	if err := config.ValidateCommandPolicyDeny(operatorDeny); err != nil {
		return nil, err
	}
	rules := make([]denyRule, 0, len(builtinEvasion)+1+len(operatorDeny))
	for _, source := range builtinEvasion {
		rules = append(rules, builtinRule(source, "HISS-16", evasionMessage))
	}
	rules = append(rules, builtinRule(builtinDevRoot, "DEV-01", topologyMessage))
	for index, source := range operatorDeny {
		compiled, err := regexp.Compile(source)
		if err != nil {
			return nil, fmt.Errorf("operator deny pattern %d: %w", index, err)
		}
		rules = append(rules, denyRule{compiled, source, "operator", operatorMessage})
	}
	return &Policy{rules: rules}, nil
}

// Command judges one proposed command line. The first matching rule denies.
func (p *Policy) Command(command string) Verdict {
	if p == nil {
		return Verdict{Deny, "[BLOCKED BY HISS-16] no command policy is loaded"}
	}
	for _, rule := range p.rules {
		if rule.pattern.MatchString(command) {
			return Verdict{Deny, fmt.Sprintf("[BLOCKED BY %s] %s (pattern %q)", rule.invariant, rule.message, rule.source)}
		}
	}
	return Verdict{Outcome: Allow}
}

// Environment judges the hook process environment: a disabled or narrowed Lefthook run
// is an evasion whatever the command is. getenv is injected so the check never reads or
// changes process state in tests.
func Environment(getenv func(string) string) Verdict {
	if getenv == nil {
		return Verdict{Deny, "[BLOCKED BY HISS-16] no environment to inspect"}
	}
	if getenv("LEFTHOOK") == "0" {
		return Verdict{Deny, "[BLOCKED BY HISS-16] LEFTHOOK=0 detected in environment. Evasion prohibited."}
	}
	if getenv("LEFTHOOK_EXCLUDE") != "" || getenv("LEFTHOOK_SKIP") != "" {
		return Verdict{Deny, "[BLOCKED BY HISS-16] Hook exclusions are prohibited."}
	}
	return Verdict{Outcome: Allow}
}
