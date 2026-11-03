package forge

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// TestAnalyzeCommit_Boundary_BodyWidth accepts 72 characters, including multibyte runes.
func TestAnalyzeCommit_Boundary_BodyWidth(t *testing.T) {
	for name, body := range map[string]string{
		"ASCII":     "A " + strings.Repeat("x", 70),
		"multibyte": "界 " + strings.Repeat("界", 70),
		"CRLF":      "A " + strings.Repeat("x", 70) + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			analysis, err := AnalyzeCommit("fix: wrap body\n\n" + body)
			if err != nil || !analysis.Valid {
				t.Fatalf("72-rune body: %+v, %v", analysis, err)
			}
		})
	}
}

// TestAnalyzeCommit_Negative_BodyWidth plants overlong prose and checks its line and rune width.
func TestAnalyzeCommit_Negative_BodyWidth(t *testing.T) {
	for name, body := range map[string]string{
		"ASCII":              "A " + strings.Repeat("x", 71),
		"multibyte":          "界 " + strings.Repeat("界", 71),
		"after closed fence": "```sh\nignored\n```\nA " + strings.Repeat("x", 71),
	} {
		t.Run(name, func(t *testing.T) {
			analysis, err := AnalyzeCommit("fix: wrap body\n\n" + body)
			line := strings.Count(body, "\n") + 3
			want := fmt.Sprintf("body line %d is 73 columns", line)
			if err != nil || analysis.Valid || len(analysis.Errors) != 1 || !strings.Contains(analysis.Errors[0], want) {
				t.Fatalf("want %q, got %+v, %v", want, analysis, err)
			}
		})
	}
}

// TestAnalyzeCommit_Positive_BodyWidthExceptions keeps unwrappable and structured lines intact.
func TestAnalyzeCommit_Positive_BodyWidthExceptions(t *testing.T) {
	long := strings.Repeat("long ", 20)
	for name, message := range map[string]string{
		"subject":           "fix: " + long,
		"signoff":           "fix: wrap body\n\nSigned-off-by: " + long + "<person@example.invalid>",
		"coauthor":          "fix: wrap body\n\nCo-authored-by: " + long + "<person@example.invalid>",
		"other trailer":     "fix: wrap body\n\nReviewed-by: " + long,
		"lowercase trailer": "fix: wrap body\n\nreviewed-by: " + long,
		"backtick fence":    "fix: wrap body\n\n```sh\n" + long + "\n```",
		"tilde fence":       "fix: wrap body\n\n~~~sh\n" + long + "\n~~~",
		"longer fence":      "fix: wrap body\n\n````sh\n```\n" + long + "\n````",
		"URL":               "fix: wrap body\n\nhttps://example.invalid/" + strings.Repeat("x", 80),
		"path":              "fix: wrap body\n\n./" + strings.Repeat("path/", 20),
		"command":           "fix: wrap body\n\n./command-" + strings.Repeat("x", 80),
		"quote":             "fix: wrap body\n\n> " + long,
	} {
		t.Run(name, func(t *testing.T) {
			analysis, err := AnalyzeCommit(message)
			if err != nil || !analysis.Valid {
				t.Fatalf("width exception: %+v, %v", analysis, err)
			}
		})
	}
}

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

