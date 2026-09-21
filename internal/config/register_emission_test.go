package config

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

func TestValidateEmissionRecordsInternalCompliance(t *testing.T) {
	resolution := Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"}
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

func TestValidateEmissionSkipsHumanRegisters(t *testing.T) {
	prose := "I think this is the normal full-prose response for a person."
	for _, register := range []TextRegister{TextRegisterSocial, TextRegisterDocs} {
		record, err := ValidateEmission(Resolution{Register: register, Source: "fixture"}, SurfaceAgent, caveman.KindMessage, prose)
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
		{"unknown register", Resolution{Register: "loud", Source: "fixture"}, SurfaceAgent, caveman.KindReturn},
		{"unknown surface", Resolution{Register: TextRegisterInternal, Source: "fixture"}, "chat", caveman.KindReturn},
		{"context kind", Resolution{Register: TextRegisterInternal, Source: "fixture"}, SurfaceAgent, caveman.KindContext},
		{"unknown kind", Resolution{Register: TextRegisterInternal, Source: "fixture"}, SurfaceAgent, "memo"},
		{"control in source", Resolution{Register: TextRegisterInternal, Source: "fixture\x1b"}, SurfaceAgent, caveman.KindReturn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record, err := ValidateEmission(tc.resolution, tc.surface, tc.kind, valid)
			if err == nil || record.Status != EmissionFail {
				t.Fatalf("invalid metadata = %+v, %v", record, err)
			}
		})
	}
}

func TestValidateEmissionBoundsManyFindings(t *testing.T) {
	resolution := Resolution{Register: TextRegisterInternal, Source: "tasks.ci_debugging"}
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
	// One Markdown heading marker plus 196 words estimates to exactly the configured
	// 256-token floor. Headings are structure, so only C8 can decide this fixture.
	atLimit := "## " + strings.TrimSpace(strings.Repeat("x ", 196))
	if got := caveman.EstimateTokens(atLimit); got != RegisterMaxTokensFloor {
		t.Fatalf("boundary fixture estimates %d tokens, want %d", got, RegisterMaxTokensFloor)
	}
	resolution := Resolution{Register: TextRegisterInternal, MaxTokens: RegisterMaxTokensFloor, Source: "tasks.ci_debugging"}
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
