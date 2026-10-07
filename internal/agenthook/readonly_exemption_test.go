package agenthook

import (
	"strings"
	"testing"
)

// wholeCommandDenies judges command with every rule on the whole command, the policy without
// the read-only exemption: what the engine refused before #46.
func wholeCommandDenies(p *Policy, command string) bool {
	for _, rule := range p.rules {
		if rule.pattern.MatchString(command) {
			return true
		}
	}
	return false
}

// Positive: a short skip flag or a skip variable mention in the words of a read-only command
// chained after a commit is no skip. Each command is refused without the exemption, so each
// case fails when the exemption is removed.
func TestReadOnlyExemption_Positive_ChainedReadOnlyWordsAreNoSkip(t *testing.T) {
	builtin := policy(t)
	for command, judged := range map[string]string{
		"git commit -m x && git log -n 5":                           "git commit -m x && git log",
		"git commit -s -F msg; sed -n 1p README.md":                 "git commit -s -F msg; sed",
		"git commit -s -F msg; sed -n '1,5p' README.md":             "git commit -s -F msg; sed '1,5p' README.md",
		"git commit -m x | head -n 3":                               "git commit -m x | head",
		"git commit -m x || git log -n 1":                           "git commit -m x || git log",
		"git am 0001.patch && git log -n 1":                         "git am 0001.patch && git log",
		"git commit -m x &&\tgit --no-pager log -n 3 --oneline":     "git commit -m x &&\tgit --no-pager log",
		"git commit -m \"fix: x\" && git log -n 1 --format=%H":      "git commit -m \"fix: x\" && git log%H",
		"git commit -m x;git show -n 1;tail -n 5 out.log":           "git commit -m x;git show;tail",
		"git commit -m x; grep -rn SKIP= .github; git status":       "git commit -m x; grep; git status",
		"git commit -m x && git diff -n HEAD~1 | wc -l":             "git commit -m x && git diff | wc",
		"git commit -m x && git rev-parse -n HEAD && sed -n 1,5p f": "git commit -m x && git rev-parse && sed",
	} {
		if got := withoutReadOnlyWords(command); got != judged {
			t.Errorf("%q: judged as %q, want %q", command, got, judged)
		}
		if verdict := builtin.Command(command); verdict.Outcome != Allow {
			t.Errorf("%q: %+v", command, verdict)
		}
		if !wholeCommandDenies(builtin, command) {
			t.Errorf("%q: allowed without the exemption too, so it proves nothing", command)
		}
	}
}

// Negative: a real skip stays refused, whether it sits on the commit before a chained
// read-only command, on a later commit, or under an exported or redefined name; and a rule
// outside the exemption judges the words of a read-only command too.
func TestReadOnlyExemption_Negative_RealSkipsStayRefused(t *testing.T) {
	builtin := policy(t)
	for _, command := range []string{
		"git commit -n -m x && git log -n 1",
		"git commit -m x -n; git log -n 1",
		"git commit -m x && git log -n 5 && git commit --amend -n",
		"git commit -m \"a; git log -n 5\" -n",
		"git commit -m 'a; git log' -n",
		"git commit -m x ';' git log -n 5",
		"git commit -m x \"&& \"git log -n 5",
		"SKIP=lint; git status",
		"SKIP=lint git commit -m x; git log -n 1",
		"SKIP=lint; export SKIP; git commit -m x",
		"git commit -m x; git log --no-verify",
		"git status; grep -n LEFTHOOK=0 lefthook.yml",
		"git commit -m x & git log -n 1",
		"git commit -m x |& head -n 3",
	} {
		if verdict := builtin.Command(command); verdict.Outcome != Deny {
			t.Errorf("%q allowed", command)
		}
	}
}

