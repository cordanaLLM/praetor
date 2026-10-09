package agenthook

import (
	"regexp"
	"strings"
)

// Rule 4 of the agent harness (ask in a popup, never in prose) enforced at the stop event.
// The check reads the final assistant message the client hands the stop hook, so it needs no
// I/O and runs before any other stop check. The deny is block-once: a repeated stop
// (stop_hook_active, agy's executionNum) is a stated skip, so a false positive costs one
// extra turn and can never loop.
//
// Final-message fields and structured question tools, verified against pinned upstream
// sources:
//   - claude: Stop.last_assistant_message and stop_hook_active (code.claude.com/docs/en/hooks,
//     "Stop"); tool AskUserQuestion.
//   - codex: Stop.last_assistant_message (nullable) and stop_hook_active (openai/codex
//     rust-v0.145.0, codex-rs/hooks/src/events/stop.rs, StopRequest); exit 2 with stderr
//     continues the turn with the stderr text (same file, parse_completed); tool
//     request_user_input (codex-rs/core/src/tools/handlers/request_user_input_spec.rs,
//     REQUEST_USER_INPUT_TOOL_NAME). Codex offers the tool only in some collaboration modes
//     (request_user_input_unavailable_message), so the reason says "or the client's own
//     equivalent" and tells the agent to restate the choice without a question when no
//     structured tool exists.
//   - gemini: AfterAgent.prompt_response and stop_hook_active (google-gemini/gemini-cli
//     v0.61.0, packages/core/src/hooks/types.ts, AfterAgentInput); exit 2 rejects the
//     response and retries with stderr as the prompt (docs/hooks/reference.md, "AfterAgent");
//     tool ask_user (packages/core/src/tools/definitions/base-declarations.ts,
//     ASK_USER_TOOL_NAME).
//   - agy: the Stop payload carries executionNum, terminationReason and fullyIdle only
//     (dialect_agy.go), no message text, so every agy stop is a stated skip.

// questionTools names the structured question tool of each client with a verified final
// message. A client absent here has no message to judge.
var questionTools = map[string]string{
	"claude": "AskUserQuestion",
	"codex":  "request_user_input",
	"gemini": "ask_user",
}

var (
	fencedBlockLine = regexp.MustCompile("^ {0,3}(```|~~~)")
	inlineCodeSpan  = regexp.MustCompile("`+[^`]*`+")
	urlSpan         = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S*[^\s.,;:!?)\]'"*_>]`)
	itemQuestionEnd = regexp.MustCompile(`[?？]["')\]}*_>]*$`)
	questionEnd     = regexp.MustCompile(`[?？]["')\]}*_>]*(\s|$)`)
	indentedLine    = regexp.MustCompile(`^( {4}|\t)`)
	listItemLine    = regexp.MustCompile(`^\s*([-*+]|\d{1,3}[.)]|[A-Za-z][.)]|\(?[A-Za-z0-9]\))\s+\S`)
	// choiceRequest points at a choice: a bare "let me know" or "tell me" closes a summary as
	// often as it closes an options list, so the request must name which, whether or a pick.
	choiceRequest = regexp.MustCompile(`(?i)\b((let me know|tell me|reply with|say)\s+(which|whether|what you prefer)|(pick|choose)\s+(one|a|an|the|between|from)|prefer|which (one|option|of|do you)|your call|you decide|up to you)\b`)
	tableRowLine  = regexp.MustCompile(`^\s*\|`)
)

// evaluateStopQuestion judges the final message of a stop. Deny only for a closing prose
// question; every other outcome is a Skip with the reason stated, or Allow for a message
// that is judged and clean.
func evaluateStopQuestion(row Registration, canonical Canonical) Verdict {
	tool, known := questionTools[row.Client]
	switch {
	case !known:
		return Verdict{Outcome: Skip, Reason: row.Client + " stop payload carries no final message, prose questions not judged"}
	case canonical.StopActive:
		return Verdict{Outcome: Skip, Reason: "prose question not checked again after a stop-hook continuation (stop_hook_active)"}
	case strings.TrimSpace(canonical.Return) == "":
		return Verdict{Outcome: Skip, Reason: "stop payload carries no final message, prose questions not judged"}
	}
	if !closesWithQuestion(canonical.Return) {
		return Verdict{Outcome: Allow}
	}
	return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] The final message asks the operator in prose. " +
		"Ask every question that offers a choice through the structured question tool (" + tool +
		", or the client's own equivalent): re-ask the same choices there, then end the turn. " +
		"When the client offers no structured question tool in this mode, restate the choice as a " +
		"decision or a statement without a question."}
}

