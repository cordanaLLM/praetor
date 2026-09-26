package agenthook

import (
	"fmt"
	"regexp"
	"slices"
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

// hooksDir matches the repository hooks directory in either path separator.
const hooksDir = `\.git[/\\]hooks`

// builtinEvasion are the engine's evasion patterns. praetor's own Python guard
// (`.config/agent/hooks/block_evasion.py`) carries the same list byte for byte
// (TestPythonGuardCarriesTheBuiltinEvasionList), and adoption renders the interceptor it
// writes from BuiltinRules, so the three enforce one rule set. They judge the command text;
// judging the act instead is tracked separately and happens on this implementation only.
//
// Every source is valid in both RE2 and Python's re, names quotes as \x22 and \x27 so it
// embeds in a Python raw string, and uses `\s` outside bracket expressions only.
//
//   - A short skip flag counts inside a bundle of flags that take no argument (`-an`,
//     `-sn`; gitcli(7) bundles short options), and after Git's global options
//     (`git -C dir commit -n`). A bundle whose skip letter follows an argument option
//     (`-mn`) is a message, not a skip.
//   - Lefthook is disabled by LEFTHOOK=0 and by LEFTHOOK=false (lefthook v2.1.14,
//     internal/command/run.go).
//   - core.hooksPath is refused in any assignment form: `=`, a space-separated value, any
//     path. Reading it (no value) stays allowed.
//   - The hooks directory is refused as the operand of a command that removes, moves,
//     rewrites or re-permissions it, as a redirect target, and `lefthook uninstall` is
//     refused outright. Reading it (ls, cat, sed -n, find without an action) stays allowed.
var builtinEvasion = []string{
	`--no-verify\b`,
	`\bgit(\s+-[Cc]\s+(\x22[^\x22]*\x22|\x27[^\x27]*\x27|[^ \t\n]+)|\s+--?[A-Za-z][-A-Za-z]*(=[^ \t\n]+)?)*\s+commit\b[^\n]*\s-[aeiopqsvz]*n`,
	`LEFTHOOK=[\x22\x27]?(0|false)\b`,
	`SKIP=.*git`,
	`(?i:core\.hookspath)(\s*=|\s+[\x22\x27]?[/~.$A-Za-z_\\])`,
	`\b(rm|rmdir|unlink|mv|cp|ln|chmod|chown|chattr|truncate|shred|tee)\b[^\n]*` + hooksDir,
	`\b(sed|perl)\b[^\n]*\s(-[A-Za-z]*i|--in-place)[^\n]*` + hooksDir,
	`\bfind\b[^\n]*` + hooksDir + `[^\n]*\s-(delete|exec|execdir|ok)\b`,
	`>\s*[\x22\x27]?[^ \t\n\x22\x27]*` + hooksDir,
	`\blefthook\s+uninstall\b`,
}

// builtinDevRoot is the generic half of the topology rule: it names no organisation.
// Organisation containers are operator data and arrive through the operator deny list.
const builtinDevRoot = `(?i)(standardsctl|praetorctl)\s+(adopt|conform|bootstrap|needs\s+(scan|report|migrate|epic))\b.*\bdev/?(\s|$)`

// BuiltinRule is one built-in command rule as source text: the Python-compatible pattern
// the policy compiles and the invariant a match reports.
type BuiltinRule struct {
	Source    string
	Invariant string
}

// BuiltinRules returns the engine's built-in command rules in evaluation order, evasion
// first and the dev-root rule last. It is a fresh slice on every call.
func BuiltinRules() []BuiltinRule {
	rules := make([]BuiltinRule, 0, len(builtinEvasion)+1)
	for _, source := range builtinEvasion {
		rules = append(rules, BuiltinRule{Source: source, Invariant: "HISS"})
	}
	return append(rules, BuiltinRule{Source: builtinDevRoot, Invariant: "DEV-01"})
}

// builtinMessages words a built-in rule's denial by the invariant it enforces.
var builtinMessages = map[string]string{"HISS": evasionMessage, "DEV-01": topologyMessage}

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
	builtins := BuiltinRules()
	rules := make([]denyRule, 0, len(builtins)+len(operatorDeny))
	for _, rule := range builtins {
		rules = append(rules, builtinRule(rule.Source, rule.Invariant, builtinMessages[rule.Invariant]))
	}
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
		return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] no command policy is loaded"}
	}
	for _, rule := range p.rules {
		if rule.pattern.MatchString(command) {
			return Verdict{Outcome: Deny, Reason: fmt.Sprintf("[BLOCKED BY %s] %s (pattern %q)", rule.invariant, rule.message, rule.source)}
		}
	}
	return Verdict{Outcome: Allow}
}

// lefthookDisableValues are the LEFTHOOK values that make Lefthook skip every hook. Lefthook
// compares exactly "0" and "false" (lefthook v2.1.14, internal/command/run.go); only "0" was
// checked before, so LEFTHOOK=false passed.
var lefthookDisableValues = []string{"0", "false"}

// lefthookNarrowingVariables narrow a Lefthook run to fewer jobs when set to anything.
var lefthookNarrowingVariables = []string{"LEFTHOOK_EXCLUDE", "LEFTHOOK_SKIP"}

// LefthookDisableValues returns the LEFTHOOK values the environment check denies.
func LefthookDisableValues() []string { return slices.Clone(lefthookDisableValues) }

// LefthookNarrowingVariables returns the variables whose presence the environment check denies.
func LefthookNarrowingVariables() []string { return slices.Clone(lefthookNarrowingVariables) }

// Environment judges the hook process environment: a disabled or narrowed Lefthook run
// is an evasion whatever the command is. getenv is injected so the check never reads or
// changes process state in tests.
func Environment(getenv func(string) string) Verdict {
	if getenv == nil {
		return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] no environment to inspect"}
	}
	if value := getenv("LEFTHOOK"); slices.Contains(lefthookDisableValues, value) {
		return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] LEFTHOOK=" + value + " detected in environment. Evasion prohibited."}
	}
	for _, name := range lefthookNarrowingVariables {
		if getenv(name) != "" {
			return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] Hook exclusions are prohibited."}
		}
	}
	return Verdict{Outcome: Allow}
}