// TestAnalyzeCommit_Positive_AttributionAllowed verifies that human co-authors,
// words inside prose, and trailer-like lines in middle body paragraphs pass the policy.
func TestAnalyzeCommit_Positive_AttributionAllowed(t *testing.T) {
	cases := []struct {
		name    string
		message string
	}{
		{
			name: "human co-author passes",
			message: "feat(planning): implement commit check\n\n" +
				"Co-authored-by: Jane Doe <jane@example.org>\n" +
				"Signed-off-by: Author <author@example.com>",
		},
		{
			name: "folded human co-author passes",
			message: "feat(planning): implement commit check\n\n" +
				"Co-authored-by: Jane Doe\n" +
				" <jane@example.org>\n" +
				"Signed-off-by: Author <author@example.com>",
		},
		{
			name: "word inside prose passes",
			message: "feat(parser): add token scanner\n\n" +
				"This commit improves the gemini parser inside body prose.\n\n" +
				"Signed-off-by: Author <author@example.com>",
		},
		{
			name: "normal prose containing generated mid-sentence passes",
			message: "fix(adopt): regenerate the harness\n\n" +
				"The harness files are generated by the adoption step from the manifest.\n\n" +
				"Signed-off-by: Author <author@example.com>",
		},
		{
			name: "robot emoji inside a sentence passes",
			message: "docs(readme): describe the bot badge\n\n" +
				"The badge shows a 🤖 next to automated pull requests.\n\n" +
				"Signed-off-by: Author <author@example.com>",
		},
		{
			name: "trailer-like line in body middle passes as git does",
			message: "feat(planning): document migration\n\n" +
				"Co-authored-by: Claude <noreply@anthropic.com>\n\n" +
				"More body paragraph here.\n\n" +
				"Signed-off-by: Author <author@example.com>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			analysis, err := AnalyzeCommit(tc.message)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !analysis.Valid {
				t.Fatalf("expected valid commit, got errors: %v", analysis.Errors)
			}
		})
	}
}

