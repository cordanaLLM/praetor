package caveman

import (
	"regexp"
	"strings"
)

var (
	runtimeEvidenceFieldRe = regexp.MustCompile(`(?i)^(?:[-*+]\s+)?evidence:\s*(.*)$`)
	runtimePointerMarkerRe = regexp.MustCompile(`(?i)(?:\bsha-?256\b|\blines\s*:)`)
	runtimeSetextRe        = regexp.MustCompile(`^(?:=+|-+)$`)
	runtimeHTMLEntityRe    = regexp.MustCompile(`&(?:#[0-9]+|#[xX][0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]+);`)
	runtimeHTMLTagRe       = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9-]*(?:\s+[^>\n]*)?\s*/?>`)
	runtimeMarkdownLinkRe  = regexp.MustCompile(`!?\[[^]\n]*\](?:\([^\n)]*\)|\[[^]\n]*\])`)
)

func checkRuntimeStructure(found *findings, ln line) {
	trimmed := strings.TrimSpace(ln.text)
	if runtimeHTMLEntityRe.MatchString(ln.text) || runtimeHTMLTagRe.MatchString(ln.text) || runtimeMarkdownLinkRe.MatchString(ln.text) {
		found.add(ln.num, RuleRuntimeEscape, "source-only rich text construct")
	}
	if runtimeSetextRe.MatchString(trimmed) {
		found.add(ln.num, RuleRuntimeEscape, "source-only heading underline")
		return
	}
	if ln.kind == kindOff {
		found.add(ln.num, RuleRuntimeEscape, "caveman off region")
		return
	}
	if ln.kind == kindCode {
		found.add(ln.num, RuleRuntimeEscape, "fenced code region")
		return
	}
	if ln.kind != kindStructured || protocolRe.MatchString(trimmed) || evidenceRe.MatchString(trimmed) {
		return
	}
	found.add(ln.num, RuleRuntimeEscape, "source-only structured text")
}

func checkRuntimeEvidence(found *findings, ln line) {
	trimmed := strings.TrimSpace(ln.text)
	match := runtimeEvidenceFieldRe.FindStringSubmatch(trimmed)
	if len(match) != 2 {
		return
	}
	value := match[1]
	if !runtimePointerMarkerRe.MatchString(value) {
		return
	}
	if !evidenceRe.MatchString(trimmed) {
		found.add(ln.num, RuleRuntimeEvidence, "malformed or trailing evidence pointer")
	}
}

func runtimeProseSegments(ln line) []string {
	if ln.kind == kindBlank || ln.kind == kindOff || ln.kind == kindCode {
		return nil
	}
	trimmed := strings.TrimSpace(ln.text)
	if protocolRe.MatchString(trimmed) || evidenceRe.MatchString(trimmed) {
		return []string{runtimeProseOf(ln.text)}
	}
	if strings.HasPrefix(trimmed, "|") {
		return runtimeTableSegments(ln.text)
	}
	return []string{runtimeProseOf(ln.text)}
}

func runtimeTableSegments(text string) []string {
	parts := strings.Split(strings.Trim(runtimeProseOf(text), "|"), "|")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if cell := strings.TrimSpace(part); cell != "" && !tableDelimiter(cell) {
			segments = append(segments, cell)
		}
	}
	return segments
}

func runtimeProseOf(text string) string {
	return inlineCodeRe.ReplaceAllStringFunc(text, func(span string) string {
		return strings.Trim(span, "`")
	})
}
