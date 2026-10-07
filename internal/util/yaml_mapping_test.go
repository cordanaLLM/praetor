package util

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// scalarRoundTrip decodes `key: <scalar>` and returns the value the decoder reads back.
func scalarRoundTrip(t *testing.T, scalar string) string {
	t.Helper()
	var decoded map[string]string
	if err := yaml.Unmarshal([]byte("key: "+scalar+"\n"), &decoded); err != nil {
		t.Fatalf("decode %q: %v", scalar, err)
	}
	return decoded["key"]
}

// Positive: a plain-safe value stays plain, as the encoder writes it.
func TestYAMLScalar_Positive_PlainValueStaysPlain(t *testing.T) {
	for _, value := range []string{"go", "Loops & I/O - Bounded Loops & Mandatory I/O Timeouts"} {
		if got := YAMLScalar(value); got != value {
			t.Errorf("YAMLScalar(%q) = %q, want it plain", value, got)
		}
	}
}

// Negative: a value a plain scalar would misread -- a comment marker, a mapping indicator, a
// boolean, a leading indicator -- is quoted, and reads back as the same string.
func TestYAMLScalar_Negative_AmbiguousValueIsQuoted(t *testing.T) {
	for _, value := range []string{"a # b", "a: b", "true", "- item", "'quoted'", ""} {
		got := YAMLScalar(value)
		if got == value && value != "" {
			t.Errorf("YAMLScalar(%q) left it plain", value)
		}
		if back := scalarRoundTrip(t, got); back != value {
			t.Errorf("YAMLScalar(%q) = %q reads back as %q", value, got, back)
		}
	}
}

// Boundary: a value the encoder would fold or break over several lines -- a long title, an
// embedded newline -- is one double-quoted line that reads back as the same string.
func TestYAMLScalar_Boundary_MultiLineValueIsOneQuotedLine(t *testing.T) {
	for _, value := range []string{strings.Repeat("long title ", 12), "first\nsecond", "tab\there \"q\""} {
		got := YAMLScalar(value)
		if strings.Contains(got, "\n") {
			t.Errorf("YAMLScalar(%q) = %q spans lines", value, got)
		}
		if back := scalarRoundTrip(t, got); back != value {
			t.Errorf("YAMLScalar(%q) = %q reads back as %q", value, got, back)
		}
	}
}
