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
	// expected: 0 declares no text (DeclaresNone), so a contract that also binds inputs is refused.
	body := "register:\n  sources:\n    expected: 0\n    sha256: " + testSourceDigest + "\n    inputs:\n      - path: hooks/a.py\n        surface: hooks\n        kind: message\n        format: python\n"
	if _, err := LoadManifest(writeManifest(t, body)); err == nil || !strings.Contains(err.Error(), "takes no inputs") {
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

// Positive (#601): a repository with no non-Markdown agent-facing text declares so explicitly,
// expected: 0 with a reason, and the decoder keeps the reason DeclaresNone reports.
func TestRegisterSourcesDeclaresNone_Positive(t *testing.T) {
	m, err := loadRegisterManifest(t, "register:\n  sources:\n    expected: 0\n    reason: no hook, prompt or MCP text outside Markdown\n")
	if err != nil {
		t.Fatal(err)
	}
	sources := m.Register.Sources
	if !sources.DeclaresNone() || sources.Reason != "no hook, prompt or MCP text outside Markdown" {
		t.Fatalf("empty contract not recognised: %+v", sources)
	}
	withEmptyInputs, err := loadRegisterManifest(t, "register:\n  sources:\n    expected: 0\n    inputs: []\n    reason: none\n")
	if err != nil || !withEmptyInputs.Register.Sources.DeclaresNone() {
		t.Fatalf("explicit empty inputs list refused: %v", err)
	}
}

// Negative (#601): an empty contract without a reason, or with pins or inputs, is refused, and a
// reason on a contract that binds text is refused too; an absent contract declares nothing.
func TestRegisterSourcesDeclaresNone_Negative(t *testing.T) {
	cases := map[string]string{
		"no reason":        "    expected: 0\n",
		"blank reason":     "    expected: 0\n    reason: '  '\n",
		"padded reason":    "    expected: 0\n    reason: ' none'\n",
		"multiline reason": "    expected: 0\n    reason: \"none\\nat all\"\n",
		"sha256":           "    expected: 0\n    sha256: " + testSourceDigest + "\n    reason: none\n",
		"not_applicable":   "    expected: 0\n    not_applicable: 1\n    reason: none\n",
		"reason on text": "    expected: 1\n    sha256: " + testSourceDigest + "\n    reason: none\n    inputs:\n" +
			"      - path: hooks/a.py\n        surface: hooks\n        kind: message\n        format: python\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadRegisterManifest(t, "register:\n  sources:\n"+body); err == nil {
				t.Fatal("invalid empty contract accepted")
			}
		})
	}
	var absent *RegisterSources
	if absent.DeclaresNone() || (&RegisterSources{}).DeclaresNone() {
		t.Fatal("an absent or unvalidated zero contract reads as a declared empty one")
	}
}

// Boundary (#601): the reason is bounded at MaxRegisterSourcesReason bytes, and adding one input
// to an empty contract needs expected and sha256 like any other contract.
func TestRegisterSourcesDeclaresNone_Boundary(t *testing.T) {
	exact := &RegisterSources{Reason: strings.Repeat("r", MaxRegisterSourcesReason)}
	if err := exact.validate(); err != nil {
		t.Fatalf("%d-byte reason refused: %v", MaxRegisterSourcesReason, err)
	}
	above := &RegisterSources{Reason: strings.Repeat("r", MaxRegisterSourcesReason+1)}
	if err := above.validate(); err == nil || !strings.Contains(err.Error(), "1..256 bytes") {
		t.Fatalf("%d-byte reason accepted: %v", MaxRegisterSourcesReason+1, err)
	}
	input := RegisterSourceInput{Path: "hooks/a.py", Surface: SurfaceHooks, Kind: "message", Format: SourceFormatPython}
	oneInput := &RegisterSources{Reason: "none", Inputs: []RegisterSourceInput{input}}
	if err := oneInput.validate(); err == nil || !strings.Contains(err.Error(), "set expected and sha256") {
		t.Fatalf("an input added to an empty contract without pins accepted: %v", err)
	}
	pinned := &RegisterSources{Expected: 1, SHA256: testSourceDigest, Inputs: []RegisterSourceInput{input}}
	if err := pinned.validate(); err != nil || pinned.DeclaresNone() {
		t.Fatalf("pinned contract: err=%v none=%v", err, pinned.DeclaresNone())
	}
}

// ValidRepositoryPath accepts a clean forward-slash path inside the repository (positive),
// refuses an absolute, escaping, unclean, backslash or line-break path (negative), and accepts a
// path of exactly maxRepositoryPath bytes but not one byte more (boundary).
func TestValidRepositoryPath(t *testing.T) {
	for value, want := range map[string]bool{
		"docs/credits.yaml": true, ".agents/skills/x/SKILL.md": true, "go.mod": true,
		"": false, "/etc/passwd": false, "../x": false, "a/../b": false, "a//b": false, "a\\b": false,
		"a\nb": false, ".": false, "..": false,
		strings.Repeat("a", maxRepositoryPath): true, strings.Repeat("a", maxRepositoryPath+1): false,
	} {
		if got := ValidRepositoryPath(value); got != want {
			t.Errorf("ValidRepositoryPath(%q) = %v, want %v", value, got, want)
		}
	}
}
