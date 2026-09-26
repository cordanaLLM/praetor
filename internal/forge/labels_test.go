package forge

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseLabelTaxonomy_Positive(t *testing.T) {
	labels, err := ParseLabelTaxonomy([]byte(`# comment
version: 1
labels:
  - name: "hiss-violation"
    color: "d73a4a"
    description: "Code introduces a regression against HISS invariants"
  - name: "team:widgets"
    color: "00FF00"
`))
	if err != nil {
		t.Fatalf("valid taxonomy rejected: %v", err)
	}
	want := []Label{
		{Name: "hiss-violation", Color: "d73a4a", Description: "Code introduces a regression against HISS invariants"},
		{Name: "team:widgets", Color: "00FF00"},
	}
	if len(labels) != len(want) || labels[0] != want[0] || labels[1] != want[1] {
		t.Fatalf("ParseLabelTaxonomy = %+v, want %+v", labels, want)
	}
}

func TestParseLabelTaxonomy_Negative(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown key":      "version: 1\nlabels:\n  - {name: a, color: abcdef, colour: abcdef}\n",
		"unknown top key":  "version: 1\nextra: true\nlabels:\n  - {name: a, color: abcdef}\n",
		"two documents":    "version: 1\nlabels:\n  - {name: a, color: abcdef}\n---\nversion: 1\n",
		"wrong version":    "version: 2\nlabels:\n  - {name: a, color: abcdef}\n",
		"empty name":       "version: 1\nlabels:\n  - {name: '', color: abcdef}\n",
		"padded name":      "version: 1\nlabels:\n  - {name: ' a ', color: abcdef}\n",
		"duplicate name":   "version: 1\nlabels:\n  - {name: a, color: abcdef}\n  - {name: a, color: 123456}\n",
		"non-hex color":    "version: 1\nlabels:\n  - {name: a, color: zz0000}\n",
		"short color":      "version: 1\nlabels:\n  - {name: a, color: abc}\n",
		"hash-prefixed":    "version: 1\nlabels:\n  - {name: a, color: '#abcdef'}\n",
		"not a mapping":    "- a\n- b\n",
		"malformed yaml":   "version: [1\n",
		"labels not array": "version: 1\nlabels: a\n",
	} {
		if labels, err := ParseLabelTaxonomy([]byte(doc)); err == nil {
			t.Errorf("%s: invalid taxonomy accepted as %+v", name, labels)
		}
	}
}

func TestParseLabelTaxonomy_Boundary(t *testing.T) {
	build := func(count int) []byte {
		labels := make([]Label, count)
		for i := range labels {
			labels[i] = Label{Name: fmt.Sprintf("label-%d", i), Color: "abcdef"}
		}
		data, err := yaml.Marshal(map[string]any{"version": 1, "labels": labels})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if labels, err := ParseLabelTaxonomy(build(MaxLabelsLimit)); err != nil || len(labels) != MaxLabelsLimit {
		t.Fatalf("exactly MaxLabelsLimit labels: %d, %v", len(labels), err)
	}
	for name, doc := range map[string][]byte{
		"one over the limit": build(MaxLabelsLimit + 1),
		"zero labels":        build(0),
		"empty document":     nil,
		"version only":       []byte("version: 1\n"),
	} {
		if _, err := ParseLabelTaxonomy(doc); err == nil {
			t.Errorf("%s accepted", name)
		} else if name == "one over the limit" && !strings.Contains(err.Error(), fmt.Sprint(MaxLabelsLimit)) {
			t.Errorf("%s: error does not name the bound: %v", name, err)
		}
	}
}
