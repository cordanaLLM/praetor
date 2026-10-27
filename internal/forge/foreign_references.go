// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package forge

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Order: engine-fr-rule-checks-20261027. Use the closing-reference grammar and decoder.
var textReferenceRegex = regexp.MustCompile(`(?i)^(` + referencePattern("issues|pulls?|discussions", "[#!]") + `)\b`)

const (
	maxReferenceTextBytes = 1 << 20
	maxReferenceOwners    = config.MaxOwnOrganizations + 1
)

// ForeignReferenceFindings refuses active references outside ownOrganizations. Bare numbers
// remain local; code spans and fenced blocks cannot post a cross-repository reference.
func ForeignReferenceFindings(text, where string, ownOrganizations []string) []string {
	if len(ownOrganizations) > maxReferenceOwners {
		return []string{where + ": own organizations exceeds 65 entries including repository owner"}
	}
	if len(text) > maxReferenceTextBytes {
		return []string{fmt.Sprintf("%s exceeds %d bytes", where, maxReferenceTextBytes)}
	}
	lines := strings.SplitN(text, "\n", maxCommitMessageLines+1)
	if len(lines) > maxCommitMessageLines {
		return []string{fmt.Sprintf("%s exceeds %d lines", where, maxCommitMessageLines)}
	}
	visible := referenceProse(lines)
	var findings []string
	line := 1
	for pos := 0; pos < len(visible) && pos < maxReferenceTextBytes; pos++ {
		if visible[pos] == '\n' {
			line++
			continue
		}
		if pos > 0 && referenceWordByte(visible[pos-1]) {
			continue
		}
		match := textReferenceRegex.FindStringSubmatchIndex(visible[pos:])
		if match == nil {
			continue
		}
		ref := referenceFromMatch(visible[pos:], match, 1)
		if foreignReference(ref, ownOrganizations) {
			findings = append(findings, fmt.Sprintf("%s line %d: foreign reference %q; put it in inline code or a code fence", where, line, ref.ref))
		}
		pos += match[1] - 1
	}
	return findings
}

func referenceWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("_./-", rune(b))
}

func foreignReference(ref closingReference, own []string) bool {
	if ref.owner == "" {
		return false
	}
	if strings.Contains(ref.ref, "://") && !strings.HasPrefix(strings.ToLower(ref.ref), "https://github.com/") {
		return false
	}
	for i := 0; i < len(own) && i < maxReferenceOwners; i++ {
		if strings.EqualFold(ref.owner, own[i]) {
			return false
		}
	}
	return true
}

// referenceProse preserves newlines while masking fences and inline code using the shared
// Markdown readers. Code spans may cross physical lines in a paragraph.
func referenceProse(lines []string) string {
	fence := util.MarkdownFence{}
	kept := make([]string, len(lines))
	for i := 0; i < len(lines) && i < maxCommitMessageLines; i++ {
		if !fence.Inside(strings.TrimSpace(lines[i])) {
			kept[i] = strings.TrimSuffix(lines[i], "\r")
		}
	}
	paragraphs := strings.Split(strings.Join(kept, "\n"), "\n\n")
	for i := 0; i < len(paragraphs) && i < maxCommitMessageLines; i++ {
		paragraphs[i] = maskReferenceCodeSpans(paragraphs[i])
	}
	return strings.Join(paragraphs, "\n\n")
}

func maskReferenceCodeSpans(text string) string {
	masked := []byte(text)
	spans := util.MarkdownCodeSpans(text, maxReferenceTextBytes)
	for i := 0; i < len(spans) && i < maxReferenceTextBytes; i++ {
		for j := spans[i].Start; j < spans[i].End && j < maxReferenceTextBytes; j++ {
			if masked[j] != '\n' {
				masked[j] = ' '
			}
		}
	}
	return string(masked)
}
