package planning

import (
	"encoding/hex"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validCount(count, maximum int) bool { return count > 0 && count <= maximum }

func validID(value string) bool {
	if len(value) == 0 || len(value) > MaxIDBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if !idRune(character) {
			return false
		}
	}
	return true
}

func idRune(character rune) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' || strings.ContainsRune("._:/-", character)
}

func validText(value string) bool {
	if len(strings.TrimSpace(value)) == 0 || len(value) > MaxTextBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}
