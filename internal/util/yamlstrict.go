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

// yamlDocumentStart opens every document EncodeYAMLDocument renders.
const yamlDocumentStart = "---\n"

// yamlIndent is the indentation of every level EncodeYAMLDocument renders.
const yamlIndent = 2

// EncodeYAMLDocument renders value as one YAML document that yamllint's default and relaxed
// rule sets accept: it opens with the "---" document start, every mapping and sequence level
// is indented by two spaces, and a line past YAMLLineLimit holding one long unbroken value is
// fitted (FitYAMLLines). yaml.Marshal indents by four, which puts a sequence nested in a
// sequence item two spaces off the width of the rest of the document, so a file it writes
// fails the indentation rule of the repository it is committed to.
func EncodeYAMLDocument(value any) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteString(yamlDocumentStart)
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(yamlIndent)
	if err := errors.Join(encoder.Encode(value), encoder.Close()); err != nil {
		return nil, fmt.Errorf("util: encode YAML: %w", err)
	}
	return FitYAMLLines(buffer.Bytes(), yamlIndent)
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
