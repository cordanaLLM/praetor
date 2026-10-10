package schemacheck

import (
	"reflect"
	"strings"
	"testing"
)

// TestReadYAMLKeepsOnAString is the reason the harness has its own reader: a YAML 1.1 loader
// turns the workflow key on: into the boolean true.
func TestReadYAMLKeepsOnAString(t *testing.T) {
	value, err := ReadYAML([]byte("name: ci\non:\n  push:\n    branches: [main]\njobs: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value %T", value)
	}
	if _, ok := object["on"]; !ok {
		t.Fatalf("keys %v lack the string key on", object)
	}
	if _, ok := object["true"]; ok {
		t.Fatal("on: became the key true")
	}
}

func TestReadYAMLScalars(t *testing.T) {
	value, err := ReadYAML([]byte("a: yes\nb: off\nc: true\nd: ~\ne: 12\nf: 1.5\ng: '7'\nh: 0x10\ni: 1_000\nj: [on, no]\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": "yes", "b": "off", "c": true, "d": nil, "e": 12.0, "f": 1.5, "g": "7", "h": "0x10", "i": "1_000", "j": []any{"on", "no"}}
	if !reflect.DeepEqual(value, want) {
		t.Fatalf("got %#v\nwant %#v", value, want)
	}
}

func TestReadYAMLFeedsTheValidator(t *testing.T) {
	schema := mustCompile(t, `{"type":"object","required":["on"],"properties":{"on":{"type":"object"}}}`)
	value, err := ReadYAML([]byte("on:\n  push: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.ValidateValue(value); err != nil {
		t.Errorf("workflow-shaped document refused: %v", err)
	}
	bad, err := ReadYAML([]byte("on: 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.ValidateValue(bad); err == nil {
		t.Error("wrong type accepted")
	}
}

func TestReadYAMLRefusals(t *testing.T) {
	cases := map[string]string{
		"two documents":   "a: 1\n---\nb: 2\n",
		"duplicate key":   "a: 1\na: 2\n",
		"non-scalar key":  "? [a]\n: 1\n",
		"syntax":          "a: [1, 2\n",
		"empty":           "",
		"tab indentation": "a:\n\tb: 1\n",
	}
	for name, text := range cases {
		if _, err := ReadYAML([]byte(text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReadYAMLAnchorsResolveAndBombsAreBounded(t *testing.T) {
	value, err := ReadYAML([]byte("base: &b {x: 1}\nuse: *b\n"))
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value %T", value)
	}
	if got := object["use"]; !reflect.DeepEqual(got, map[string]any{"x": 1.0}) {
		t.Errorf("alias resolved to %#v", got)
	}
	var bomb strings.Builder
	bomb.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 9; i++ {
		bomb.WriteString("a")
		bomb.WriteString(string(rune('0' + i)))
		bomb.WriteString(": &a")
		bomb.WriteString(string(rune('0' + i)))
		bomb.WriteString(" [")
		for j := 0; j < 10; j++ {
			if j > 0 {
				bomb.WriteString(", ")
			}
			bomb.WriteString("*a" + string(rune('0'+i-1)))
		}
		bomb.WriteString("]\n")
	}
	if _, err := ReadYAML([]byte(bomb.String())); err == nil {
		t.Error("alias bomb accepted")
	}
}
