package forge

import (
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
func CleanCommitMessage(message string) string {
	lines := strings.Split(message, "\n")
	kept := make([]string, 0, len(lines))
	for i := 0; i < len(lines) && i < maxCommitMessageLines; i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		if line == commitScissors {
			break
		}
		if !strings.HasPrefix(line, "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
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

// maxQuotedSubjectBytes bounds the subject a failure quotes.
const maxQuotedSubjectBytes = 120

// quoteSubject quotes subject for a failure, cut at maxQuotedSubjectBytes.
func quoteSubject(subject string) string {
	if len(subject) > maxQuotedSubjectBytes {
		subject = subject[:maxQuotedSubjectBytes] + "..."
	}
	return strconv.Quote(subject)
}
