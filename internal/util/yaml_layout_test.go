package util

import (
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// digestValue is a lock or register digest, 71 columns on its own.
var digestValue = "sha256:" + strings.Repeat("4c", 32)

// withinYamllintLimit reports whether every line fits YAMLLineLimit or is one unbroken word
// after its indentation, the two forms yamllint's default line-length rule accepts.
func withinYamllintLimit(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if utf8.RuneCountInString(line) > YAMLLineLimit && strings.Contains(strings.TrimLeft(line, " "), " ") {
			return false
		}
	}
	return true
}

// Positive: a long digest under a nested key and one under a sequence item move to their own
// line, indented one level below the key, and the document decodes to the same value.
func TestFitYAMLLines_Positive_MovesUnbrokenValues(t *testing.T) {
	input := "---\nregister:\n    sources:\n        sha256: " + digestValue + "\n" +
		"        inputs:\n            - path: .paperclip/" + strings.Repeat("x", 70) + ".json\n              kind: message\n"
	fitted, err := FitYAMLLines([]byte(input), 4)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nregister:\n    sources:\n        sha256:\n            " + digestValue + "\n" +
		"        inputs:\n            - path:\n                  .paperclip/" + strings.Repeat("x", 70) + ".json\n" +
		"              kind: message\n"
	if string(fitted) != want {
		t.Fatalf("FitYAMLLines() =\n%s\nwant\n%s", fitted, want)
	}
	var before, after any
	if yaml.Unmarshal([]byte(input), &before) != nil || yaml.Unmarshal(fitted, &after) != nil || !deepEqualYAML(before, after) {
		t.Fatalf("the fitted document decodes differently:\n%v\n%v", before, after)
	}
	if !withinYamllintLimit(string(fitted)) {
		t.Errorf("a fitted line is still too long:\n%s", fitted)
	}
}

// Negative: a long value holding spaces, a quoted value and a line that is not a mapping entry
// cannot be fitted without changing the text, so they stay byte for byte; an indent below one
// is refused.
func TestFitYAMLLines_Negative_LeavesWhatItCannotFit(t *testing.T) {
	for _, input := range []string{
		"description: " + strings.Repeat("word ", 20) + "end\n",
		"digest: \"" + digestValue + "\"\n",
		"# " + strings.Repeat("comment ", 12) + "\n",
		"- " + digestValue + digestValue + "\n",
	} {
		fitted, err := FitYAMLLines([]byte(input), 2)
		if err != nil || string(fitted) != input {
			t.Errorf("%q: changed to %q (%v)", input, fitted, err)
		}
	}
	if _, err := FitYAMLLines([]byte("a: b\n"), 0); err == nil {
		t.Error("an indent of zero must be refused")
	}
}

// Boundary: a line of exactly YAMLLineLimit columns stays, one column more moves, and a last
// line without a line break keeps having none.
func TestFitYAMLLines_Boundary_LimitAndFinalLine(t *testing.T) {
	key := "k: "
	exact := key + strings.Repeat("v", YAMLLineLimit-len(key))
	if fitted, err := FitYAMLLines([]byte(exact+"\n"), 2); err != nil || string(fitted) != exact+"\n" {
		t.Errorf("a line of exactly %d columns must stay: %q %v", YAMLLineLimit, fitted, err)
	}
	over := exact + "v"
	fitted, err := FitYAMLLines([]byte(over), 2)
	if err != nil || string(fitted) != "k:\n  "+strings.Repeat("v", YAMLLineLimit-len(key)+1) {
		t.Errorf("one column over must move, without adding a line break: %q %v", fitted, err)
	}
}

// Positive: EncodeYAMLDocument fits a long digest the same way, at its two-space indentation.
func TestEncodeYAMLDocument_Positive_FitsLongDigest(t *testing.T) {
	value := map[string]map[string]string{"register": {"sha256": digestValue + digestValue[7:]}}
	data, err := EncodeYAMLDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	if want := "---\nregister:\n  sha256:\n    " + value["register"]["sha256"] + "\n"; string(data) != want {
		t.Fatalf("EncodeYAMLDocument() =\n%s\nwant\n%s", data, want)
	}
}

func deepEqualYAML(left, right any) bool {
	leftText, leftErr := yaml.Marshal(left)
	rightText, rightErr := yaml.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftText) == string(rightText)
}