// TestAnalyzeCommit_Negative_AttributionRefused verifies that tool attribution
// in trailers, footers, or robot emoji lines is refused naming line, rule, and fix.
var attributionRefusedCases = []struct {
	name          string
	message       string
	offendingLine string
}{
	{
		name: "banned coauthor no colon",
		message: "feat(planning): implement commit check\n\n" +
			"Co-authored-by Claude <noreply@anthropic.com>",
		offendingLine: "Co-authored-by Claude <noreply@anthropic.com>",
	},
	{
		name: "banned coauthor copilot",
		message: "feat(planning): implement commit check\n\n" +
			"Co-authored-by: GitHub Copilot",
		offendingLine: "Co-authored-by: GitHub Copilot",
	},
	{
		name: "banned coauthor lowercase",
		message: "feat(planning): implement commit check\n\n" +
			"co-authored-by: claude",
		offendingLine: "co-authored-by: claude",
	},
	{
		name: "generated with footer",
		message: "feat(planning): implement commit check\n\n" +
			"Generated with Claude Code",
		offendingLine: "Generated with Claude Code",
	},
	{
		name: "generated with unknown tool",
		message: "feat(planning): implement commit check\n\n" +
			"Generated with SomeTool",
		offendingLine: "Generated with SomeTool",
	},
	{
		name: "created by unknown tool",
		message: "feat(planning): implement commit check\n\n" +
			"Created by SomeTool",
		offendingLine: "Created by SomeTool",
	},
	{
		name: "robot emoji line",
		message: "feat(planning): implement commit check\n\n" +
			"🤖 Generated by AI",
		offendingLine: "🤖 Generated by AI",
	},
	{
		name: "banned coauthor bot bracket",
		message: "feat(planning): implement commit check\n\n" +
			"Co-authored-by: Bot-[bot] <bot@example.com>",
		offendingLine: "Co-authored-by: Bot-[bot] <bot@example.com>",
	},
	{
		name: "banned coauthor dependabot",
		message: "feat(planning): implement commit check\n\n" +
			"Co-authored-by: dependabot[bot] <dependabot[bot]@users.noreply.github.com>",
		offendingLine: "Co-authored-by: dependabot[bot] <dependabot[bot]@users.noreply.github.com>",
	},
	{
		name: "banned token anthropic",
		message: "feat(test): check\n\n" +
			"Co-authored-by: Anthropic <dev@anthropic.com>",
		offendingLine: "Co-authored-by: Anthropic <dev@anthropic.com>",
	},
	{
		name: "banned token openai",
		message: "feat(test): check\n\n" +
			"Co-authored-by: OpenAI <dev@openai.com>",
		offendingLine: "Co-authored-by: OpenAI <dev@openai.com>",
	},
	{
		name: "banned token chatgpt",
		message: "feat(test): check\n\n" +
			"Co-authored-by: ChatGPT <dev@openai.com>",
		offendingLine: "Co-authored-by: ChatGPT <dev@openai.com>",
	},
	{
		name: "banned token gpt",
		message: "feat(test): check\n\n" +
			"Co-authored-by: GPT-4 <dev@openai.com>",
		offendingLine: "Co-authored-by: GPT-4 <dev@openai.com>",
	},
	{
		name: "banned token codex",
		message: "feat(test): check\n\n" +
			"Co-authored-by: Codex <dev@openai.com>",
		offendingLine: "Co-authored-by: Codex <dev@openai.com>",
	},
	{
		name: "banned token gemini",
		message: "feat(test): check\n\n" +
			"Co-authored-by: Gemini <dev@google.com>",
		offendingLine: "Co-authored-by: Gemini <dev@google.com>",
	},
	{
		name: "banned token cursor",
		message: "feat(test): check\n\n" +
			"Co-authored-by: Cursor <dev@cursor.sh>",
		offendingLine: "Co-authored-by: Cursor <dev@cursor.sh>",
	},
	{
		name: "banned token opencode",
		message: "feat(test): check\n\n" +
			"Co-authored-by: OpenCode <dev@example.org>",
		offendingLine: "Co-authored-by: OpenCode <dev@example.org>",
	},
	{
		name: "banned token antigravity",
		message: "feat(test): check\n\n" +
			"Co-authored-by: Antigravity <dev@example.org>",
		offendingLine: "Co-authored-by: Antigravity <dev@example.org>",
	},
	{
		name: "folded coauthor continuation names claude",
		message: "feat(planning): implement commit check\n\n" +
			"Co-authored-by: Human\n" +
			" Claude <human@example.com>\n" +
			"Signed-off-by: Human <human@example.com>",
		offendingLine: "Co-authored-by: Human Claude <human@example.com>",
	},
	{
		name: "folded coauthor tab continuation names anthropic noreply",
		message: "feat(planning): implement commit check\n\n" +
			"Co-authored-by: Helper\n" +
			"\t<noreply@anthropic.com>",
		offendingLine: "Co-authored-by: Helper <noreply@anthropic.com>",
	},
	{
		name: "folded coauthor multi-line continuation",
		message: "feat(planning): implement commit check\n\n" +
			"Co-authored-by: Jane Doe\n" +
			" <jane@example.org>\n" +
			" (pair session with Codex)",
		offendingLine: "Co-authored-by: Jane Doe <jane@example.org> (pair session with Codex)",
	},
	{
		name: "comment-prefixed generated-with footer in stored commit",
		message: "feat(planning): implement commit check\n\n" +
			"# Generated with SomeTool",
		offendingLine: "# Generated with SomeTool",
	},
	{
		name: "markdown heading created-by footer in stored commit",
		message: "feat(planning): implement commit check\n\n" +
			"### Created by Claude Code",
		offendingLine: "### Created by Claude Code",
	},
}

func TestAnalyzeCommit_Negative_AttributionRefused(t *testing.T) {
	for _, tc := range attributionRefusedCases {
		t.Run(tc.name, func(t *testing.T) { assertAttributionRefused(t, tc.message, tc.offendingLine) })
	}
}

func TestAnalyzeCommit_Boundary_CommentedFooterCleanedInHookRefusedInStoredCommit(t *testing.T) {
	rawHookMessage := "feat(planning): implement commit check\n\n# Generated with SomeTool\n"
	cleaned, err := CleanCommitMessage(rawHookMessage)
	if err != nil {
		t.Fatalf("CleanCommitMessage: %v", err)
	}
	hookAnalysis, err := AnalyzeCommit(cleaned)
	if err != nil || !hookAnalysis.Valid {
		t.Fatalf("cleaned hook message should pass, got %+v, %v", hookAnalysis, err)
	}
	assertAttributionRefused(t, rawHookMessage, "# Generated with SomeTool")
}

