package agenthook

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// denyRule is one compiled pattern with its source and the refusal a match prints ahead of
// the source (rulePrefix), which names the invariant the rule enforces. A readOnlyExempt rule
// judges the command without the words of chained read-only commands (withoutReadOnlyWords).
type denyRule struct {
	pattern        *regexp.Regexp
	source         string
	prefix         string
	readOnlyExempt bool
}

// pythonSpace is what `\s` matches in Python's `re` on text. RE2's `\s` is ASCII only and
// has no vertical tab, so a one-to-one port of the built-in rules spells the class out.
const pythonSpace = `[\s\v\x1c-\x1f\x{85}\p{Z}]`

// builtinRule compiles a built-in source with Python's whitespace class. The sources use
// `\s` outside bracket expressions only; a test compiles every rule.
func builtinRule(rule BuiltinRule, message string) denyRule {
	return denyRule{
		pattern:        regexp.MustCompile(strings.ReplaceAll(rule.Source, `\s`, pythonSpace)),
		source:         rule.Source,
		prefix:         rulePrefix(rule.Invariant, message),
		readOnlyExempt: rule.ReadOnlyExempt,
	}
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

// shortSkipFlagRule refuses the short skip flag of git commit and git am (builtinEvasion).
const shortSkipFlagRule = `\bgit([ \t]+-[Cc][ \t]+(\x22[^\x22]*\x22|\x27[^\x27]*\x27|[^ \t\n\x22\x27][^ \t\n]*)|[ \t]+(--[A-Za-z][-A-Za-z]*|-[ABD-Zabd-z][-A-Za-z]*|-[Cc][A-Za-z]+)(=[^ \t\n]+)?)*\s+(commit\b[^\n]*\s-[aeiopqsvz]*|am\b[^\n]*\s-[3cikmqsu]*)n`

// skipVariableRule refuses the skip variable ahead of a Git call (builtinEvasion).
const skipVariableRule = `SKIP=.*git`

// The in-place edit rule and the find rule (builtinEvasion) are a command word, the first one
// of its line (lineThroughFirstWord), followed by a tail that starts with a `[^\n]*` gap. The
// words are lowercase ASCII letters with pairwise distinct first letters within a list.
var (
	inPlaceEditorWords = []string{"sed", "perl"}
	findWords          = []string{"find"}
)

const (
	inPlaceEditTail = `[^\n]*\s(-[A-Za-z]*i|--in-place)[^\n]*` + hooksDir
	findTail        = `[^\n]*` + hooksDir + `[^\n]*\s-(delete|exec|execdir|ok)\b`
)

// lineThroughFirstWord returns a pattern for a line from its start through the first whole
// word there (`\b` on both sides) that is one of words, captured as group 1. A rule starts
// with it so Python's re starts that rule once per line. Unanchored, re retries the rule at
// every occurrence of the word and backtracks through every later part from each one:
// `\bfind\b[^\n]*X[^\n]*Y` on a line of find words and X costs cubic time in the line length;
// anchored, quadratic.
//
// The anchor keeps a rule's language when a `[^\n]*` gap follows the word: a match from a
// later occurrence of a word on the line is then a match from the first. Each repetition takes
// one maximal run of word characters other than words, or none, and the one character after
// it that is neither a word character nor a line break; `\w` is what each engine's own `\b`
// reads (ASCII in RE2, Unicode in Python's re), so both find the occurrence `\bword\b` finds.
// The run alternatives start with distinct letters and a run must end before a non-word
// character, so a run parses one way only. TestAnchoredRulesKeepTheirLanguage replays both
// rules against their unanchored form in RE2 and in Python's re.
func lineThroughFirstWord(words ...string) string {
	firsts := ""
	runs := make([]string, 0, len(words)+1)
	for _, word := range words {
		firsts += word[:1]
		rest := `\w+`
		for index := len(word) - 1; index > 0; index-- {
			letter := word[index : index+1]
			rest = `(?:[^\W` + letter + `]\w*|` + letter + rest + `)?`
		}
		runs = append(runs, word[:1]+rest)
	}
	runs = append([]string{`[^\W` + firsts + `]\w*`}, runs...)
	return `(?m)^(?:(?:` + strings.Join(runs, "|") + `)?[^\w\n])*(` + strings.Join(words, "|") + `)\b`
}

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
//   - The in-place edit rule and the find rule start at the first sed or perl word, and the
//     first find word, of a line (lineThroughFirstWord): they refuse what `\b(sed|perl)\b` and
//     `\bfind\b` at any word did, and Python's re no longer retries them at every such word,
//     which cost cubic time in the line length (#829).
//
// The short skip flag rule and the skip variable rule judge the command without the words of
// chained read-only commands (readOnlyExemptRules); every other rule judges the whole command.
var builtinEvasion = []string{
	`--no-v(e(r(i(f(y)?)?)?)?)?\b`,
	shortSkipFlagRule,
	`LEFTHOOK=[\x22\x27]?(0|false)\b`,
	skipVariableRule,
	`(?i:core\.hookspath)(\s*=|\s+[\x22\x27]?[/~.$A-Za-z_\\])`,
	`\b(?i:rm|rmdir|unlink|mv|cp|ln|chmod|chown|chattr|truncate|shred|tee|del|erase|rd|ri|remove-item|move|move-item|ren|rename|rename-item|copy|copy-item|set-content|add-content|out-file|icacls|attrib)\b[^\n]*` + hooksDir,
	lineThroughFirstWord(inPlaceEditorWords...) + inPlaceEditTail,
	lineThroughFirstWord(findWords...) + findTail,
	`>\s*[\x22\x27]?[^ \t\n\x22\x27]*` + hooksDir,
	`\blefthook\s+uninstall\b`,
}

// readOnlyExemptRules are the built-in rules that judge the command without the words of
// chained read-only commands (withoutReadOnlyWords): a short skip flag or a skip variable
// mention that belongs to `git log -n 5` after a commit is no skip (#46). The rule sources
// stay as they are; only the text they judge loses those words. Every other
// rule, and every operator rule, judges the whole command.
var readOnlyExemptRules = []string{shortSkipFlagRule, skipVariableRule}

// ReadOnlyWords matches one read-only command that a plain separator starts, with its words.
// The separator is `;`, `&&`, `||` or `|`; the command is git log, show, status, diff or
// rev-parse (with or without --no-pager), head, tail, grep, wc, or sed with -n as its first
// word. Group 1, or group 2 for sed, holds the separator and the command name;
// withoutReadOnlyWords keeps it and drops the words after it, so the skip variable rule still
// sees the Git call. A word is printable ASCII without a quote, escape, expansion,
// substitution, redirection, comment or separator, so the match stops before any text the
// shell could read as more than plain words of that command: a quoted argument and every word
// after it stay in the judged text.
//
// Why a dropped word is never a skip: the dropped text holds no quote, so it lies wholly
// inside or wholly outside any quoted string that is open at the separator. Outside, the
// separator ends the commit and the word is an argument of the read-only command. Inside, the
// word is part of one quoted argument and never a flag of its own. ReadOnlyVeto rules out the
// constructs that break this reading: escapes, expansions, line breaks and redefined names.
//
// The pattern is valid in RE2 and Python's re and linear in both: each alternative starts at
// a separator, and no quantified group can match its own text in two ways (the blank and word
// classes are disjoint), so Python's backtracking tries every start once.
const ReadOnlyWords = `((?:;|&&|\|\|?)[ \t]*(?:git[ \t]+(?:--no-pager[ \t]+)?(?:log|show|status|diff|rev-parse)|head|tail|grep|wc))(?:[ \t]+[-A-Za-z0-9_./:=@,+~*?]+)*|((?:;|&&|\|\|?)[ \t]*sed)[ \t]+-n\b(?:[ \t]+[-A-Za-z0-9_./:=@,+~*?]+)*`

// ReadOnlyVeto matches a command the exemption does not apply to: one holding, anywhere, a
// construct that can move a command boundary, hide a word inside another construct or
// redefine a command name. Every rule then judges the whole command, exactly as without the
// exemption. The constructs are
//
//   - a character outside printable ASCII and tab, line breaks included: a quoted string can
//     continue on a later line, and a backslash line continuation joins two lines;
//   - an escape or command substitution (`\`, a backtick, cmd.exe `^`);
//   - an expansion, substitution, group or subshell (`$`, `(`, `)`, `{`, `}`), a redirection
//     or here-document (`<`, `>`), history expansion or negation (`!`);
//   - PowerShell's stop-parsing token `--%`, after which `;` and `&` are plain text, and a
//     cmd.exe `%` right before a separator, where an expanded `^` would escape it;
//   - a word that exports a variable or redefines a command (export, declare, typeset, set,
//     which covers `set -a`, setenv, alias, function, hash, cmd.exe doskey, PowerShell sal,
//     nal, Set-Alias, New-Alias), as a whole word anywhere, a quoted message included, in any
//     letter case.
//
// It is linear in both engines: one character class or a fixed word per alternative.
const ReadOnlyVeto = `[^\t\x20-\x7e]|[\\\x60$(){}<>^!]|--%|%[;&|]|(?i:(?:^|[^-A-Za-z0-9_])(?:export|declare|typeset|set|setenv|alias|function|sal|nal|set-alias|new-alias|doskey|hash)(?:[^-A-Za-z0-9_]|$))`

// ReadOnlyKept names the groups of ReadOnlyWords a replacement keeps, in RE2's replacement
// syntax; Python's re.sub writes the same groups as \1\2.
const ReadOnlyKept = "${1}${2}"

var (
	readOnlyWordsPattern = regexp.MustCompile(ReadOnlyWords)
	readOnlyVetoPattern  = regexp.MustCompile(ReadOnlyVeto)
)

// withoutReadOnlyWords returns command without the words of every read-only command
// ReadOnlyWords matches, separators and command names kept, or command unchanged when
// ReadOnlyVeto matches anywhere in it.
func withoutReadOnlyWords(command string) string {
	if readOnlyVetoPattern.MatchString(command) {
		return command
	}
	return readOnlyWordsPattern.ReplaceAllString(command, ReadOnlyKept)
}

// builtinDevRoot is the generic half of the topology rule: it names no organisation.
// Organisation containers are operator data and arrive through the operator deny list.
const builtinDevRoot = `(?i)(standardsctl|praetorctl)\s+(adopt|conform|bootstrap|needs\s+(scan|report|migrate|epic))\b.*\bdev/?(\s|$)`

// BuiltinRule is one built-in command rule as source text: the Python-compatible pattern
// the policy compiles and the invariant a match reports. A ReadOnlyExempt rule judges the
// command without the words of chained read-only commands: those ReadOnlyWords matches,
// unless ReadOnlyVeto matches.
type BuiltinRule struct {
	Source         string
	Invariant      string
	ReadOnlyExempt bool
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
		rules = append(rules, BuiltinRule{Source: source, Invariant: "HISS", ReadOnlyExempt: slices.Contains(readOnlyExemptRules, source)})
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
		rules = append(rules, builtinRule(rule, builtinMessages[rule.Invariant]))
	}
	for index, source := range operatorDeny {
		compiled, err := regexp.Compile(source)
		if err != nil {
			return nil, fmt.Errorf("operator deny pattern %d: %w", index, err)
		}
		rules = append(rules, denyRule{pattern: compiled, source: source, prefix: rulePrefix("operator", operatorMessage)})
	}
	return &Policy{rules: rules}, nil
}

// Command judges one proposed command line. The first matching rule denies; a
// readOnlyExempt rule judges the command without the words of chained read-only commands.
func (p *Policy) Command(command string) Verdict {
	if p == nil {
		return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] no command policy is loaded"}
	}
	judged := withoutReadOnlyWords(command)
	for _, rule := range p.rules {
		text := command
		if rule.readOnlyExempt {
			text = judged
		}
		if rule.pattern.MatchString(text) {
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
