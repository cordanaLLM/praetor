package schemacheck

import (
	"errors"
	"strings"
	"testing"
)

const objectSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["name"],
  "additionalProperties": false,
  "properties": {
    "name": {"type": "string", "maxLength": 8},
    "count": {"type": "integer", "minimum": 0, "maximum": 10}
  }
}`

func mustCompile(t *testing.T, raw string) *Schema {
	t.Helper()
	schema, err := Compile("test.json", []byte(raw), nil)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return schema
}

func TestValidatePositive(t *testing.T) {
	schema := mustCompile(t, objectSchema)
	for _, doc := range []string{`{"name":"ok"}`, `{"name":"ok","count":0}`, `{"name":"12345678","count":10}`} {
		if err := schema.Validate([]byte(doc)); err != nil {
			t.Errorf("Validate(%s) = %v", doc, err)
		}
	}
}

// TestValidateNegative covers the mutated documents the harness contract requires to fail, each
// naming the field.
func TestValidateNegative(t *testing.T) {
	schema := mustCompile(t, objectSchema)
	cases := map[string]struct{ doc, field string }{
		"missing required":  {`{}`, "name"},
		"wrong type":        {`{"name":3}`, "/name"},
		"unknown member":    {`{"name":"a","extra":1}`, "extra"},
		"over the boundary": {`{"name":"123456789"}`, "/name"},
		"below the minimum": {`{"name":"a","count":-1}`, "/count"},
		"above the maximum": {`{"name":"a","count":11}`, "/count"},
		"fractional":        {`{"name":"a","count":1.5}`, "/count"},
	}
	for name, c := range cases {
		err := schema.Validate([]byte(c.doc))
		var violations *Violations
		if !errors.As(err, &violations) {
			t.Errorf("%s: Validate(%s) = %v, want *Violations", name, c.doc, err)
			continue
		}
		if !violations.Names(c.field) {
			t.Errorf("%s: violations do not name %q:\n%v", name, c.field, violations)
		}
	}
}

func TestValidateRefusesANonJSONDocument(t *testing.T) {
	schema := mustCompile(t, objectSchema)
	for _, doc := range []string{``, `{"name":`, `{"name":"a"} {"name":"b"}`} {
		err := schema.Validate([]byte(doc))
		var violations *Violations
		if err == nil || errors.As(err, &violations) {
			t.Errorf("Validate(%q) = %v, want a not-JSON error", doc, err)
		}
	}
}

func TestViolationsAreBounded(t *testing.T) {
	schema := mustCompile(t, `{"type":"array","items":{"type":"string"}}`)
	var doc strings.Builder
	doc.WriteString("[")
	for i := 0; i < MaxViolations+10; i++ {
		if i > 0 {
			doc.WriteString(",")
		}
		doc.WriteString("1")
	}
	doc.WriteString("]")
	err := schema.Validate([]byte(doc.String()))
	var violations *Violations
	if !errors.As(err, &violations) {
		t.Fatalf("Validate = %v", err)
	}
	if len(violations.Items) != MaxViolations || violations.Truncated != 10 {
		t.Fatalf("items %d truncated %d, want %d and 10", len(violations.Items), violations.Truncated, MaxViolations)
	}
	if !strings.Contains(violations.Error(), "and 10 more") {
		t.Errorf("Error() hides the truncation: %v", violations)
	}
}

// TestExternalReferenceNeedsAStatedReplacement is the no-silent-fallback case: a reference the
// caller did not replace fails the compilation instead of validating nothing.
func TestExternalReferenceNeedsAStatedReplacement(t *testing.T) {
	const raw = `{"$ref":"https://example.invalid/other.json#/$defs/Thing"}`
	if _, err := Compile("ext.json", []byte(raw), nil); err == nil || !strings.Contains(err.Error(), "example.invalid/other.json") {
		t.Fatalf("Compile without replacement = %v, want a refusal naming the URL", err)
	}
	external := External{"https://example.invalid/other.json": `{"$defs":{"Thing":{"type":"string"}}}`}
	schema, err := Compile("ext.json", []byte(raw), external)
	if err != nil {
		t.Fatalf("Compile with replacement: %v", err)
	}
	if err := schema.Validate([]byte(`"fine"`)); err != nil {
		t.Errorf("replacement refuses a string: %v", err)
	}
	if err := schema.Validate([]byte(`3`)); err == nil {
		t.Error("replacement accepts a number")
	}
}

func TestCompileRefusals(t *testing.T) {
	if _, err := Compile("bad.json", []byte(`{"type":"nonsense"}`), nil); err == nil {
		t.Error("Compile accepted an unknown type")
	}
	if _, err := Compile("bad.json", []byte(`not json`), nil); err == nil {
		t.Error("Compile accepted text")
	}
	if _, err := CompileVendored("missing/schema.json", nil); err == nil {
		t.Error("CompileVendored accepted a path outside the manifest")
	}
}

func TestDraftSevenSchemasCompile(t *testing.T) {
	schema := mustCompile(t, `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","definitions":{"A":{"const":"x"}},"properties":{"a":{"$ref":"#/definitions/A"}}}`)
	if err := schema.Validate([]byte(`{"a":"x"}`)); err != nil {
		t.Errorf("draft 7 positive: %v", err)
	}
	if err := schema.Validate([]byte(`{"a":"y"}`)); err == nil {
		t.Error("draft 7 negative accepted")
	}
}
