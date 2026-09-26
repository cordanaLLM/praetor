package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

func emissionTestResolution(resolution Resolution) Resolution {
	resolution.ManifestSHA256 = AbsentRegisterAuthority().ManifestSHA256()
	return resolution
}

func TestValidateEmissionRecordsInternalCompliance(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"})
	valid := "verdict: pass\nchanged: none\nran: go test ./... = pass\nevidence: none\nopen: none"
	record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, valid)
	if err != nil || record.Status != EmissionPass || record.Findings != 0 {
		t.Fatalf("valid internal return = %+v, %v", record, err)
	}
	if record.Register != TextRegisterInternal || record.Source != resolution.Source || record.Surface != SurfaceAgent || record.Kind != caveman.KindReturn {
		t.Fatalf("validation lost resolved identity: %+v", record)
	}

	invalid := "I think the repair is probably ready and it changed internal/example.go."
	record, err = ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, invalid)
	if err == nil || record.Status != EmissionFail || record.Findings < 1 {
		t.Fatalf("invalid internal return = %+v, %v", record, err)
	}
	if len(err.Error()) > MaxEmissionDiagnosticBytes || !strings.Contains(err.Error(), "surface=agent") || !strings.Contains(err.Error(), "kind=return") {
		t.Fatalf("diagnostic not bounded/actionable: %q", err)
	}
}

func TestValidateEmissionBindsExactTextAndContract(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "surfaces.agent"})
	texts := []string{
		"verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none",
		"verdict: fail\nchanged: none\nran: none\nevidence: none\nopen: none",
		"",
	}
	seen := map[string]bool{}
	for index := 0; index < len(texts) && index < 3; index++ {
		text := texts[index]
		record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, text)
		if (err != nil) != (text == "") {
			t.Fatalf("text %d validation error = %v", index, err)
		}
		digest := sha256.Sum256([]byte(text))
		want := hex.EncodeToString(digest[:])
		if record.ContractVersion != EmissionContractVersion || record.TextSHA256 != want || seen[record.TextSHA256] {
			t.Fatalf("text %d binding = %+v, want digest %s", index, record, want)
		}
		seen[record.TextSHA256] = true
	}
}

func TestValidateEmissionSkipsHumanRegisters(t *testing.T) {
	prose := "I think this is the normal full-prose response for a person."
	for _, register := range []TextRegister{TextRegisterSocial, TextRegisterDocs} {
		resolution := emissionTestResolution(Resolution{Register: register, Source: "surfaces.agent"})
		record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindMessage, prose)
		if err != nil || record.Status != EmissionNotApplicable || record.Findings != 0 {
			t.Errorf("%s = %+v, %v", register, record, err)
		}
	}
}

func TestValidateEmissionRejectsInvalidBoundaryMetadata(t *testing.T) {
	valid := "verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none"
	cases := []struct {
		name       string
		resolution Resolution
		surface    RegisterSurface
		kind       caveman.MessageKind
	}{
		{"empty register", Resolution{}, SurfaceAgent, caveman.KindReturn},
		{"unknown register", Resolution{Register: "loud", Source: "surfaces.agent"}, SurfaceAgent, caveman.KindReturn},
		{"unknown surface", Resolution{Register: TextRegisterInternal, Source: "surfaces.agent"}, "chat", caveman.KindReturn},
		{"context kind", Resolution{Register: TextRegisterInternal, Source: "surfaces.agent"}, SurfaceAgent, caveman.KindContext},
		{"unknown kind", Resolution{Register: TextRegisterInternal, Source: "surfaces.agent"}, SurfaceAgent, "memo"},
		{"control in source", Resolution{Register: TextRegisterInternal, Source: "surfaces.agent\x1b"}, SurfaceAgent, caveman.KindReturn},
		{"wrong surface source", Resolution{Register: TextRegisterInternal, Source: "surfaces.agent"}, SurfacePrompts, caveman.KindReturn},
		{"invalid task source", Resolution{Register: TextRegisterInternal, Source: "tasks.bad\tlabel"}, SurfaceAgent, caveman.KindReturn},
		{"escape task source", Resolution{Register: TextRegisterInternal, Source: "tasks.bad\x1b[31m"}, SurfaceAgent, caveman.KindReturn},
		{"vertical task source", Resolution{Register: TextRegisterInternal, Source: "tasks.bad\vlabel"}, SurfaceAgent, caveman.KindReturn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record, err := ValidateEmission(emissionTestResolution(tc.resolution), tc.surface, tc.kind, valid)
			if err == nil || record.Status != EmissionFail {
				t.Fatalf("invalid metadata = %+v, %v", record, err)
			}
		})
	}
}

