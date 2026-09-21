package config

import (
	"strings"
	"testing"
)

const testSourceDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRegisterSourcesPositive(t *testing.T) {
	m, err := loadRegisterManifest(t, `register:
  sources:
    expected: 3
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
	if m.Register == nil || m.Register.Sources == nil || m.Register.Sources.Expected != 3 {
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