// closesWithQuestion reports whether the closing paragraph of text ends in a question, or
// closes an options list with a request to choose. Fenced, indented and inline code,
// blockquotes, table rows and URLs are removed first; only the last two paragraphs are
// judged, so a question answered further down a report passes. Known trade-offs: a report that
// is one paragraph and asks and answers in it ("Why did it fail? The cache was stale.") is
// denied once, because the check cannot tell a rhetorical question from a real one inside a
// paragraph; a rhetorical heading answered by a list in the last two paragraphs is denied the
// same way. The block-once skip (stop_hook_active) bounds either false positive to one turn.
func closesWithQuestion(text string) bool {
	paragraphs := closingParagraphs(stripQuoted(text))
	if listAsksQuestion(paragraphs) {
		return true
	}
	prose, found := closingProse(paragraphs)
	if !found {
		return false
	}
	if questionEnd.MatchString(prose) {
		return true
	}
	return choiceRequest.MatchString(prose) && countListItems(paragraphs) >= 2
}

// stripQuoted drops fenced code blocks (an unclosed fence runs to the end), blockquote
// lines, 4-space or tab indented code (an indented list item stays), table rows, inline code
// spans and URLs.
func stripQuoted(text string) string {
	kept := make([]string, 0, strings.Count(text, "\n")+1)
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case fencedBlockLine.MatchString(line):
			fenced = !fenced
		case fenced, strings.HasPrefix(strings.TrimLeft(line, " \t"), ">"), tableRowLine.MatchString(line),
			indentedLine.MatchString(line) && !listItemLine.MatchString(line):
		default:
			kept = append(kept, line)
		}
	}
	joined := strings.Join(kept, "\n")
	return urlSpan.ReplaceAllString(inlineCodeSpan.ReplaceAllString(joined, ""), "link")
}

// closingParagraphs returns the last two nonempty paragraphs, each as its trimmed lines.
func closingParagraphs(text string) [][]string {
	var paragraphs [][]string
	var current []string
	flush := func() {
		if len(current) > 0 {
			paragraphs = append(paragraphs, current)
			current = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, strings.TrimRight(line, " \t\r"))
	}
	flush()
	if len(paragraphs) > 2 {
		paragraphs = paragraphs[len(paragraphs)-2:]
	}
	return paragraphs
}

// closingProse is the prose of the last closing paragraph that has any non-list line, the
// lines joined by a space. Any sentence of it that ends in a question mark counts, so a
// trailing "Happy to." does not hide the question; a question in an earlier paragraph is
// rhetorical because a later paragraph follows it.
func closingProse(paragraphs [][]string) (string, bool) {
	for p := len(paragraphs) - 1; p >= 0; p-- {
		var prose []string
		for _, line := range paragraphs[p] {
			if !listItemLine.MatchString(line) {
				prose = append(prose, strings.TrimSpace(line))
			}
		}
		if len(prose) > 0 {
			return strings.Join(prose, " "), true
		}
	}
	return "", false
}

func countListItems(paragraphs [][]string) int {
	count := 0
	for _, paragraph := range paragraphs {
		for _, line := range paragraph {
			if listItemLine.MatchString(line) {
				count++
			}
		}
	}
	return count
}

// listAsksQuestion reports whether the last paragraph is a list with an item that ends in a
// question (the item itself ends in one): a numbered or bulleted list of questions is the usual way to ask in prose. A list
// in the earlier paragraph is not judged, because a later paragraph follows it.
func listAsksQuestion(paragraphs [][]string) bool {
	if len(paragraphs) == 0 {
		return false
	}
	for _, line := range paragraphs[len(paragraphs)-1] {
		if listItemLine.MatchString(line) && itemQuestionEnd.MatchString(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}