func TestValidateEmissionBoundsManyFindings(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "surfaces.prompts"})
	text := "\x1b[31m" + strings.Repeat("I think it is probably ready and we should just apply the change.\n", 256)
	record, err := ValidateEmission(resolution, SurfacePrompts, caveman.KindMessage, text)
	if err == nil || record.Status != EmissionFail || record.Findings < MaxEmissionDiagnosticFindings {
		t.Fatalf("many findings = %+v, %v", record, err)
	}
	if len(err.Error()) > MaxEmissionDiagnosticBytes || strings.Count(err.Error(), "line=") > MaxEmissionDiagnosticFindings {
		t.Fatalf("diagnostic exceeded bounds: bytes=%d lines=%d", len(err.Error()), strings.Count(err.Error(), "line="))
	}
	if strings.ContainsRune(err.Error(), '\x1b') {
		t.Fatalf("diagnostic retained terminal control bytes: %q", err)
	}
}

func TestValidateEmissionHonorsResolvedTokenBoundary(t *testing.T) {
	// 197 one-word clauses estimate to exactly the configured 256-token floor without
	// relying on a source-only Markdown suppression construct.
	atLimit := strings.TrimSpace(strings.Repeat("x; ", 197))
	if got := caveman.EstimateTokens(atLimit); got != RegisterMaxTokensFloor {
		t.Fatalf("boundary fixture estimates %d tokens, want %d", got, RegisterMaxTokensFloor)
	}
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, MaxTokens: RegisterMaxTokensFloor, Source: "surfaces.prompts"})
	record, err := ValidateEmission(resolution, SurfacePrompts, caveman.KindMessage, atLimit)
	if err != nil || record.Status != EmissionPass || record.MaxTokens != RegisterMaxTokensFloor {
		t.Fatalf("at-limit emission = %+v, %v", record, err)
	}

	overLimit := atLimit + " x"
	if got := caveman.EstimateTokens(overLimit); got != RegisterMaxTokensFloor+1 {
		t.Fatalf("over-limit fixture estimates %d tokens, want %d", got, RegisterMaxTokensFloor+1)
	}
	record, err = ValidateEmission(resolution, SurfacePrompts, caveman.KindMessage, overLimit)
	if err == nil || record.Status != EmissionFail || record.Findings != 1 || !strings.Contains(err.Error(), caveman.RuleTokenCeiling) {
		t.Fatalf("one-token-over emission = %+v, %v", record, err)
	}

	resolution.MaxTokens = RegisterMaxTokensFloor - 1
	record, err = ValidateEmission(resolution, SurfacePrompts, caveman.KindMessage, atLimit)
	if err == nil || record.Status != EmissionFail {
		t.Fatalf("below-floor resolution = %+v, %v", record, err)
	}
}

func TestValidateEmissionUsesStrictRuntimeProfile(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"})
	base := "verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none\n"
	for name, suffix := range map[string]string{
		"off region": caveman.OffMarker + "\nwe are probably ready\n" + caveman.OnMarker,
		"comment":    "<!-- we are probably ready -->",
		"fence":      "```text\nwe are probably ready\n```",
		"heading":    "## we are probably ready",
		"quote":      "detail: \"we are probably ready\"",
		"inline":     "detail: `we are probably ready`",
	} {
		t.Run(name, func(t *testing.T) {
			record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, base+suffix)
			if err == nil || record.Status != EmissionFail {
				t.Fatalf("strict runtime escape passed: %+v %v", record, err)
			}
		})
	}
}

func TestValidateEmissionRejectsDefaultIgnorableCombiningText(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"})
	hidden := "verdict: pass\nchanged: none\nran: w\u034fe a\u034fre ready\nevidence: none\nopen: none"
	record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, hidden)
	if err == nil || record.Status != EmissionFail || record.Findings == 0 {
		t.Fatalf("default-ignorable grammar = %+v, %v", record, err)
	}
	visible := "verdict: pass\nchanged: none\nran: cafe\u0301 check\nevidence: none\nopen: none"
	record, err = ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, visible)
	if err != nil || record.Status != EmissionPass || record.Findings != 0 {
		t.Fatalf("visible combining text = %+v, %v", record, err)
	}
}

func TestValidateEmissionRejectsUnsafeControls(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"})
	hidden := "verdict: pass\nchanged: none\nran: w\x00e w\x08e w\x7fe\nevidence: none\nopen: none"
	record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, hidden)
	if err == nil || record.Status != EmissionFail || record.Findings == 0 {
		t.Fatalf("unsafe controls = %+v, %v", record, err)
	}
	safeWhitespace := "verdict:\tpass\r\nchanged: none\r\nran: w\te check\r\nevidence: none\r\nopen: none"
	record, err = ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, safeWhitespace)
	if err != nil || record.Status != EmissionPass || record.Findings != 0 {
		t.Fatalf("tab and CRLF boundaries = %+v, %v", record, err)
	}
}

func TestValidateEmissionNormalizesPunctuationAndURLScheme(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"})
	hidden := "verdict: pass\nchanged: none\nran: w.e w:e w_e w-e w+e w=e w#e w|e w~e w,e\n" +
		"evidence: none\nopen: none"
	record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, hidden)
	if err == nil || record.Status != EmissionFail || record.Findings == 0 {
		t.Fatalf("punctuation grammar = %+v, %v", record, err)
	}
	literal := "verdict: pass\nchanged: none\nran: HTTPS://example.test/we/is\nevidence: none\nopen: none"
	record, err = ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, literal)
	if err != nil || record.Status != EmissionPass || record.Findings != 0 {
		t.Fatalf("mixed-case URL literal = %+v, %v", record, err)
	}
	unicodeHidden := "verdict: pass\nchanged: none\nran: w\uFF0Ee w\u00B7e w\u2014e w\uFF0Fe w\u2215e w\u2024e\n" +
		"evidence: none\nopen: none"
	record, err = ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, unicodeHidden)
	if err == nil || record.Status != EmissionFail || record.Findings == 0 {
		t.Fatalf("Unicode punctuation grammar = %+v, %v", record, err)
	}
}

