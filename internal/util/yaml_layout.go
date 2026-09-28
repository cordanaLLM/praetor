package util

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	// YAMLLineLimit is the line-length maximum of yamllint's default configuration.
	YAMLLineLimit = 80
	// maxFitYAMLLines bounds the lines FitYAMLLines rewrites (HISS-02); a longer text is
	// returned unchanged.
	maxFitYAMLLines = 1 << 16
	// maxYAMLDocuments bounds the documents YAMLEquivalent compares (HISS-02).
	maxYAMLDocuments = 64
)

// yamlUnbrokenValue matches a block mapping entry, "<indent>[- ]key: value", whose value is
// one plain scalar holding no white space and opening with no YAML indicator.
var yamlUnbrokenValue = regexp.MustCompile("^( *)((?:- )*)([A-Za-z0-9_][A-Za-z0-9_.-]*): " +
	"([^\\s\"'&*!|>%@`#\\[\\]{},?:-]\\S*)$")

// FitYAMLLines moves the value of every line longer than YAMLLineLimit that holds a mapping
// key and one plain scalar without white space, such as a digest or a path, alone onto the
// next line, indent spaces deeper than its key. That line is one unbroken word, which
// yamllint's default line-length rule allows (allow-non-breakable-words), and YAML reads the
// same value from it. Every other line is kept byte for byte. The result must decode to what
// data decodes to, or FitYAMLLines fails instead of returning a changed document.
func FitYAMLLines(data []byte, indent int) ([]byte, error) {
	if indent < 1 {
		return nil, errors.New("util: YAML indent must be positive")
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) > maxFitYAMLLines {
		return data, nil
	}
	var out strings.Builder
	changed := false
	for _, line := range lines {
		fitted, moved := fitYAMLLine(line, indent)
		changed = changed || moved
		out.WriteString(fitted)
	}
	if !changed {
		return data, nil
	}
	fitted := []byte(out.String())
	if err := YAMLEquivalent(data, fitted); err != nil {
		return nil, fmt.Errorf("util: fit YAML lines: %w", err)
	}
	return fitted, nil
}

// fitYAMLLine returns line with its value moved to the next line, and whether it moved it.
func fitYAMLLine(line string, indent int) (string, bool) {
	body, newline := strings.CutSuffix(line, "\n")
	if utf8.RuneCountInString(body) <= YAMLLineLimit {
		return line, false
	}
	match := yamlUnbrokenValue.FindStringSubmatch(body)
	if match == nil {
		return line, false
	}
	continuation := strings.Repeat(" ", len(match[1])+len(match[2])+indent)
	fitted := match[1] + match[2] + match[3] + ":\n" + continuation + match[4]
	if newline {
		fitted += "\n"
	}
	return fitted, true
}

// YAMLEquivalent reports an error unless left and right hold the same number of documents and
// each pair decodes to the same value, so the two texts differ at most in layout: comments,
// line breaks, quoting, indentation, document markers. A text that does not decode is an error.
func YAMLEquivalent(left, right []byte) error {
	leftDecoder := yaml.NewDecoder(bytes.NewReader(left))
	rightDecoder := yaml.NewDecoder(bytes.NewReader(right))
	// One pass per document and one more to reach the end of both texts.
	for range maxYAMLDocuments + 1 {
		var leftValue, rightValue any
		leftErr, rightErr := leftDecoder.Decode(&leftValue), rightDecoder.Decode(&rightValue)
		leftDone, rightDone := errors.Is(leftErr, io.EOF), errors.Is(rightErr, io.EOF)
		if leftDone && rightDone {
			return nil
		}
		if leftDone != rightDone {
			return errors.New("the texts hold different numbers of YAML documents")
		}
		if err := errors.Join(leftErr, rightErr); err != nil {
			return err
		}
		if !reflect.DeepEqual(leftValue, rightValue) {
			return errors.New("the documents decode to different values")
		}
	}
	return fmt.Errorf("more than %d YAML documents", maxYAMLDocuments)
}
