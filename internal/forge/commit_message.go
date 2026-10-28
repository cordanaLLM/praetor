package forge

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The commit message policy (#61). It is the one implementation: the repository's commit-msg hook
// applies it to the message being committed (praetorctl forge check-message), and CI applies it to
// every commit of a pull request (praetorctl forge check-commits), so a commit made with its hooks
// skipped still meets it before it merges.
var (
	// commitSubjectRegex is the Conventional Commits subject the policy requires:
	// type(scope)!: description, with the scope and the breaking marker optional and the type one
	// of commitSubjectTypes. The commit-msg hook enforced this list before the rule moved here.
	commitSubjectRegex = regexp.MustCompile(`^(?:build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)(?:\([^()\n]+\))?!?: \S.*$`)
	// commitSubjectTypes names the types commitSubjectRegex accepts, for the failure message.
	commitSubjectTypes = "build, chore, ci, docs, feat, fix, perf, refactor, revert, style, test"
	// gitGeneratedSubjects are the subject prefixes git itself writes for a merge and a revert,
	// which the policy accepts as they are.
	gitGeneratedSubjects = [...]string{"Merge ", `Revert "`}
)

// commitScissors is the line git's scissors and verbose cleanup modes cut a message at: it and
// everything below it, the diff git commit --verbose shows, never reach the commit.
const commitScissors = "# ------------------------ >8 ------------------------"

// maxCommitMessageLines bounds the line scan of one commit message (HISS-02).
const maxCommitMessageLines = 100000

// CleanCommitMessage returns message as git's default cleanup records it for the policy: without
// the lines that start with '#', git's comment character, and without the scissors line and
// everything after it. The commit-msg hook reads the message file before git cleans it, so
// check-message cleans it first; a message read from a commit is already clean.
//
// A message of more than maxCommitMessageLines lines before the scissors line is refused, not
// cleaned in part: the lines past the bound may hold the footer the policy reads, such as
// BREAKING CHANGE:. A final newline ends the last line and starts no other.
func CleanCommitMessage(message string) (string, error) {
	lines := strings.Split(strings.TrimSuffix(message, "\n"), "\n")
	kept := make([]string, 0, min(len(lines), maxCommitMessageLines))
	for i := 0; i < len(lines) && i <= maxCommitMessageLines; i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		if line == commitScissors {
			break
		}
		if i == maxCommitMessageLines {
			return "", fmt.Errorf("commit message holds more than %d lines before the scissors line; "+
				"the policy refuses it rather than check it in part", maxCommitMessageLines)
		}
		if !strings.HasPrefix(line, "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n"), nil
}

// commitSubjectError returns the policy failure for subject, the first line of a cleaned message,
// or "" when it is a Conventional Commits subject or one git generated for a merge or a revert.
func commitSubjectError(subject string) string {
	if commitSubjectRegex.MatchString(subject) {
		return ""
	}
	for _, prefix := range gitGeneratedSubjects {
		if strings.HasPrefix(subject, prefix) {
			return ""
		}
	}
	return "Commit subject must be 'type(scope): description' (scope optional), with type one of " +
		commitSubjectTypes + "; got " + quoteSubject(subject)
}

// IsRefactorCommit reports whether a valid Conventional Commit subject uses the refactor type.
// The subject policy remains the single parser for both the hook and range gates.
func IsRefactorCommit(message string) bool {
	trimmed := strings.TrimSpace(message)
	subject, _, _ := strings.Cut(trimmed, "\n")
	subject = strings.TrimSuffix(subject, "\r")
	if !commitSubjectRegex.MatchString(subject) {
		return false
	}
	typeToken, _, _ := strings.Cut(subject, ":")
	typeToken = strings.TrimSuffix(typeToken, "!")
	typeToken, _, _ = strings.Cut(typeToken, "(")
	return typeToken == "refactor"
}

// maxQuotedSubjectBytes bounds the subject a failure quotes.
const maxQuotedSubjectBytes = 120

// quoteSubject quotes subject for a failure, cut at maxQuotedSubjectBytes.
func quoteSubject(subject string) string {
	if len(subject) > maxQuotedSubjectBytes {
		subject = subject[:maxQuotedSubjectBytes] + "..."
	}
	return strconv.Quote(subject)
}

var (
	// bannedCoauthorsRegex matches names or emails matching the tool list with word boundaries,
	// case-insensitively, following fleet rule 1.
	bannedCoauthorsRegex = regexp.MustCompile(`(?i)\b(?:claude|anthropic|openai|chatgpt|gpt|codex|gemini|copilot|cursor|opencode|antigravity)\b`)

	// coauthorTrailerRegex matches a Co-authored-by trailer line (case-insensitive, optional colon).
	coauthorTrailerRegex = regexp.MustCompile(`(?i)^co-authored-by(?::|\s)\s*(.+)$`)

	// generatedFooterRegex matches a line opening with generated or created with/by; it is a
	// refused footer only when the same line names a tool (bannedCoauthorsRegex).
	generatedFooterRegex = regexp.MustCompile(`(?i)^(?:<!--\s*|[-*]\s*|[#>\s]*)*(?:generated|created)\s+(?:with|by)\b`)
)

// isBannedCoauthor reports whether val names a model, coding tool, or bot.
func isBannedCoauthor(val string) bool {
	valLower := strings.ToLower(val)
	if strings.Contains(valLower, "noreply@anthropic.com") {
		return true
	}
	if strings.Contains(valLower, "[bot]") {
		return true
	}
	return bannedCoauthorsRegex.MatchString(val)
}

// appendAttributionError adds a refusal error for line if not already present.
func appendAttributionError(errs []string, line string) []string {
	errMsg := fmt.Sprintf("no tool attribution (fleet rule): %s; fix: drop the line", line)
	for i := 0; i < len(errs) && i < maxCommitMessageLines; i++ {
		if errs[i] == errMsg {
			return errs
		}
	}
	return append(errs, errMsg)
}

// commitParagraphs splits a cleaned commit message into paragraphs of non-empty lines (HISS-02 bounded).
func commitParagraphs(message string) [][]string {
	lines := strings.Split(message, "\n")
	var paragraphs [][]string
	var current []string

	for i := 0; i < len(lines) && i < maxCommitMessageLines; i++ {
		line := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "" {
			if len(current) > 0 {
				paragraphs = append(paragraphs, current)
				current = nil
			}
		} else {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		paragraphs = append(paragraphs, current)
	}
	return paragraphs
}

// commitAttributionErrors inspects message for tool attribution violating fleet rule 1: a
// line opening with the robot emoji, a generated-with or created-with footer naming a tool,
// and a Co-authored-by trailer in the final paragraph naming a model, tool or bot (the
// trailer block as git interpret-trailers reads it).
func commitAttributionErrors(message string) []string {
	errs := attributionLineErrors(message)
	paragraphs := commitParagraphs(message)
	if len(paragraphs) < 2 {
		return errs
	}
	finalPara := paragraphs[len(paragraphs)-1]
	for i := 0; i < len(finalPara) && i < maxCommitMessageLines; i++ {
		line := strings.TrimRight(finalPara[i], "\r")
		m := coauthorTrailerRegex.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) > 1 && isBannedCoauthor(m[1]) {
			errs = appendAttributionError(errs, line)
		}
	}
	return errs
}

// attributionLineErrors refuses footer lines anywhere in message: a robot emoji opening a
// line, or a generated-with or created-with line that names a tool.
func attributionLineErrors(message string) []string {
	lines := strings.Split(message, "\n")
	var errs []string
	for i := 0; i < len(lines) && i < maxCommitMessageLines; i++ {
		line := strings.TrimRight(lines[i], "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		emoji := strings.HasPrefix(trimmed, "🤖")
		footer := generatedFooterRegex.MatchString(trimmed) && bannedCoauthorsRegex.MatchString(trimmed)
		if emoji || footer {
			errs = appendAttributionError(errs, line)
		}
	}
	return errs
}
