package operationalsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type jsonFrame struct {
	object, key bool
	keys        map[string]bool
}

func validateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	stack := make([]jsonFrame, 0, 32)
	for i := 0; i < 50000; i++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		stack, err = consumeJSONToken(stack, token)
		if err != nil {
			return err
		}
	}
	return errors.New("JSON exceeds 50000 tokens")
}

func consumeJSONToken(stack []jsonFrame, token json.Token) ([]jsonFrame, error) {
	if len(stack) > 0 {
		key, err := stack[len(stack)-1].consume(token)
		if err != nil || key {
			return stack, err
		}
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return stack, nil
	}
	switch delimiter {
	case '{', '[':
		if len(stack) >= 128 {
			return nil, errors.New("JSON nesting exceeds 128")
		}
		return append(stack, jsonFrame{object: delimiter == '{', key: true, keys: make(map[string]bool)}), nil
	case '}', ']':
		if len(stack) == 0 {
			return nil, errors.New("unexpected JSON closing delimiter")
		}
		return stack[:len(stack)-1], nil
	}
	return stack, nil
}

func (frame *jsonFrame) consume(token json.Token) (bool, error) {
	if !frame.object {
		return false, nil
	}
	if !frame.key {
		frame.key = true
		return false, nil
	}
	key, ok := token.(string)
	if !ok {
		return false, nil
	}
	if frame.keys[key] {
		return true, fmt.Errorf("duplicate JSON key %q", key)
	}
	frame.keys[key] = true
	frame.key = false
	return true, nil
}
