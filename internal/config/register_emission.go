package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

// EmissionStatus states what the runtime validator proved for one emitted text.
type EmissionStatus string

const (
	EmissionPass          EmissionStatus = "pass"
	EmissionFail          EmissionStatus = "fail"
	EmissionNotApplicable EmissionStatus = "not_applicable"

	// MaxEmissionDiagnosticFindings keeps producer errors useful without returning every
	// repeated grammar finding to another agent.
	MaxEmissionDiagnosticFindings = 3
	// MaxEmissionDiagnosticBytes bounds the complete error sent across a runtime seam.
	MaxEmissionDiagnosticBytes = 768
	emissionExcerptBytes       = 160
)

// EmissionValidation records one runtime register decision. Findings is a count only;
// bounded actionable detail travels in the returned error when Status is fail.
type EmissionValidation struct {
	Status    EmissionStatus      `json:"status"`
	Surface   RegisterSurface     `json:"surface"`
	Kind      caveman.MessageKind `json:"kind"`
	Register  TextRegister        `json:"register"`
	MaxTokens int                 `json:"max_tokens,omitempty"`
	Source    string              `json:"source"`
	Findings  int                 `json:"findings"`
}

// ValidateEmission checks engine-owned runtime text against its resolved register through
// caveman.Check. Human-facing social/docs registers are recorded as not_applicable.
// Internal text fails closed on invalid metadata, invalid UTF-8, empty text, or a finding.
func ValidateEmission(resolution Resolution, surface RegisterSurface, kind caveman.MessageKind, text string) (EmissionValidation, error) {
	record := EmissionValidation{Surface: surface, Kind: kind, Register: resolution.Register,
		MaxTokens: resolution.MaxTokens, Source: resolution.Source}
	if err := validateEmissionMetadata(record, text); err != nil {
		record.Status = EmissionFail
		return record, emissionError(record, err.Error(), nil)
	}
	if resolution.Register != TextRegisterInternal {
		record.Status = EmissionNotApplicable
		return record, nil
	}
	report := caveman.Check(text, caveman.Options{Kind: kind, MaxTokens: resolution.MaxTokens})
	record.Findings = len(report.Findings)
	if report.Passed() {
		record.Status = EmissionPass
		return record, nil
	}
	record.Status = EmissionFail
	return record, emissionError(record, "Caveman contract rejected engine-owned text", report.Findings)
}

func validateEmissionMetadata(record EmissionValidation, text string) error {
	if !KnownRegisterSurface(record.Surface) {
		return fmt.Errorf("unknown register surface %q", record.Surface)
	}
	if !runtimeEmissionKind(record.Kind) {
		return fmt.Errorf("runtime message kind %q is unsupported", record.Kind)
	}
	if !runtimeEmissionRegister(record.Register) {
		return fmt.Errorf("resolved register %q is unsupported", record.Register)
	}
	if err := validateEmissionMaxTokens(record.MaxTokens); err != nil {
		return err
	}
	if err := validateEmissionSource(record.Source); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("emitted text must be nonempty UTF-8")
	}
	if !utf8.ValidString(text) {
		return errors.New("emitted text must be nonempty UTF-8")
	}
	return nil
}

func runtimeEmissionKind(kind caveman.MessageKind) bool {
	return kind == caveman.KindMessage || kind == caveman.KindBrief || kind == caveman.KindReturn
}

func runtimeEmissionRegister(register TextRegister) bool {
	return register == TextRegisterInternal || register == TextRegisterSocial || register == TextRegisterDocs
}

func validateEmissionMaxTokens(maxTokens int) error {
	if maxTokens == 0 {
		return nil
	}
	if maxTokens < RegisterMaxTokensFloor || maxTokens > RegisterMaxTokensCeiling {
		return fmt.Errorf("resolved max_tokens must be %d..%d", RegisterMaxTokensFloor, RegisterMaxTokensCeiling)
	}
	return nil
}

func validateEmissionSource(source string) error {
	if source == "" || len(source) > 256 {
		return errors.New("register resolution source must be bounded single-line text")
	}
	if strings.IndexFunc(source, unicode.IsControl) >= 0 {
		return errors.New("register resolution source must be bounded single-line text")
	}
	return nil
}

func emissionError(record EmissionValidation, reason string, findings []caveman.Finding) error {
	var detail strings.Builder
	fmt.Fprintf(&detail, "runtime text validation failed: surface=%s kind=%s register=%s source=%s: %s",
		record.Surface, record.Kind, record.Register, record.Source, reason)
	limit := len(findings)
	if limit > MaxEmissionDiagnosticFindings {
		limit = MaxEmissionDiagnosticFindings
	}
	for index := 0; index < limit; index++ {
		finding := findings[index]
		fmt.Fprintf(&detail, "; line=%d %s: %s", finding.Line, finding.Rule, boundEmissionText(finding.Excerpt, emissionExcerptBytes))
	}
	if len(findings) > limit {
		fmt.Fprintf(&detail, "; additional_findings=%d", len(findings)-limit)
	}
	return errors.New(boundEmissionText(detail.String(), MaxEmissionDiagnosticBytes))
}

func boundEmissionText(text string, limit int) string {
	text = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}
		return char
	}, text)
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "")
}
