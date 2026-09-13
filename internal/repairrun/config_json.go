package repairrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Token validation rejects duplicate, aliased and null keys before typed decoding.
func decodeConfigJSON(data []byte, value any) error {
	return decodeExactJSON(data, value, false)
}

func decodeExactJSON(data []byte, value any, allowNull bool) error {
	if !utf8.Valid(data) || !json.Valid(data) {
		return errors.New("invalid UTF-8 JSON")
	}
	if err := checkJSONTokens(data, allowNull); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("invalid repair JSON schema")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var input, output any
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	if err := json.Unmarshal(canonical, &output); err != nil {
		return err
	}
	if !reflect.DeepEqual(input, output) {
		return errors.New("repair JSON requires exact field spelling and explicit fields")
	}
	return nil
}

func checkJSONTokens(data []byte, allowNull bool) error {
	if err := checkJSONStrings(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	frames := []map[string]bool{}
	keys := []bool{}
	for n := 0; n < 65536; n++ {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil || (token == nil && !allowNull) {
			return errors.New("invalid or null repair JSON value")
		}
		if delimiter, ok := token.(json.Delim); ok {
			frames, keys, err = configDelimiter(frames, keys, delimiter)
		} else if len(frames) > 0 {
			err = configScalar(frames, keys, token)
		}
		if err != nil {
			return err
		}
	}
	return errors.New("repair JSON token bound exceeded")
}

func configDelimiter(frames []map[string]bool, keys []bool, token json.Delim) ([]map[string]bool, []bool, error) {
	if token == '}' || token == ']' {
		return frames[:len(frames)-1], keys[:len(keys)-1], nil
	}
	if len(frames) >= 16 {
		return nil, nil, errors.New("repair JSON nesting bound exceeded")
	}
	if len(keys) > 0 {
		keys[len(keys)-1] = true
	}
	var frame map[string]bool
	if token == '{' {
		frame = map[string]bool{}
	}
	return append(frames, frame), append(keys, true), nil
}

func configScalar(frames []map[string]bool, keys []bool, token json.Token) error {
	n := len(frames) - 1
	if frames[n] == nil {
		return nil
	}
	if keys[n] {
		key, ok := token.(string)
		if !ok || frames[n][strings.ToLower(key)] {
			return errors.New("duplicate or aliased repair JSON field")
		}
		frames[n][strings.ToLower(key)] = true
	}
	keys[n] = !keys[n]
	return nil
}

func checkJSONStrings(data []byte) error {
	inside := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inside = !inside
			continue
		}
		if data[i] != '\\' || !inside {
			continue
		}
		end, err := configEscapeEnd(data, i+1)
		if err != nil {
			return err
		}
		i = end
	}
	return nil
}

func configEscapeEnd(data []byte, index int) (int, error) {
	if data[index] != 'u' {
		return index, nil
	}
	value, err := strconv.ParseUint(string(data[index+1:index+5]), 16, 16)
	if err != nil {
		return 0, errors.New("invalid Unicode escape")
	}
	end := index + 4
	if value < 0xd800 || value > 0xdfff {
		return end, nil
	}
	if value >= 0xdc00 || end+6 >= len(data) || string(data[end+1:end+3]) != `\u` {
		return 0, errors.New("unpaired Unicode surrogate")
	}
	second, err := strconv.ParseUint(string(data[end+3:end+7]), 16, 16)
	if err != nil || second < 0xdc00 || second > 0xdfff {
		return 0, errors.New("unpaired Unicode surrogate")
	}
	return end + 6, nil
}