func TestValidateEmissionPreservesCompleteURLAndPathLiterals(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"})
	text := "verdict: pass\n" +
		"changed: C:/work/we/is/value.go internal/we/is/value.go:42 /work/we/is/value.go:42:7 " +
		"C:\\work\\we\\is\\value.go:42 C:/work/we/is/value.go:42\n" +
		"ran: https://example.test/log_(we)/is (HTTPS://example.test/log_(we)/is)\n" +
		"evidence: none\nopen: none"
	record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, text)
	if err != nil || record.Status != EmissionPass || record.Findings != 0 {
		t.Fatalf("complete URL and path literals = %+v, %v", record, err)
	}
}

func TestValidateEmissionRejectsSegmentedGrammarAndPhrases(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"})
	for name, value := range map[string]string{
		"visible combining grammar": "w\u0301e ready",
		"segmented flags":           "--we. --w.e --w-e --w_e",
		"segmented phrases":         "prob.ably note.that in.order.to as.requested",
		"wrapped phrase":            "note\nthat",
	} {
		t.Run(name, func(t *testing.T) {
			text := "verdict: pass\nchanged: none\nran: " + value + "\nevidence: none\nopen: none"
			record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, text)
			if err == nil || record.Status != EmissionFail || record.Findings == 0 {
				t.Fatalf("adversarial emission = %+v, %v", record, err)
			}
		})
	}
}

func TestValidateEmissionPreservesLiteralBoundaries(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"})
	text := "verdict: pass\n" +
		"changed: internal/(we)/is/value.go\n" +
		"ran: notify we@example.com. cafe\u0301 --max-words=3 --write-output --run=TestValue\n" +
		"evidence: none\nopen: none"
	record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, text)
	if err != nil || record.Status != EmissionPass || record.Findings != 0 {
		t.Fatalf("literal boundaries = %+v, %v", record, err)
	}
}

func TestValidateEmissionRejectsCompatibilityAndSeparatorEscapes(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"})
	for name, value := range map[string]string{
		"line separator":      "value\u2028check",
		"paragraph separator": "value\u2029check",
		"modifier apostrophe": "we\u02bcre ready",
		"modifier hedge":      "prob\u02bcably",
		"fullwidth grammar":   "\uff57\uff45 ready",
		"circled grammar":     "ⓦⓔ ready",
		"bold grammar":        "𝐰𝐞 ready",
		"double grammar":      "𝕨𝕖 ready",
		"modifier grammar":    "ʷᵉ ready",
		"pseudo path":         "we／are／value．go",
		"pseudo mail":         "we＠example．com",
	} {
		t.Run(name, func(t *testing.T) {
			text := "verdict: pass\nchanged: none\nran: " + value + "\nevidence: none\nopen: none"
			record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, text)
			if err == nil || record.Status != EmissionFail || record.Findings == 0 {
				t.Fatalf("runtime escape = %+v, %v", record, err)
			}
		})
	}
}

func TestValidateEmissionPreservesWrappedPlatformLiteralsAndShortFlags(t *testing.T) {
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"})
	text := "verdict: pass\n" +
		"changed: (internal/(we)/is/value.go). internal/(we)/is/ " +
		"internal/(we)/is/value.go:42). (C:\\café\\(we)\\is\\value.go:42). " +
		"C:\\café\\(we)\\is\\value.go:42). (\\\\sérver\\share\\(we)\\is\\value.go). " +
		"\\\\sérver\\share\\(we)\\is\\value.go:42).\n" +
		"ran: notify (we@example.com). -I -i -a /I clang -I/usr/include source.c " +
		"clang -I=/usr/include source.c rock\u02bcn\n" +
		"evidence: none\nopen: none"
	record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, text)
	if err != nil || record.Status != EmissionPass || record.Findings != 0 {
		t.Fatalf("platform literal emission = %+v, %v", record, err)
	}
}

func TestValidateEmissionAcceptsMaximumTaskProvenance(t *testing.T) {
	label := strings.Repeat("x", 256)
	resolution := emissionTestResolution(Resolution{Register: TextRegisterInternal, Source: "tasks." + label})
	text := "verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none"
	record, err := ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, text)
	if err != nil || record.Status != EmissionPass || record.Source != resolution.Source {
		t.Fatalf("maximum task provenance rejected: %+v %v", record, err)
	}
	resolution.Source += "x"
	if record, err = ValidateEmission(resolution, SurfaceAgent, caveman.KindReturn, text); err == nil || record.Status != EmissionFail {
		t.Fatalf("overlong task provenance accepted: %+v", record)
	}
}
