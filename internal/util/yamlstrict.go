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

// YAMLDocumentOptions selects how DecodeYAMLDocument treats keys and empty input. The
// single-document rule itself is not optional.
type YAMLDocumentOptions struct {
	// KnownFields refuses keys the target does not declare. Set it where the target is the
	// file's complete schema; a reader of one section of a file whose schema another decoder
	// owns leaves it unset, so unrelated sections do not fail it.
	KnownFields bool
	// AllowEmpty accepts input that holds no document and leaves the target unchanged, as
	// yaml.Unmarshal does for an empty file.
	AllowEmpty bool
}

// DecodeYAMLStrict decodes exactly one YAML document from data into out and refuses keys
// out does not declare. A misspelled key that is silently dropped reads as configured
// while the built-in default governs instead; a second document that is silently ignored
// hides half of what the operator wrote. Both are errors here, so a configuration file
// either decodes completely or is refused.
func DecodeYAMLStrict(data []byte, out any) error {
	return DecodeYAMLDocument(data, out, YAMLDocumentOptions{KnownFields: true})
}

// DecodeYAMLDocument decodes the one YAML document in data into out and refuses a second
// one. Every reader of a file shares this rule, so no reader acts on a first document that
// another reader of the same file would refuse for carrying a second.
func DecodeYAMLDocument(data []byte, out any, opts YAMLDocumentOptions) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(opts.KnownFields)
	if err := decoder.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			if opts.AllowEmpty {
				return nil
			}
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
