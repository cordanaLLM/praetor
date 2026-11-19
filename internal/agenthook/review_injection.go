package agenthook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/util"
)

// reviewInjectionNotice attaches the complete routing label to the finding marker.
func reviewInjectionNotice(task string) string {
	return ReviewInjectionMarker + "; task: " + task
}

// reviewOutcomeVerdict classifies flagged reviews as findings while preserving ordinary verdicts.
func reviewOutcomeVerdict(text string, injection bool) string {
	if injection {
		return "fail"
	}
	return claudeVerdictWord(text)
}

// deliverReviewHandback prepends the finding to the dispatcher's message and preserves every other tool operand.
func deliverReviewHandback(canonical Canonical, task string, verdict *Verdict) (string, error) {
	input, err := decodeJSONObject(canonical.DispatchInput)
	if err != nil {
		return "", fmt.Errorf("read review handback input: %w", err)
	}
	notice := reviewInjectionNotice(task)
	message := notice + "\n\n" + canonical.Return
	encoded, err := json.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("encode review handback message: %w", err)
	}
	input["message"] = encoded
	updated, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode review handback input: %w", err)
	}
	verdict.Notice, verdict.AddedContext, verdict.UpdatedInput = notice, notice, updated
	return message, nil
}

// deliverReviewBrief adds review guidance and preserves the prompt used to verify dispatch receipts.
func deliverReviewBrief(client string, canonical Canonical, prompt string, verdict *Verdict) (string, error) {
	guidance, err := reviewBriefGuidance(canonical.Briefs)
	if err != nil {
		return "", err
	}
	verdict.Notice = appendLaneNotice(verdict.Notice, guidance)
	if client != "claude" || guidance == "" {
		return prompt, nil
	}
	return deliverReviewGuidance(canonical, guidance, verdict)
}

// deliverReviewGuidance composes reviewer instructions with any existing Claude prompt rewrite.
func deliverReviewGuidance(canonical Canonical, guidance string, verdict *Verdict) (string, error) {
	raw := canonical.DispatchInput
	if verdict.UpdatedInput != nil {
		raw = verdict.UpdatedInput
	}
	var input struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("read review dispatch prompt: %w", err)
	}
	prompt := input.Prompt + "\n\n" + guidance
	updated, err := claudeReadOnlyInput(raw, prompt)
	if err != nil {
		return "", fmt.Errorf("deliver review guidance: %w", err)
	}
	verdict.UpdatedInput = updated
	return prompt, nil
}

// validateReviewInjection reads one active, exact boolean declaration within the input and line bounds.
// errReviewInjectionMissing marks a return with no active declaration, which a caller that cannot
// tell a review return from another subagent's return (Codex) treats as not applicable.
var errReviewInjectionMissing = errors.New("prompt_injection is required")

func validateReviewInjection(text string) (bool, error) {
	if len(text) > MaxInputBytes {
		return false, errors.New("prompt_injection return exceeds byte bound")
	}
	lines := strings.Split(text, "\n")
	if len(lines) > util.MaxMarkedBlockLines {
		return false, errors.New("prompt_injection return exceeds line bound")
	}
	text = activeReviewText(lines)
	value := ""
	lines = strings.Split(text, "\n")
	for index := 0; index < len(lines) && index < util.MaxMarkedBlockLines; index++ {
		field, found := reviewInjectionField(lines[index])
		if !found {
			continue
		}
		if value != "" {
			return false, errors.New("prompt_injection must appear exactly once")
		}
		value = strings.TrimSpace(field)
		if value != "true" && value != "false" {
			return false, errors.New("prompt_injection must be true or false")
		}
	}
	if value == "" {
		return false, errReviewInjectionMissing
	}
	return value == "true", nil
}

// reviewInjectionField reads the exact declaration key and trims its value without accepting quoted keys.
func reviewInjectionField(line string) (string, bool) {
	key, value, found := strings.Cut(strings.TrimSpace(line), ":")
	return strings.TrimSpace(value), found && strings.EqualFold(strings.TrimSpace(key), "prompt_injection")
}

// reviewFenceIndent bounds Markdown fence indentation in physical columns.
const reviewFenceIndent = 3

// reviewTextScanner tracks disabled regions while delegating code fences to the shared Markdown tracker.
type reviewTextScanner struct {
	fence   util.MarkdownFence
	off     bool
	comment string
}

// active returns text outside blockquotes, comments, disabled regions and fences; quotes are literal.
func (s *reviewTextScanner) active(line string, lineStart bool) string {
	original := line
	line = strings.TrimSpace(line)
	if s.off {
		if lineStart {
			s.off = line != caveman.OnMarker
		}
		return ""
	}
	if s.comment == "" && s.inactive(original, lineStart) {
		return ""
	}
	line, s.comment = util.StripComments(line, util.CommentSyntax{Block: [][2]string{{"<!--", "-->"}}, Unquoted: true}, s.comment)
	return line
}

