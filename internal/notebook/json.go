package notebook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
			if delim == '}' || delim == ']' {
				stack = stack[:len(stack)-1]
				continue
			}
			consumeValue(stack)
			frame := jsonFrame{}
			if delim == '{' {
				frame.keys = make(map[string]bool)
				frame.keyNext = true
			}
			stack = append(stack, frame)
			if len(stack) > 32 {
				return fmt.Errorf("JSON nesting exceeds 32")
			}
			continue
		}
		if err := consumeToken(stack, token); err != nil {
			return err
		}
	}
	return fmt.Errorf("JSON token bound exceeded")
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
		if !ok || f.keys[key] {
			return fmt.Errorf("invalid or duplicate JSON key")
		}
		f.keys[key] = true
		f.keyNext = false
		return nil
	}
	consumeValue(stack)
	return nil
}
