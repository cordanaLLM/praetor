package agenthook

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// denyRule is one compiled pattern with its source and the refusal a match prints ahead of
// the source (rulePrefix), which names the invariant the rule enforces.
type denyRule struct {
	pattern *regexp.Regexp
	source  string
	prefix  string
}

// pythonSpace is what `\s` matches in Python's `re` on text. RE2's `\s` is ASCII only and
// has no vertical tab, so a one-to-one port of the built-in rules spells the class out.
const pythonSpace = `[\s\v\x1c-\x1f\x{85}\p{Z}]`

// builtinRule compiles a built-in source with Python's whitespace class. The sources use
// `\s` outside bracket expressions only; a test compiles every rule.
func builtinRule(source, invariant, message string) denyRule {
	return denyRule{regexp.MustCompile(strings.ReplaceAll(source, `\s`, pythonSpace)), source, rulePrefix(invariant, message)}
}

// Refusal wording, one source for every engine: this policy, praetor's own Python guard
// (`.config/agent/hooks/block_evasion.py`) and the interceptor adoption renders
// (buildBlockEvasionPY in internal/adopt). The interceptor is rendered from these values;
// the guard carries them as literals its register census lints, and parity_test.go
// compares its full refusal text with this package's for every case, so neither drifts.
// The wording is caveman (internal register): the census rejects articles and copulas.
const (
	evasionMessage  = "verification evasion prohibited; commits, pushes and tool calls pass verification gates"
	topologyMessage = "adoption or needs target: workstation dev root; repositories live inside organization folders as leaf Git repositories"
	operatorMessage = "command matches operator command policy"
	// InvalidInputRefusal prefixes the refusal of hook input no dialect reads; the reason the
	// input was refused follows it.
	InvalidInputRefusal = "[BLOCKED BY HISS] Invalid hook input: "
	// NarrowingRefusal refuses a Lefthook run narrowed by a LefthookNarrowingVariables entry.
	NarrowingRefusal = "[BLOCKED BY HISS] hook exclusions: prohibited."
)

// refusal renders one refusal: the invariant marker, then the message.
func refusal(invariant, message string) string {
	return "[BLOCKED BY " + invariant + "] " + message
}

// rulePrefix is the refusal a rule match prints; the rule's source follows it.
func rulePrefix(invariant, message string) string {
	return refusal(invariant, message+"; pattern: ")
}

// ScanBoundRefusal is the refusal of a command over the scan bounds of the Python adapters
// (MaxScanChars, MaxScanLineChars). The Go policy scans any length in linear time and never
// prints it; the Python guard and the rendered interceptor do, before any rule runs.
func ScanBoundRefusal() string {
	return refusal("HISS", fmt.Sprintf("command exceeds scan bound: at most %d characters, %d per line; "+
		"split command or write long content to file first", MaxScanChars, MaxScanLineChars))
}

// LefthookDisabledRefusal refuses a LEFTHOOK value from LefthookDisableValues.
func LefthookDisabledRefusal(value string) string {
	return refusal("HISS", "LEFTHOOK="+value+" detected in environment. Evasion prohibited.")
}

// hooksDir matches the repository hooks directory in either path separator and any letter
// case: Windows file systems resolve `.GIT\Hooks` to the same directory.
const hooksDir = `(?i:\.git[/\\]hooks)`

