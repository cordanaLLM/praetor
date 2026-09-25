package util

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// ErrYAMLNotSingleDocument reports YAML input that carries no document or more than one.
var ErrYAMLNotSingleDocument = errors.New("util: YAML input must contain exactly one document")

// DecodeYAMLStrict decodes exactly one YAML document from data into out and refuses keys
// out does not declare. A misspelled key that is silently dropped reads as configured
// while the built-in default governs instead; a second document that is silently ignored
// hides half of what the operator wrote. Both are errors here, so a configuration file
// either decodes completely or is refused.
func DecodeYAMLStrict(data []byte, out any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrYAMLNotSingleDocument
		}
		return fmt.Errorf("util: decode YAML: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.Join(ErrYAMLNotSingleDocument, err)
	}
	return nil
}
