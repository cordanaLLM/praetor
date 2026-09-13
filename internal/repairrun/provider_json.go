package repairrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

type providerJSONFrame struct {
	keys    map[string]bool
	wantKey bool
}

func providerValidateJSON(data []byte) error {
	if len(data) > providerResponseLimit || !utf8.Valid(data) || !json.Valid(data) {
		return errors.New("repair provider returned invalid or oversized UTF-8 JSON")
	}
	if err := providerValidateEscapes(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	stack := []providerJSONFrame{}
	for count := 0; count < 65536; count++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New("repair provider JSON decoding failed")
		}
		stack, err = providerJSONToken(stack, token)
		if err != nil {
			return err
		}
	}
	return errors.New("repair provider JSON exceeds token bound")
}

func providerJSONToken(stack []providerJSONFrame, token json.Token) ([]providerJSONFrame, error) {
	var frame *providerJSONFrame
	if len(stack) > 0 {
		frame = &stack[len(stack)-1]
	}
	if delimiter, ok := token.(json.Delim); ok {
		return providerJSONDelimiter(stack, frame, delimiter)
	}
	if frame == nil || frame.keys == nil {
		return stack, nil
	}
	if frame.wantKey {
		key, ok := token.(string)
		if !ok || frame.keys[strings.ToLower(key)] {
			return nil, errors.New("repair provider JSON contains duplicate or case-aliased fields")
		}
		frame.keys[strings.ToLower(key)] = true
	}
	frame.wantKey = !frame.wantKey
	return stack, nil
}

func providerJSONDelimiter(stack []providerJSONFrame, frame *providerJSONFrame, delimiter json.Delim) ([]providerJSONFrame, error) {
	if delimiter == '}' || delimiter == ']' {
		return stack[:len(stack)-1], nil
	}
	if frame != nil && frame.keys != nil {
		frame.wantKey = true
	}
	if len(stack) >= 16 {
		return nil, errors.New("repair provider JSON exceeds nesting bound")
	}
	next := providerJSONFrame{}
	if delimiter == '{' {
		next.keys, next.wantKey = make(map[string]bool), true
	}
	return append(stack, next), nil
}

func providerValidateEscapes(data []byte) error {
	quoted := false
	for index := 0; index < len(data) && index < providerResponseLimit; index++ {
		if data[index] == '"' {
			quoted = !quoted
			continue
		}
		if data[index] != '\\' || !quoted {
			continue
		}
		end, err := providerEscapeEnd(data, index+1)
		if err != nil {
			return err
		}
		index = end
	}
	return nil
}

// JSON syntax validation precedes this scan, so each initial escape is complete.
func providerEscapeEnd(data []byte, index int) (int, error) {
	if data[index] != 'u' {
		return index, nil
	}
	value, err := strconv.ParseUint(string(data[index+1:index+5]), 16, 16)
	if err != nil {
		return 0, errors.New("repair provider Unicode escape is invalid")
	}
	index += 4
	if value < 0xd800 || value > 0xdfff {
		return index, nil
	}
	if value >= 0xdc00 || index+6 >= len(data) || string(data[index+1:index+3]) != `\u` {
		return 0, errors.New("repair provider Unicode surrogate is unpaired")
	}
	second, err := strconv.ParseUint(string(data[index+3:index+7]), 16, 16)
	if err != nil || second < 0xdc00 || second > 0xdfff {
		return 0, errors.New("repair provider Unicode surrogate is unpaired")
	}
	return index + 6, nil
}