// Boundary: each construct ReadOnlyVeto names turns the exemption off for the whole command,
// and the same command with a plain letter in its place keeps it. A word in the list counts
// only as a whole word, in any letter case.
func TestReadOnlyExemption_Boundary_VetoedConstructs(t *testing.T) {
	builtin := policy(t)
	const chained = " && git log -n 5"
	for _, token := range []string{
		"é", "\r", "\v", "\\", "`", "^", "$", "(", ")", "{", "}", "<", ">", "!", "--%",
	} {
		vetoed := "git commit -m a" + token + "b" + chained
		if verdict := builtin.Command(vetoed); verdict.Outcome != Deny {
			t.Errorf("%q: the exemption applied past %q", vetoed, token)
		}
		if plain := strings.Replace(vetoed, token, "x", 1); builtin.Command(plain).Outcome != Allow {
			t.Errorf("%q: refused without %q", plain, token)
		}
	}
	if verdict := builtin.Command("git commit -m x\n&& git log -n 5\ngit commit -m x" + chained); verdict.Outcome != Deny {
		t.Errorf("a line break kept the exemption: %+v", verdict)
	}
	for _, separator := range []string{";", "&&", "|"} {
		if builtin.Command("git commit -m x%"+separator+" git log -n 5").Outcome != Deny ||
			builtin.Command("git commit -m x% "+separator+" git log -n 5").Outcome != Allow {
			t.Errorf("cmd.exe percent before %q: want refused only when it touches the separator", separator)
		}
	}
	for _, word := range []string{
		"export", "declare", "typeset", "set", "setenv", "alias", "function", "hash", "doskey", "sal",
		"nal", "Set-Alias", "New-Alias", "EXPORT", "Set",
	} {
		if verdict := builtin.Command("git commit -m " + word + chained); verdict.Outcome != Deny {
			t.Errorf("%q kept the exemption", word)
		}
		if verdict := builtin.Command("git commit -m " + word + "s" + chained); verdict.Outcome != Allow {
			t.Errorf("%qs, no listed word, lost the exemption: %+v", word, verdict)
		}
	}
}

// Boundary: what counts as a read-only command and where its words end. sed needs -n as a
// whole first word; only ;, &&, || and | separate; the words stop at the first quote, `%` or
// other character outside the word class, and everything after that point is judged, so a
// flag after a quoted argument still counts (refused, although head would take it).
func TestReadOnlyExemption_Boundary_ReadOnlyCommandShape(t *testing.T) {
	for command, judged := range map[string]string{
		"x; sed -n":             "x; sed",
		"x; sed -ni 1d f":       "x; sed -ni 1d f",
		"x; sed -nE 1p f":       "x; sed -nE 1p f",
		"x; sed 1p f":           "x; sed 1p f",
		"x; sed -n 1p f":        "x; sed",
		"x; sed -n '1,5p' f":    "x; sed '1,5p' f",
		"x; head -n 1 \"x\" -n": "x; head \"x\" -n",
		"x; git log -n 1 %H -n": "x; git log %H -n",
		"x; grep -n a #b -n":    "x; grep #b -n",
		"x; headers -n 3":       "x; headers -n 3",
		"x; git logs -n 3":      "x; git logs -n 3",
		"x; git -C d log -n 3":  "x; git -C d log -n 3",
		"x & git log -n 3":      "x & git log -n 3",
		"x |& head -n 3":        "x |& head -n 3",
		"x;; head -n 3":         "x;; head",
		"x ; head":              "x ; head",
		"":                      "",
		";|&&||":                ";|&&||",
	} {
		if got := withoutReadOnlyWords(command); got != judged {
			t.Errorf("%q: judged as %q, want %q", command, got, judged)
		}
	}
}

// Boundary: the exemption sources keep the contract both Python renderings rely on: the
// replacement keeps groups 1 and 2, which are the only groups, and neither source uses `\s`,
// whose class differs between RE2 and Python, or a double quote, which ends a raw string.
func TestReadOnlyExemption_Boundary_SourceContract(t *testing.T) {
	if groups := readOnlyWordsPattern.NumSubexp(); groups != 2 || ReadOnlyKept != "${1}${2}" {
		t.Errorf("ReadOnlyWords has %d groups, replacement %q", groups, ReadOnlyKept)
	}
	if readOnlyVetoPattern.NumSubexp() != 0 {
		t.Errorf("ReadOnlyVeto captures a group")
	}
	for _, source := range []string{ReadOnlyWords, ReadOnlyVeto} {
		if strings.Contains(source, `\s`) || strings.ContainsAny(source, "\"\n") || strings.HasSuffix(source, `\`) {
			t.Errorf("%q cannot be shared with the Python renderings", source)
		}
	}
}