// inactive advances line-start markers while keeping fenced and disabled content inactive.
func (s *reviewTextScanner) inactive(line string, lineStart bool) bool {
	if lineStart {
		fenceLine := strings.TrimLeft(line, " ")
		if len(line)-len(fenceLine) <= reviewFenceIndent && s.fence.Inside(strings.TrimRight(fenceLine, " \t\r")) {
			return true
		}
	}
	if s.fence.Open() || !lineStart {
		return s.fence.Open()
	}
	line = strings.TrimSpace(line)
	s.off = line == caveman.OffMarker
	return s.off || strings.HasPrefix(line, ">")
}

// activeReviewText protects code-span comment openers before scanning inactive regions.
func activeReviewText(lines []string) string {
	text := maskReviewParagraphs(strings.Join(lines, "\n"), true)
	lines = strings.Split(text, "\n")
	var scanner reviewTextScanner
	for index := 0; index < len(lines) && index < util.MaxMarkedBlockLines; index++ {
		lines[index] = scanner.active(lines[index], true)
	}
	return maskReviewParagraphs(strings.Join(lines, "\n"), false)
}

// maskReviewParagraphs keeps code spans across soft line breaks, bounded by whitespace-only blank lines.
func maskReviewParagraphs(text string, commentOpenersOnly bool) string {
	paragraphs := blankLine.Split(text, util.MaxMarkedBlockLines)
	var scanner reviewTextScanner
	for index := 0; index < len(paragraphs) && index < util.MaxMarkedBlockLines; index++ {
		if commentOpenersOnly {
			paragraphs[index] = protectReviewCodeComments(paragraphs[index], &scanner)
		} else {
			paragraphs[index] = maskReviewCodeSpans(paragraphs[index])
		}
	}
	return strings.Join(paragraphs, "\n\n")
}

// protectReviewCodeComments ignores comment openers only when a code span opens outside a comment.
// Closers stay visible to StripComments: backticks inside an actual HTML comment are literal.
func protectReviewCodeComments(text string, scanner *reviewTextScanner) string {
	var out strings.Builder
	position := 0
	for steps := 0; position < len(text) && steps < MaxInputBytes; steps++ {
		spans := util.MarkdownCodeSpans(text[position:], 1)
		if len(spans) == 0 {
			out.WriteString(text[position:])
			scanReviewRegions(text, position, len(text), scanner)
			break
		}
		start, end := position+spans[0].Start, position+spans[0].End
		out.WriteString(text[position:start])
		scanReviewRegions(text, position, start, scanner)
		end = reviewRegionFragmentEnd(text, start, end, scanner)
		part := text[start:end]
		if scanner.comment == "" && !scanner.off && !scanner.fence.Open() {
			part = strings.ReplaceAll(part, "<!--", "    ")
		} else {
			scanReviewRegions(text, start, end, scanner)
		}
		out.WriteString(part)
		position = end
	}
	return out.String()
}

// reviewRegionFragmentEnd resumes span protection after an inactive line or a real comment closer.
func reviewRegionFragmentEnd(text string, start, end int, scanner *reviewTextScanner) int {
	if scanner.comment != "" {
		if closer := strings.Index(text[start:], scanner.comment); closer >= 0 {
			return start + closer + len(scanner.comment)
		}
		return len(text)
	}
	if scanner.off || scanner.fence.Open() {
		if newline := strings.IndexByte(text[start:], '\n'); newline >= 0 {
			return start + newline + 1
		}
		return len(text)
	}
	return end
}

// reviewFragmentLine restores permitted physical indentation split off before a code span.
func reviewFragmentLine(text string, start int, line string) (string, bool) {
	beginning := start
	for width := 0; beginning > 0 && width < reviewFenceIndent && text[beginning-1] == ' '; width++ {
		beginning--
	}
	if beginning == 0 || text[beginning-1] == '\n' {
		return text[beginning:start] + line, true
	}
	return line, false
}

// scanReviewRegions recognizes markers only at physical line starts after format-permitted indentation.
func scanReviewRegions(text string, start, end int, scanner *reviewTextScanner) {
	lines := strings.Split(text[start:end], "\n")
	for index := 0; index < len(lines) && index < util.MaxMarkedBlockLines; index++ {
		line, lineStart := reviewFragmentLine(text, start, lines[index])
		scanner.active(line, lineStart)
		start += len(lines[index]) + 1
	}
}

// maskReviewCodeSpans masks inline code within one paragraph without consuming active declarations.
func maskReviewCodeSpans(text string) string {
	spans := util.MarkdownCodeSpans(text, MaxInputBytes)
	var out strings.Builder
	last := 0
	for index := 0; index < len(spans) && index < MaxInputBytes; index++ {
		span := spans[index]
		out.WriteString(text[last:span.Start])
		out.WriteString(strings.Repeat(" ", span.End-span.Start))
		last = span.End
	}
	out.WriteString(text[last:])
	return out.String()
}
