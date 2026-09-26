package util

import (
	"errors"
	"testing"
)

type yamlSectionFixture struct {
	Receipt struct {
		PublicKey string `yaml:"public_key"`
	} `yaml:"receipt"`
}

// Positive: a section reader decodes its section of a single document and tolerates the
// file's other keys.
func TestDecodeYAMLDocument_Positive_SectionToleratesUnrelatedKeys(t *testing.T) {
	var got yamlSectionFixture
	data := []byte("version: 1\nreceipt:\n  public_key: abc\nrepository:\n  owner: x\n")
	if err := DecodeYAMLDocument(data, &got, YAMLDocumentOptions{}); err != nil {
		t.Fatalf("DecodeYAMLDocument() error = %v", err)
	}
	if got.Receipt.PublicKey != "abc" {
		t.Fatalf("DecodeYAMLDocument() = %+v, want public_key abc", got)
	}
}

// Negative: a second document is refused whether or not unknown keys are, so no reader acts
// on a first document another reader refuses; empty input is refused unless allowed.
func TestDecodeYAMLDocument_Negative_SecondDocumentAndEmpty(t *testing.T) {
	for _, opts := range []YAMLDocumentOptions{{}, {AllowEmpty: true}, {KnownFields: true, AllowEmpty: true}} {
		var got yamlSectionFixture
		err := DecodeYAMLDocument([]byte("receipt:\n  public_key: first\n---\nreceipt:\n  public_key: second\n"), &got, opts)
		if !errors.Is(err, ErrYAMLNotSingleDocument) {
			t.Errorf("opts %+v: second document error = %v, want ErrYAMLNotSingleDocument", opts, err)
		}
	}
	var got yamlSectionFixture
	if err := DecodeYAMLDocument(nil, &got, YAMLDocumentOptions{}); !errors.Is(err, ErrYAMLNotSingleDocument) {
		t.Errorf("empty input without AllowEmpty = %v, want ErrYAMLNotSingleDocument", err)
	}
	if err := DecodeYAMLDocument([]byte("receipt: {}\nstray: 1\n"), &got, YAMLDocumentOptions{KnownFields: true}); err == nil {
		t.Error("KnownFields accepted an undeclared key")
	}
}

// Boundary: with AllowEmpty, an empty or comment-only file decodes to nothing and leaves the
// target as it was, as yaml.Unmarshal did for these readers.
func TestDecodeYAMLDocument_Boundary_EmptyLeavesTargetUnchanged(t *testing.T) {
	for _, data := range []string{"", "# only a comment\n"} {
		got := yamlSectionFixture{}
		got.Receipt.PublicKey = "kept"
		if err := DecodeYAMLDocument([]byte(data), &got, YAMLDocumentOptions{AllowEmpty: true}); err != nil {
			t.Errorf("%q: error = %v", data, err)
		}
		if got.Receipt.PublicKey != "kept" {
			t.Errorf("%q: target changed to %+v", data, got)
		}
	}
}
