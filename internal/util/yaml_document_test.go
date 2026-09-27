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

// Positive: EncodeYAMLDocument opens with the document start and indents a sequence nested
// in a sequence item by two spaces, like every other level, and decodes back to the value.
func TestEncodeYAMLDocument_Positive_TwoSpaceLevels(t *testing.T) {
	type item struct {
		Import       string   `yaml:"import"`
		Capabilities []string `yaml:"capabilities"`
	}
	value := struct {
		Packages []item `yaml:"packages"`
	}{Packages: []item{{Import: "example.com/acme/kit/db", Capabilities: []string{"db.postgres", "db.sql"}}}}
	data, err := EncodeYAMLDocument(value)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\npackages:\n  - import: example.com/acme/kit/db\n    capabilities:\n      - db.postgres\n      - db.sql\n"
	if string(data) != want {
		t.Fatalf("EncodeYAMLDocument() =\n%s\nwant\n%s", data, want)
	}
	var back struct {
		Packages []item `yaml:"packages"`
	}
	if err := DecodeYAMLStrict(data, &back); err != nil || len(back.Packages) != 1 || len(back.Packages[0].Capabilities) != 2 {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
}

// failingYAMLMarshaler refuses to render itself.
type failingYAMLMarshaler struct{}

var errRefusedYAML = errors.New("refused")

func (failingYAMLMarshaler) MarshalYAML() (any, error) { return nil, errRefusedYAML }

// Negative: a value that refuses to render is an error wrapping the refusal, not a partial
// document.
func TestEncodeYAMLDocument_Negative_MarshalerError(t *testing.T) {
	data, err := EncodeYAMLDocument(map[string]any{"value": failingYAMLMarshaler{}})
	if !errors.Is(err, errRefusedYAML) || data != nil {
		t.Fatalf("EncodeYAMLDocument(refusing) = %q, %v; want the refusal", data, err)
	}
}

// Boundary: an empty mapping is still one document with the document start.
func TestEncodeYAMLDocument_Boundary_EmptyMapping(t *testing.T) {
	data, err := EncodeYAMLDocument(map[string]string{})
	if err != nil || string(data) != "---\n{}\n" {
		t.Fatalf("EncodeYAMLDocument(empty) = %q, %v", data, err)
	}
}
