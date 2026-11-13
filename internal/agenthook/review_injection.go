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
	input, err := decodeObject(canonical.DispatchInput)
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
		return false, errors.New("prompt_injection is required")
	}
	return value == "true", nil
}

// reviewInjectionField reads the exact declaration key and trims its value without accepting quoted keys.
func reviewInjectionField(line string) (string, bool) {
	key, value, found := strings.Cut(strings.TrimSpace(line), ":")
	return strings.TrimSpace(value), found && strings.EqualFold(strings.TrimSpace(key), "prompt_injection")
}

// reviewTextScanner tracks disabled regions while delegating code fences to the shared Markdown tracker.
type reviewTextScanner struct {
	fence   util.MarkdownFence
	off     bool
	comment bool
}

// active reports whether a line can carry a declaration outside quotes, comments, disabled regions and fences.
func (s *reviewTextScanner) active(line string) bool {
	line = strings.TrimSpace(line)
	if s.off {
		s.off = line != caveman.OnMarker
		return false
	}
	if s.comment {
		s.comment = !strings.Contains(line, "-->")
		return false
	}
	if line == "" || strings.HasPrefix(line, ">") || s.fence.Inside(line) {
		return false
	}
	if line == caveman.OffMarker {
		s.off = true
		return false
	}
	if strings.HasPrefix(line, "<!--") {
		s.comment = !strings.Contains(line, "-->")
		return false
	}
	return true
}

// activeReviewText masks inactive lines and inline code using the shared Markdown span reader.
func activeReviewText(lines []string) string {
	var scanner reviewTextScanner
	for index := 0; index < len(lines) && index < util.MaxMarkedBlockLines; index++ {
		if !scanner.active(lines[index]) {
			lines[index] = ""
		}
	}
	paragraphs := strings.Split(strings.Join(lines, "\n"), "\n\n")
	for index := 0; index < len(paragraphs) && index < util.MaxMarkedBlockLines; index++ {
		paragraphs[index] = maskReviewCodeSpans(paragraphs[index])
	}
	return strings.Join(paragraphs, "\n\n")
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
