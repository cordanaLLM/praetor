package config

import (
	"fmt"
	"strings"
	"testing"
)

const testSourceDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRegisterSourcesPositive(t *testing.T) {
	m, err := loadRegisterManifest(t, `register:
  sources:
    expected: 3
    not_applicable: 2
    sha256: `+testSourceDigest+`
    inputs:
      - path: .config/hooks
        surface: hooks
        kind: message
        format: python
      - path: prompts/agents.yaml
        surface: prompts
        kind: brief
        format: yaml
        selector: agents.*.prompt
`)
	if err != nil {
		t.Fatal(err)
	}
	if m.Register == nil || m.Register.Sources == nil || m.Register.Sources.Expected != 3 ||
		m.Register.Sources.NotApplicable != 2 {
		t.Fatalf("register sources dropped: %+v", m.Register)
	}
}

func TestRegisterSourcesNegative(t *testing.T) {
	base := "register:\n  sources:\n    expected: 1\n    sha256: " + testSourceDigest + "\n    inputs:\n"
	cases := map[string]string{
		"unknown field":    base + "      - path: hooks/a.py\n        surface: hooks\n        kind: message\n        format: python\n        guess: true\n",
		"traversal":        base + "      - path: ../hooks/a.py\n        surface: hooks\n        kind: message\n        format: python\n",
		"unknown format":   base + "      - path: hooks/a.py\n        surface: hooks\n        kind: message\n        format: ruby\n",
		"missing selector": base + "      - path: prompts/a.json\n        surface: prompts\n        kind: message\n        format: json\n",
		"script selector":  base + "      - path: hooks/a.sh\n        surface: hooks\n        kind: message\n        format: shell\n        selector: output\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadManifest(writeManifest(t, body)); err == nil {
				t.Fatal("invalid source contract accepted")
			}
		})
	}
}

func TestRegisterSourcesBoundary(t *testing.T) {
	body := "register:\n  sources:\n    expected: 0\n    sha256: " + testSourceDigest + "\n    inputs:\n      - path: hooks/a.py\n        surface: hooks\n        kind: message\n        format: python\n"
	if _, err := LoadManifest(writeManifest(t, body)); err == nil || !strings.Contains(err.Error(), "expected must be 1") {
		t.Fatalf("zero expected coverage accepted: %v", err)
	}
	body = strings.Replace(body, "expected: 0", "expected: 1", 1)
	body = strings.Replace(body, testSourceDigest, strings.ToUpper(testSourceDigest), 1)
	if _, err := LoadManifest(writeManifest(t, body)); err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Fatalf("non-canonical digest accepted: %v", err)
	}
}

func TestRegisterSourcesCountBoundaries(t *testing.T) {
	// The aggregate is one full table per declared input, not one table for the whole
	// repository: a 256 total left Praetor's own 249-value contract seven values of room.
	if MaxRegisterSourceOutputs != 16384 || MaxRegisterSourceTableValues != 256 {
		t.Fatalf("aggregate/table caps = %d/%d, want 16384/256", MaxRegisterSourceOutputs, MaxRegisterSourceTableValues)
	}
	input := RegisterSourceInput{Path: "hooks/a.py", Surface: SurfaceHooks, Kind: "message", Format: SourceFormatPython}
	exactOutputs := &RegisterSources{Expected: MaxRegisterSourceOutputs, SHA256: testSourceDigest, Inputs: []RegisterSourceInput{input}}
	if err := exactOutputs.validate(); err != nil {
		t.Fatalf("exact %d expected outputs rejected: %v", MaxRegisterSourceOutputs, err)
	}
	aboveOutputs := *exactOutputs
	aboveOutputs.Expected++
	if err := aboveOutputs.validate(); err == nil || !strings.Contains(err.Error(), "1..16384") {
		t.Fatalf("%d expected outputs accepted: %v", MaxRegisterSourceOutputs+1, err)
	}
	aboveClassified := *exactOutputs
	aboveClassified.NotApplicable = MaxRegisterSourceOutputs + 1
	if err := aboveClassified.validate(); err == nil || !strings.Contains(err.Error(), "not_applicable must be 0..16384") {
		t.Fatalf("%d classified outputs accepted: %v", MaxRegisterSourceOutputs+1, err)
	}

	inputs := make([]RegisterSourceInput, MaxRegisterSourceInputs)
	for index := range inputs {
		inputs[index] = input
		inputs[index].Path = fmt.Sprintf("hooks/check-%02d.py", index)
	}
	exactInputs := &RegisterSources{Expected: 1, SHA256: testSourceDigest, Inputs: inputs}
	if err := exactInputs.validate(); err != nil {
		t.Fatalf("exact %d inputs rejected: %v", MaxRegisterSourceInputs, err)
	}
	aboveInputs := *exactInputs
	aboveInputs.Inputs = append(append([]RegisterSourceInput(nil), inputs...), RegisterSourceInput{
		Path: "hooks/check-64.py", Surface: SurfaceHooks, Kind: "message", Format: SourceFormatPython,
	})
	if err := aboveInputs.validate(); err == nil || !strings.Contains(err.Error(), "1..64 rows") {
		t.Fatalf("%d inputs accepted: %v", MaxRegisterSourceInputs+1, err)
	}
}
