// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package harvester

import (
	"fmt"
	"strconv"
)

// encoding/json substitutes U+FFFD for unpaired escaped UTF-16 surrogates. Reject
// them before any decoding so observations and object keys cannot silently change.
// The enclosing scanner bounds raw to MaxTranscriptLineBytes. JSON syntax itself
// is validated separately; this linear scan only validates escapes in strings.
func rejectUnpairedTranscriptSurrogates(raw []byte) error {
	quoted := false
	for index := 0; index < len(raw); index++ {
		switch raw[index] {
		case '"':
			quoted = !quoted
		case '\\':
			if quoted {
				next, err := validateTranscriptEscape(raw, index)
				if err != nil {
					return err
				}
				index = next - 1
			}
		}
	}
	return nil
}

func validateTranscriptEscape(raw []byte, start int) (int, error) {
	if start+1 >= len(raw) {
		return 0, fmt.Errorf("incomplete JSON string escape")
	}
	if raw[start+1] != 'u' {
		return start + 2, nil
	}
	first, err := transcriptUnicodeUnit(raw, start)
	if err != nil {
		return 0, err
	}
	if first < 0xd800 || first > 0xdfff {
		return start + 6, nil
	}
	if first >= 0xdc00 {
		return 0, fmt.Errorf("unpaired JSON UTF-16 surrogate")
	}
	second, err := transcriptUnicodeUnit(raw, start+6)
	if err != nil || second < 0xdc00 || second > 0xdfff {
		return 0, fmt.Errorf("unpaired JSON UTF-16 surrogate")
	}
	return start + 12, nil
}

func transcriptUnicodeUnit(raw []byte, start int) (uint64, error) {
	if start+6 > len(raw) || raw[start] != '\\' || raw[start+1] != 'u' {
		return 0, fmt.Errorf("invalid JSON Unicode escape")
	}
	value, err := strconv.ParseUint(string(raw[start+2:start+6]), 16, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid JSON Unicode escape")
	}
	return value, nil
}
