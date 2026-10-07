package forge

import (
	"strconv"
	"strings"
	"testing"
)

// TestAnalyzeCommit_Positive_ConventionalAndGitGeneratedSubjects (#61): every accepted type, with
// and without a scope and the breaking marker, and the subjects git writes for a merge and a
// revert meet the policy.
func TestAnalyzeCommit_Positive_ConventionalAndGitGeneratedSubjects(t *testing.T) {
	for _, message := range []string{
		"build: bump", "chore(deps): update", "ci: cache", "docs(guide): explain", "feat: add",
		"fix(audit): fail closed", "perf: faster", "refactor: split", "revert: undo", "style: format",
		"test(hooks): cover", "feat(api)!: remove\n\nMigration: call the replacement",
		"Merge branch 'side'", `Revert "feat: add"`,
	} {
		if analysis, err := AnalyzeCommit(message); err != nil || !analysis.Valid {
			t.Fatalf("%q: %+v, %v", message, analysis, err)
		}
	}
}

// TestAnalyzeCommit_Negative_SubjectBreaksThePolicy (#61): a subject without a type, with an
// unknown or capitalised type, an empty scope, no space after the colon, no description, or a
// fixup prefix fails, quoting the subject.
func TestAnalyzeCommit_Negative_SubjectBreaksThePolicy(t *testing.T) {
	for _, subject := range []string{
		"bad subject", "deps: bump", "Feat: add", "feat(): add", "feat:add", "feat: ", "fixup! feat: add",
		"feat(a(b)): nested scope",
	} {
		analysis, err := AnalyzeCommit(subject + "\n\nSigned-off-by: Test <test@example.invalid>")
		if err != nil || analysis.Valid || len(analysis.Errors) != 1 ||
			!strings.HasPrefix(analysis.Errors[0], "Commit subject must be 'type(scope): description' (scope optional)") ||
			!strings.Contains(analysis.Errors[0], quoteSubject(subject)) {
			t.Fatalf("%q: %+v, %v", subject, analysis, err)
		}
	}
}

// TestAnalyzeCommit_Boundary_SubjectAndFooterTogether (#61): a breaking commit whose subject breaks
// the policy reports both failures; a CRLF subject is read without its carriage return; an
// overlong subject is quoted cut.
func TestAnalyzeCommit_Boundary_SubjectAndFooterTogether(t *testing.T) {
	analysis, err := AnalyzeCommit("change!: remove\n\nBREAKING CHANGE: gone")
	if err != nil || len(analysis.Errors) != 2 || !strings.Contains(analysis.Errors[1], "HISS-14 violation") {
		t.Fatalf("bad subject and missing footer: %+v, %v", analysis, err)
	}
	if analysis, err := AnalyzeCommit("fix: carriage return\r\n\r\nbody\r\n"); err != nil || !analysis.Valid {
		t.Fatalf("CRLF message: %+v, %v", analysis, err)
	}
	long := strings.Repeat("x", maxQuotedSubjectBytes+10)
	analysis, err = AnalyzeCommit(long)
	if err != nil || len(analysis.Errors) != 1 || !strings.HasSuffix(analysis.Errors[0], strings.Repeat("x", maxQuotedSubjectBytes)+`..."`) {
		t.Fatalf("overlong subject: %+v, %v", analysis, err)
	}
}

// TestCleanCommitMessage_Boundary (#61): comment lines go, git's own included, and so does the
// scissors line with everything after it; a '#' inside a line and an empty message stay.
func TestCleanCommitMessage_Boundary(t *testing.T) {
	raw := "# Use type(scope): description\nfix: subject #12\n\nbody\n# Please enter the commit message\n" +
		commitScissors + "\ndiff --git a/x b/x\n+BREAKING CHANGE: not part of the message\n"
	cleaned, err := CleanCommitMessage(raw)
	if want := "fix: subject #12\n\nbody"; err != nil || cleaned != want {
		t.Fatalf("got %q, %v, want %q", cleaned, err, want)
	}
	if got, err := CleanCommitMessage(""); err != nil || got != "" {
		t.Fatalf("empty message: %q, %v", got, err)
	}
	analysis, err := AnalyzeCommit(cleaned)
	if err != nil || !analysis.Valid || analysis.IsBreaking {
		t.Fatalf("cleaned message: %+v, %v", analysis, err)
	}
	comments, err := CleanCommitMessage("# only a comment\n")
	if err != nil {
		t.Fatalf("comments only: %v", err)
	}
	if _, err := AnalyzeCommit(comments); err == nil {
		t.Fatal("a message of comments only must be refused as empty")
	}
}

// TestCleanCommitMessage_Boundary_LineBound (#61): a message of exactly maxCommitMessageLines
// lines is cleaned whole, its final newline included, and so is one whose lines past the bound
// follow the scissors line, which cuts them anyway. One line more is refused with an error naming
// the bound, rather than cleaned without the lines past it: there they held a BREAKING CHANGE:
// footer the policy then never read.
func TestCleanCommitMessage_Boundary_LineBound(t *testing.T) {
	subject := "fix: at the bound"
	exact := subject + strings.Repeat("\nb", maxCommitMessageLines-1)
	for name, message := range map[string]string{
		"exactly the bound":                exact,
		"exactly the bound, final newline": exact + "\n",
		"cut lines past the bound":         exact + "\n" + commitScissors + strings.Repeat("\nd", 10),
	} {
		cleaned, err := CleanCommitMessage(message)
		if err != nil || strings.Count(cleaned, "\n") != maxCommitMessageLines-1 {
			t.Fatalf("%s: %d lines, %v", name, strings.Count(cleaned, "\n")+1, err)
		}
	}
	over := exact + "\nBREAKING CHANGE: the footer past the bound"
	cleaned, err := CleanCommitMessage(over)
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(maxCommitMessageLines)) || cleaned != "" {
		t.Fatalf("one line past the bound: %d bytes cleaned, %v", len(cleaned), err)
	}
}
