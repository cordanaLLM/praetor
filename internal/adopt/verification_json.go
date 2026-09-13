package adopt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Decode only bounded objects, rejecting duplicate and case-folded owned keys.
// Unrelated package/global settings remain valid and are never rewritten.
func verificationObject(data []byte, owned ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("verification metadata must be a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for i := 0; decoder.More() && i < 256; i++ {
		if err := verificationJSONField(decoder, fields, owned); err != nil {
			return nil, err
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("verification metadata exceeds 256 fields or is invalid")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing verification metadata")
	}
	return fields, nil
}

func verificationJSONField(decoder *json.Decoder, fields map[string]json.RawMessage, owned []string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	key, ok := token.(string)
	if !ok || fields[key] != nil {
		return errors.New("duplicate or invalid verification metadata field")
	}
	for _, name := range owned {
		if strings.EqualFold(key, name) && key != name {
			return fmt.Errorf("verification field %s requires exact spelling", name)
		}
	}
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	fields[key] = value
	return nil
}

func verificationString(data json.RawMessage) (string, error) {
	var value string
	if len(data) == 0 {
		return "", nil
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return "", errors.New("verification metadata string cannot be null")
	}
	err := json.Unmarshal(data, &value)
	return value, err
}
