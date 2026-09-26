package notebook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Decode accepts one bounded strict UTF-8 JSON value, rejecting ambiguous keys.
func Decode(raw []byte, value any) error {
	if len(raw) == 0 || len(raw) > 1<<20 || !utf8.Valid(raw) {
		return fmt.Errorf("JSON requires 1..1048576 UTF-8 bytes")
	}
	if err := uniqueKeys(raw); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return fmt.Errorf("decode artifact: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected exactly one JSON document")
	}
	return nil
}

type jsonFrame struct {
	keys    map[string]bool
	keyNext bool
}

func uniqueKeys(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	stack := []jsonFrame{}
	for n := 0; n <= len(raw); n++ {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			stack, err = consumeDelimiter(stack, delim)
			if err != nil {
				return err
			}
			continue
		}
		if err := consumeToken(stack, token); err != nil {
			return err
		}
	}
	return fmt.Errorf("JSON token bound exceeded")
}

func consumeDelimiter(stack []jsonFrame, delim json.Delim) ([]jsonFrame, error) {
	if delim == '}' || delim == ']' {
		return stack[:len(stack)-1], nil
	}
	consumeValue(stack)
	frame := jsonFrame{}
	if delim == '{' {
		frame.keys = make(map[string]bool)
		frame.keyNext = true
	}
	stack = append(stack, frame)
	if len(stack) > 32 {
		return nil, fmt.Errorf("JSON nesting exceeds 32")
	}
	return stack, nil
}

func consumeValue(stack []jsonFrame) {
	if len(stack) > 0 {
		stack[len(stack)-1].keyNext = stack[len(stack)-1].keys != nil
	}
}

func consumeToken(stack []jsonFrame, token any) error {
	if len(stack) == 0 {
		return nil
	}
	f := &stack[len(stack)-1]
	if f.keys != nil && f.keyNext {
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("invalid or duplicate JSON key")
		}
		folded := foldKey(key)
		if f.keys[folded] {
			return fmt.Errorf("invalid or duplicate JSON key")
		}
		f.keys[folded] = true
		f.keyNext = false
		return nil
	}
	consumeValue(stack)
	return nil
}

// maxFoldOrbit bounds the walk around a rune's case-folding orbit (HISS-02). Unicode
// orbits are at most four runes long; the bound exists so a future table cannot make the
// loop unbounded.
const maxFoldOrbit = 8

// foldKey returns the spelling encoding/json compares field names by: ASCII letters
// upper-cased, every other rune folded to the smallest rune in its case-folding orbit.
//
// Rejecting only byte-exact duplicates was not strictness. The decoder that runs next
// matches fields case-insensitively, so {"quality": 1, "Quality": 2} passed the
// uniqueness check and then decoded with the second spelling silently overwriting the
// first. Folding here makes a case-variant duplicate the same refusal an exact duplicate
// already is. The fold is reimplemented because encoding/json keeps its own unexported.
func foldKey(key string) string {
	return strings.Map(foldRune, key)
}

func foldRune(r rune) rune {
	if r < utf8.RuneSelf {
		if 'a' <= r && r <= 'z' {
			return r - ('a' - 'A')
		}
		return r
	}
	for i := 0; i < maxFoldOrbit; i++ {
		folded := unicode.SimpleFold(r)
		if folded <= r {
			return folded
		}
		r = folded
	}
	return r
}
