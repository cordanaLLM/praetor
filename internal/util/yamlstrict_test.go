package util

import (
	"errors"
	"testing"
)

type yamlStrictFixture struct {
	Name  string   `yaml:"name"`
	Items []string `yaml:"items"`
}

func TestDecodeYAMLStrict_DecodesDeclaredKeys(t *testing.T) {
	var got yamlStrictFixture
	if err := DecodeYAMLStrict([]byte("name: kit\nitems: [a, b]\n"), &got); err != nil {
		t.Fatalf("DecodeYAMLStrict() error = %v", err)
	}
	if got.Name != "kit" || len(got.Items) != 2 || got.Items[1] != "b" {
		t.Fatalf("DecodeYAMLStrict() = %+v, want name kit and items [a b]", got)
	}
}

func TestDecodeYAMLStrict_RefusesUnknownKeyAndSecondDocument(t *testing.T) {
	var got yamlStrictFixture
	if err := DecodeYAMLStrict([]byte("name: kit\nnmae: typo\n"), &got); err == nil {
		t.Fatal("DecodeYAMLStrict() accepted an undeclared key")
	}
	err := DecodeYAMLStrict([]byte("name: kit\n---\nname: other\n"), &got)
	if !errors.Is(err, ErrYAMLNotSingleDocument) {
		t.Fatalf("DecodeYAMLStrict() second document error = %v, want ErrYAMLNotSingleDocument", err)
	}
	if err := DecodeYAMLStrict([]byte("name: [unclosed\n"), &got); err == nil {
		t.Fatal("DecodeYAMLStrict() accepted malformed YAML")
	}
}

func TestDecodeYAMLStrict_EmptyInputIsNotADocument(t *testing.T) {
	for _, input := range []string{"", "\n", "# only a comment\n"} {
		var got yamlStrictFixture
		if err := DecodeYAMLStrict([]byte(input), &got); !errors.Is(err, ErrYAMLNotSingleDocument) {
			t.Errorf("DecodeYAMLStrict(%q) error = %v, want ErrYAMLNotSingleDocument", input, err)
		}
	}
	var got yamlStrictFixture
	if err := DecodeYAMLStrict([]byte("---\nname: kit\n"), &got); err != nil || got.Name != "kit" {
		t.Fatalf("DecodeYAMLStrict() with a leading document marker = %+v, %v", got, err)
	}
}