// builtinEvasion are the engine's evasion patterns. praetor's own Python guard
// (`.config/agent/hooks/block_evasion.py`) carries the same list byte for byte
// (TestPythonGuardCarriesTheBuiltinEvasionList), and adoption renders the interceptor it
// writes from BuiltinRules, so the three enforce one rule set. They judge the command text;
// judging the act instead is tracked separately and happens on this implementation only.
//
// Every source is valid in both RE2 and Python's re, names quotes as \x22 and \x27 so it
// embeds in a Python raw string, and uses `\s` outside bracket expressions only.
//
//   - The long skip option counts in every abbreviation down to `--no-v`: Git's option
//     parser resolves any unambiguous prefix (gitcli(7)), `--no-veri` on commit, push and
//     rebase, `--no-v` on am. A shorter prefix that collides with `--no-verbose` is rejected
//     by Git itself, so refusing it too costs nothing.
//   - A short skip flag counts inside a bundle of flags that take no argument (`-an`,
//     `-sn`; gitcli(7) bundles short options), and after Git's global options
//     (`git -C dir commit -n`). A bundle whose skip letter follows an argument option
//     (`-mn`) is a message, not a skip. `git am` has the same short flag (it skips
//     pre-applypatch and applypatch-msg) with its own argument-free letters; its `-m` keeps
//     the message id and takes no argument.
//   - The global options before `commit` parse one way only, because Python's re backtracks
//     through every parse when nothing matches: a bare -C or -c is reachable through the
//     argument alternative alone, and the options are separated by the shell's blanks (space,
//     tab) only, which no option or argument token contains. `-[A-Za-bd-z]` still admitted
//     `-C` and `\s` separators also matched the \r, \v and Unicode spaces the tokens admit, so
//     `git -C -C ... status` took seconds per two more options (the corpus holds both shapes).
//     A space other than a blank does not split a shell word, so it separates no option.
//   - Lefthook is disabled by LEFTHOOK=0 and by LEFTHOOK=false (lefthook v2.1.14,
//     internal/command/run.go).
//   - core.hooksPath is refused in any assignment form: `=`, a space-separated value, any
//     path. Reading it (no value) stays allowed.
//   - The hooks directory is refused as the operand of a command that removes, moves,
//     rewrites or re-permissions it, as a redirect target, and `lefthook uninstall` is
//     refused outright. Reading it (ls, cat, sed -n, find without an action) stays allowed.
//     The command names include cmd.exe's and PowerShell's (del, erase, rd, Remove-Item and
//     its ri alias, move, ren, copy, Set-Content, Out-File, icacls, attrib), matched in any
//     letter case as both shells do; a `cmd /c` or `powershell -c` wrapper still carries the
//     inner command in the text.
var builtinEvasion = []string{
	`--no-v(e(r(i(f(y)?)?)?)?)?\b`,
	`\bgit([ \t]+-[Cc][ \t]+(\x22[^\x22]*\x22|\x27[^\x27]*\x27|[^ \t\n\x22\x27][^ \t\n]*)|[ \t]+(--[A-Za-z][-A-Za-z]*|-[ABD-Zabd-z][-A-Za-z]*|-[Cc][A-Za-z]+)(=[^ \t\n]+)?)*\s+(commit\b[^\n]*\s-[aeiopqsvz]*|am\b[^\n]*\s-[3cikmqsu]*)n`,
	`LEFTHOOK=[\x22\x27]?(0|false)\b`,
	`SKIP=.*git`,
	`(?i:core\.hookspath)(\s*=|\s+[\x22\x27]?[/~.$A-Za-z_\\])`,
	`\b(?i:rm|rmdir|unlink|mv|cp|ln|chmod|chown|chattr|truncate|shred|tee|del|erase|rd|ri|remove-item|move|move-item|ren|rename|rename-item|copy|copy-item|set-content|add-content|out-file|icacls|attrib)\b[^\n]*` + hooksDir,
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

// RefusalPrefix is the refusal a match of the rule prints ahead of its Source: the whole
// deny reason is RefusalPrefix() + Source. It is empty for an invariant without built-in
// wording.
func (r BuiltinRule) RefusalPrefix() string {
	message, ok := builtinMessages[r.Invariant]
	if !ok {
		return ""
	}
	return rulePrefix(r.Invariant, message)
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
		rules = append(rules, denyRule{compiled, source, rulePrefix("operator", operatorMessage)})
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
			return Verdict{Outcome: Deny, Reason: rule.prefix + rule.source}
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
		return Verdict{Outcome: Deny, Reason: LefthookDisabledRefusal(value)}
	}
	for _, name := range lefthookNarrowingVariables {
		if getenv(name) != "" {
			return Verdict{Outcome: Deny, Reason: NarrowingRefusal}
		}
	}
	return Verdict{Outcome: Allow}
}
