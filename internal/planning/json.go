package planning

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

func decodeDraft(raw []byte) (Draft, error) {
	var draft Draft
	if len(raw) == 0 || len(raw) > MaxJSONBytes || !utf8.Valid(raw) {
		return draft, fmt.Errorf("planning JSON requires 1..%d UTF-8 bytes", MaxJSONBytes)
	}
	if err := uniqueKeys(raw); err != nil {
		return draft, err
	}
	if err := validateDraftJSONShape(raw); err != nil {
		return draft, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&draft); err != nil {
		return draft, fmt.Errorf("decode planning draft: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return draft, fmt.Errorf("expected exactly one planning JSON document")
	}
	normalizeEmptyDependencies(&draft)
	return draft, nil
}

func normalizeEmptyDependencies(draft *Draft) {
	for index := range draft.Milestones {
		if len(draft.Milestones[index].DependsOn) == 0 {
			draft.Milestones[index].DependsOn = []string{}
		}
	}
	for index := range draft.Steps {
		if len(draft.Steps[index].DependsOn) == 0 {
			draft.Steps[index].DependsOn = []string{}
		}
	}
}

type jsonFrame struct {
	keys    map[string]bool
	keyNext bool
}

func uniqueKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	stack := make([]jsonFrame, 0, 16)
	for tokenCount := 0; tokenCount <= len(raw); tokenCount++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("decode planning JSON token: %w", err)
		}
		if delimiter, ok := token.(json.Delim); ok {
			stack, err = consumeDelimiter(stack, delimiter)
			if err != nil {
				return err
			}
			continue
		}
		if err := consumeToken(stack, token); err != nil {
			return err
		}
	}
	return fmt.Errorf("planning JSON token bound exceeded")
}

func consumeDelimiter(stack []jsonFrame, delimiter json.Delim) ([]jsonFrame, error) {
	if delimiter == '}' || delimiter == ']' {
		if len(stack) == 0 {
			return nil, fmt.Errorf("invalid planning JSON delimiter")
		}
		return stack[:len(stack)-1], nil
	}
	consumeValue(stack)
	frame := jsonFrame{}
	if delimiter == '{' {
		frame.keys, frame.keyNext = make(map[string]bool), true
	}
	stack = append(stack, frame)
	if len(stack) > 32 {
		return nil, fmt.Errorf("planning JSON nesting exceeds 32")
	}
	return stack, nil
}

func consumeToken(stack []jsonFrame, token any) error {
	if len(stack) == 0 {
		return nil
	}
	frame := &stack[len(stack)-1]
	if frame.keys != nil && frame.keyNext {
		key, ok := token.(string)
		if !ok || frame.keys[key] {
			return fmt.Errorf("invalid or duplicate planning JSON key")
		}
		frame.keys[key], frame.keyNext = true, false
		return nil
	}
	consumeValue(stack)
	return nil
}

func consumeValue(stack []jsonFrame) {
	if len(stack) > 0 {
		stack[len(stack)-1].keyNext = stack[len(stack)-1].keys != nil
	}
}