func TestAnalyzeCommit_Boundary_AttributionDeduplicationOrderAndScale(t *testing.T) {
	dupMsg := "feat(planning): check deduplication\n\n" +
		"Generated with AlphaTool\n" +
		"Generated with BetaTool\n" +
		"Generated with AlphaTool\n\n" +
		"Co-authored-by: Claude <noreply@anthropic.com>\n" +
		"Co-authored-by: GitHub Copilot\n" +
		"Co-authored-by: Claude <noreply@anthropic.com>"
	analysis, err := AnalyzeCommit(dupMsg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{
		"no tool attribution (fleet rule): Generated with AlphaTool; fix: drop the line",
		"no tool attribution (fleet rule): Generated with BetaTool; fix: drop the line",
		"no tool attribution (fleet rule): Co-authored-by: Claude <noreply@anthropic.com>; fix: drop the line",
		"no tool attribution (fleet rule): Co-authored-by: GitHub Copilot; fix: drop the line",
	}
	if len(analysis.Errors) != len(wantOrder) {
		t.Fatalf("got %d errors %v, want %d", len(analysis.Errors), analysis.Errors, len(wantOrder))
	}
	for i := 0; i < len(wantOrder); i++ {
		if analysis.Errors[i] != wantOrder[i] {
			t.Fatalf("Errors[%d] = %q, want %q", i, analysis.Errors[i], wantOrder[i])
		}
	}

	const distinctLines = 60000
	var b strings.Builder
	b.WriteString("feat(planning): scale check\n\n")
	for i := 0; i < distinctLines; i++ {
		fmt.Fprintf(&b, "Generated with Tool-%d\n", i)
	}
	// Scale is checked by count only: a wall-clock bound depends on host load and fails under it.
	errs := commitAttributionErrors(b.String())
	if len(errs) != distinctLines {
		t.Fatalf("got %d attribution errors, want %d", len(errs), distinctLines)
	}
}

func assertAttributionRefused(t *testing.T, message, offendingLine string) {
	t.Helper()
	analysis, err := AnalyzeCommit(message)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if analysis.Valid {
		t.Fatalf("expected commit to be refused for attribution, but was valid")
	}
	for _, e := range analysis.Errors {
		if strings.Contains(e, "no tool attribution") && strings.Contains(e, "fleet rule") &&
			strings.Contains(e, "drop the line") && strings.Contains(e, offendingLine) {
			return
		}
	}
	t.Fatalf("did not find expected refusal message for %q in %v", offendingLine, analysis.Errors)
}

// TestCommitAttributionErrors_Boundary_LineBound verifies that a stored message over the line
// bound is refused outright, so a footer past the bound cannot pass unchecked, while a message of
// exactly the bound is analysed as usual.
func TestCommitAttributionErrors_Boundary_LineBound(t *testing.T) {
	filler := strings.Repeat("body line\n", maxCommitMessageLines-2)
	atBound := "feat(forge): bound\n" + filler + "plain last line"
	if errs := commitAttributionErrors(atBound); len(errs) != 0 {
		t.Fatalf("message of exactly %d lines: got %v, want no errors", maxCommitMessageLines, errs)
	}
	over := "feat(forge): bound\n" + filler + "plain line\nGenerated with AlphaTool"
	errs := commitAttributionErrors(over)
	if len(errs) != 1 || !strings.Contains(errs[0], "more than") {
		t.Fatalf("message of %d lines with a footer past the bound: got %v, want the over-bound refusal",
			maxCommitMessageLines+1, errs)
	}
}
