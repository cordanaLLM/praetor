package paperclip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// Positive: the rules written for a synthesized harness pass markdownlint's default rules,
// and they carry no markdownlint disable at all.
func TestRenderedRulesPassDefaultMarkdownlint(t *testing.T) {
	repo := identifiedRepo(t)
	h, err := SynthesizeHarness(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteHarness(h, repo); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, paperclipDir, rulesFile))
	if err != nil {
		t.Fatal(err)
	}
	if findings := testsupport.MarkdownFindings(string(data)); len(findings) != 0 {
		t.Fatalf("rules.md breaks markdownlint defaults: %v\n%s", findings, data)
	}
	// An unwrappable command fence may carry an MD013 disable, but only one re-enabled right
	// after that fence: a file-wide disable would also silence the adopter's own additions
	// (BUG-806).
	text := string(data)
	if strings.Count(text, "markdownlint-disable") != strings.Count(text, "markdownlint-enable") {
		t.Fatalf("rules.md leaves a markdownlint rule disabled past its fence:\n%s", data)
	}
	for _, block := range strings.Split(text, "<!-- markdownlint-disable MD013 -->")[1:] {
		fenceEnd := strings.Index(block, "```\n\n<!-- markdownlint-enable MD013 -->")
		if fenceEnd < 0 || strings.Count(block[:fenceEnd], "```") != 1 {
			t.Fatalf("an MD013 disable must cover exactly one command fence:\n%s", data)
		}
	}
}

// Negative: the rules shape written before this fix, a heading directly followed by its
// list and fence, is reported, so the positive test is not passing on a lenient checker.
func TestPreviousRulesShapeFailsMarkdownlint(t *testing.T) {
	previous := "# Paperclip Operating Rules (acme)\n\n## Operating Contract\n- rule\n\n" +
		"## AGit Push Protocol\n```bash\ngit push origin HEAD:refs/for/main\n```\n\n" +
		"## High-Integrity Invariants\n- HISS-01\n"
	findings := strings.Join(testsupport.MarkdownFindings(previous), "\n")
	for _, rule := range []string{"MD022", "MD031", "MD032"} {
		if !strings.Contains(findings, rule) {
			t.Errorf("previous rules shape not reported for %s: %s", rule, findings)
		}
	}
}

// Boundary: a contract line far over the limit wraps into lines of at most the limit that
// read back as the original text; a word longer than the limit keeps its own line; a word
// that would open a list, heading, fence or HTML block never starts a line; and a push command too long to
// wrap gets an MD013 disable scoped to its fence alone.
func TestRenderedRulesWrapAtTheLineLimit(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("contract clause word ", 30))
	token := strings.Repeat("x", 2*markdownLineLimit)
	marker := strings.Repeat("abc ", 19) + "- 1. # tail"
	opener := strings.Repeat("abc ", 19) + "``` === <div> ~~~ tail"
	command := "git push origin HEAD:refs/for/main -o topic=" + strings.Repeat("t", markdownLineLimit)
	h := &Harness{Version: 1, Platform: "acme", OperatingContract: []string{long, "short " + token, marker, opener}, AGitPushFormat: command, Invariants: []string{"HISS-01"}}
	rules := renderRules(h)
	if findings := testsupport.MarkdownFindings(rules); len(findings) != 0 {
		t.Fatalf("wrapped rules break markdownlint defaults: %v\n%s", findings, rules)
	}
	unwrapped := strings.ReplaceAll(rules, "\n  ", " ")
	for _, item := range []string{long, "short " + token, marker, opener} {
		if !strings.Contains(unwrapped, "- "+item+"\n") {
			t.Errorf("item %q did not survive wrapping:\n%s", item, rules)
		}
	}
	for _, line := range strings.Split(rules, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(line) > markdownLineLimit && trimmed != token && !strings.HasPrefix(line, "git push") {
			t.Errorf("line over %d characters: %q", markdownLineLimit, line)
		}
		if strings.HasPrefix(line, "  ") && !safeLineStart(trimmed) {
			t.Errorf("continuation line opens a new block: %q", line)
		}
	}
	if strings.Count(rules, "<!-- markdownlint-disable MD013 -->") != 1 || strings.Count(rules, "<!-- markdownlint-enable MD013 -->") != 1 {
		t.Fatalf("long push command not scoped by one disable/enable pair:\n%s", rules)
	}
}
