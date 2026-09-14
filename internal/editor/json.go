package editor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const maxEditorJSONDepth = 128

type editorJSONFrame struct {
	keys    map[string]bool
	keyNext bool
}

func decodeEditorJSON(raw []byte) (any, error) {
	if len(raw) == 0 || len(raw) > contextopt.MaxSourceBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("editor JSON requires 1..%d UTF-8 bytes", contextopt.MaxSourceBytes)
	}
	if err := validateEditorJSONTokens(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode editor JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("editor JSON requires exactly one document")
	}
	if err := validateJSONNodeBound(value); err != nil {
		return nil, err
	}
	return value, nil
}

func validateEditorJSONTokens(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	stack := make([]editorJSONFrame, 0, 16)
	for tokenCount := 0; tokenCount <= len(raw); tokenCount++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("decode editor JSON token: %w", err)
		}
		if delimiter, ok := token.(json.Delim); ok {
			var consumeErr error
			stack, consumeErr = consumeEditorJSONDelimiter(stack, delimiter)
			if consumeErr != nil {
				return consumeErr
			}
			continue
		}
		if err := consumeEditorJSONToken(stack, token); err != nil {
			return err
		}
	}
	return errors.New("editor JSON token count exceeds byte bound")
}

func consumeEditorJSONDelimiter(stack []editorJSONFrame, delimiter json.Delim) ([]editorJSONFrame, error) {
	if delimiter == '}' || delimiter == ']' {
		if len(stack) == 0 {
			return nil, errors.New("invalid editor JSON delimiter")
		}
		return stack[:len(stack)-1], nil
	}
	consumeEditorJSONValue(stack)
	frame := editorJSONFrame{}
	if delimiter == '{' {
		frame.keys, frame.keyNext = make(map[string]bool), true
	}
	stack = append(stack, frame)
	if len(stack) > maxEditorJSONDepth {
		return nil, fmt.Errorf("editor JSON nesting exceeds %d", maxEditorJSONDepth)
	}
	return stack, nil
}

func consumeEditorJSONToken(stack []editorJSONFrame, token any) error {
	if len(stack) == 0 {
		return nil
	}
	frame := &stack[len(stack)-1]
	if frame.keys != nil && frame.keyNext {
		key, ok := token.(string)
		if !ok || frame.keys[key] {
			return errors.New("editor JSON contains an invalid or duplicate key")
		}
		frame.keys[key], frame.keyNext = true, false
		return nil
	}
	consumeEditorJSONValue(stack)
	return nil
}

func consumeEditorJSONValue(stack []editorJSONFrame) {
	if len(stack) > 0 {
		stack[len(stack)-1].keyNext = stack[len(stack)-1].keys != nil
	}
}
