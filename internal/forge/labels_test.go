package forge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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

func parseTaxonomy(t *testing.T, data []byte) []Label {
	t.Helper()
	labels, err := ParseLabelTaxonomy(data)
	if err != nil {
		t.Fatalf("taxonomy must parse: %v", err)
	}
	return labels
}

// TestDefaultLabelTaxonomy_Positive pins the single taxonomy: fourteen unique labels, carried
// byte for byte by praetor's own .config/labels.yaml, so the shipped default and the dogfooded
// file cannot drift apart again.
func TestDefaultLabelTaxonomy_Positive(t *testing.T) {
	labels := parseTaxonomy(t, DefaultLabelTaxonomy())
	if len(labels) != 14 {
		t.Fatalf("expected 14 labels, got %d", len(labels))
	}
	// yamllint's default document-start rule, which make hooks-lint applies (BUG-782).
	if !bytes.HasPrefix(DefaultLabelTaxonomy(), []byte("---\n")) {
		t.Error("the taxonomy adoption writes must open with the --- document start")
	}
	own, err := os.ReadFile(filepath.Join("..", "..", ".config", "labels.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout under core.autocrlf writes CRLF; the committed blob is LF.
	own = bytes.ReplaceAll(own, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(own, DefaultLabelTaxonomy()) {
		t.Error(".config/labels.yaml and forge.DefaultLabelTaxonomy have drifted apart")
	}
}

// TestDefaultLabelTaxonomy_Negative asserts no label is duplicated or missing a color.
func TestDefaultLabelTaxonomy_Negative(t *testing.T) {
	seen := map[string]bool{}
	for _, label := range parseTaxonomy(t, DefaultLabelTaxonomy()) {
		if label.Name == "" || seen[label.Name] || len(label.Color) != 6 {
			t.Errorf("invalid or duplicate label %+v", label)
		}
		seen[label.Name] = true
	}
}

// TestDefaultLabelTaxonomy_Negative_NoSignedWaiverClaim: no gate verifies a signature on a
// waiver, so no label may say one is required, and hiss-waiver says so outright (#393). The
// earlier text, "Requires cryptographically signed waiver approval", fails both checks.
func TestDefaultLabelTaxonomy_Negative_NoSignedWaiverClaim(t *testing.T) {
	waiver := ""
	for _, label := range parseTaxonomy(t, DefaultLabelTaxonomy()) {
		text := strings.ToLower(label.Description)
		if strings.Contains(text, "cryptograph") || strings.Contains(strings.ReplaceAll(text, "not signed", ""), "signed") {
			t.Errorf("label %s claims a signature: %q", label.Name, label.Description)
		}
		if label.Name == "hiss-waiver" {
			waiver = text
		}
	}
	if !strings.Contains(waiver, "not signed") {
		t.Errorf("the hiss-waiver description must say waivers are not signed, got %q", waiver)
	}
}

// TestDefaultLabelTaxonomy_Boundary asserts every call returns an independent copy, so a
// caller that mutates the bytes cannot change what the next caller writes.
func TestDefaultLabelTaxonomy_Boundary(t *testing.T) {
	first := DefaultLabelTaxonomy()
	first[0] = 'X'
	if DefaultLabelTaxonomy()[0] == 'X' {
		t.Fatal("DefaultLabelTaxonomy must return a fresh copy")
	}
}
