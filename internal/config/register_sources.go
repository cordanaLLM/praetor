package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	// MaxRegisterSourceInputs bounds declared non-Markdown source scopes.
	MaxRegisterSourceInputs = 64
	// MaxRegisterSourceTableValues bounds the values one selector match set or one Go string
	// table yields. It is a per-construct bound, not a repository total.
	MaxRegisterSourceTableValues = 256
	// MaxRegisterSourceOutputs bounds the applicable and, separately, the not-applicable
	// values a whole contract extracts: every declared input may contribute one full table.
	// The selected-byte bound in cavemansource stays the tighter aggregate limit.
	MaxRegisterSourceOutputs = MaxRegisterSourceInputs * MaxRegisterSourceTableValues
	// MaxRegisterSourcesReason bounds the reason an empty contract (expected: 0) records.
	MaxRegisterSourcesReason = 256
	maxRepositoryPath        = 256
)

// RegisterSourceFormat selects the deterministic parser for a declared source scope.
type RegisterSourceFormat string

const (
	SourceFormatShell  RegisterSourceFormat = "shell"
	SourceFormatPython RegisterSourceFormat = "python"
	SourceFormatGo     RegisterSourceFormat = "go"
	SourceFormatJSON   RegisterSourceFormat = "json"
	SourceFormatYAML   RegisterSourceFormat = "yaml"
)

// RegisterSourceInput declares one file or directory of agent-facing runtime text.
// Shell and Python discover output calls. Go, JSON and YAML require a dotted selector.
type RegisterSourceInput struct {
	Path     string               `yaml:"path"`
	Surface  RegisterSurface      `yaml:"surface"`
	Kind     string               `yaml:"kind"`
	Format   RegisterSourceFormat `yaml:"format"`
	Selector string               `yaml:"selector,omitempty"`
}

// RegisterSources is the omission-resistant coverage contract for non-Markdown text.
// Expected and SHA256 bind the complete extracted inventory; deleting a declaration or
// a matched value therefore fails until the reviewed policy is deliberately updated.
//
// A repository with no non-Markdown agent-facing text declares that explicitly: expected: 0
// with a reason, and no inputs, not_applicable or sha256 (DeclaresNone). An absent contract
// still declares nothing and fails the audit closed (#601).
type RegisterSources struct {
	Expected      int                   `yaml:"expected"`
	NotApplicable int                   `yaml:"not_applicable,omitempty"`
	SHA256        string                `yaml:"sha256,omitempty"`
	Inputs        []RegisterSourceInput `yaml:"inputs,omitempty"`
	// Reason says why the repository has no agent-facing text to bind; only an empty contract
	// (expected: 0) carries one, and it must.
	Reason string `yaml:"reason,omitempty"`
}

// DeclaresNone reports a validated contract that binds no text: expected: 0 with a reason.
// Audit, `caveman check --configured-sources` and adoption read it here, so the three agree on
// what an empty declaration means.
func (s *RegisterSources) DeclaresNone() bool {
	return s != nil && s.Expected == 0 && len(s.Inputs) == 0 && s.Reason != ""
}

func (s *RegisterSources) validate() error {
	if s == nil {
		return nil
	}
	if s.Expected == 0 {
		return s.validateNone()
	}
	if s.Reason != "" {
		return errors.New("register sources reason applies only to a contract that declares no text (expected: 0)")
	}
	if err := s.validateCoverage(); err != nil {
		return err
	}
	return s.validateInputs()
}

// validateNone accepts an empty contract only as a complete, reviewable statement: no inputs,
// no pins, and a one-line reason. Binding a first input means setting expected and sha256 to
// the values it extracts, as for any other contract.
func (s *RegisterSources) validateNone() error {
	if len(s.Inputs) > 0 || s.NotApplicable != 0 || s.SHA256 != "" {
		return errors.New("register sources expected: 0 declares no text, so it takes no inputs, not_applicable " +
			"or sha256; to bind inputs, set expected and sha256 to the values they extract")
	}
	reason := strings.TrimSpace(s.Reason)
	if reason == "" || reason != s.Reason || len(reason) > MaxRegisterSourcesReason ||
		strings.ContainsAny(reason, "\x00\r\n") {
		return fmt.Errorf("register sources expected: 0 requires a reason of 1..%d bytes on one line, "+
			"without surrounding whitespace, saying why the repository has no agent-facing text", MaxRegisterSourcesReason)
	}
	return nil
}

func (s *RegisterSources) validateCoverage() error {
	if s.Expected < 1 || s.Expected > MaxRegisterSourceOutputs {
		return fmt.Errorf("register sources expected must be 1..%d, or 0 with a reason to declare no text", MaxRegisterSourceOutputs)
	}
	if s.NotApplicable < 0 || s.NotApplicable > MaxRegisterSourceOutputs {
		return fmt.Errorf("register sources not_applicable must be 0..%d", MaxRegisterSourceOutputs)
	}
	if !validRegisterSourceDigest(s.SHA256) {
		return errors.New("register sources sha256 must be sha256: followed by 64 lowercase hexadecimal characters")
	}
	return nil
}

func (s *RegisterSources) validateInputs() error {
	if len(s.Inputs) < 1 || len(s.Inputs) > MaxRegisterSourceInputs {
		return fmt.Errorf("register sources inputs must contain 1..%d rows", MaxRegisterSourceInputs)
	}
	seen := make(map[string]bool, len(s.Inputs))
	for index := range s.Inputs {
		if err := s.Inputs[index].Validate(); err != nil {
			return fmt.Errorf("register sources inputs[%d]: %w", index, err)
		}
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", s.Inputs[index].Path,
			s.Inputs[index].Surface, s.Inputs[index].Kind, s.Inputs[index].Format,
			s.Inputs[index].Selector)
		if seen[key] {
			return fmt.Errorf("register sources inputs[%d] duplicates an earlier row", index)
		}
		seen[key] = true
	}
	return nil
}

// Validate rejects an unsafe or ambiguous source declaration before discovery.
func (s RegisterSourceInput) Validate() error {
	if err := s.validateIdentity(); err != nil {
		return err
	}
	return s.validateParser()
}

func (s RegisterSourceInput) validateIdentity() error {
	if !ValidRepositoryPath(s.Path) {
		return errors.New("path must be a clean local forward-slash path of at most 256 bytes")
	}
	if s.Surface != SurfaceMCP && s.Surface != SurfaceHooks && s.Surface != SurfacePrompts {
		return fmt.Errorf("surface %q must be mcp, hooks, or prompts", s.Surface)
	}
	if s.Kind != "message" && s.Kind != "brief" && s.Kind != "return" {
		return fmt.Errorf("kind %q must be message, brief, or return", s.Kind)
	}
	return nil
}

func (s RegisterSourceInput) validateParser() error {
	selected := s.Format == SourceFormatGo || s.Format == SourceFormatJSON || s.Format == SourceFormatYAML
	if !selected && s.Format != SourceFormatShell && s.Format != SourceFormatPython {
		return fmt.Errorf("format %q must be shell, python, go, json, or yaml", s.Format)
	}
	if selected != (s.Selector != "") {
		return errors.New("go/json/yaml require selector; shell/python prohibit selector")
	}
	if s.Selector != "" && !validRegisterSourceSelector(s.Selector) {
		return errors.New("selector must contain 1..16 dotted keys, indexes, or wildcards")
	}
	return nil
}

// ValidRepositoryPath reports whether value names a file inside the repository the way the
// manifest spells one: a clean forward-slash path of at most maxRepositoryPath bytes, never
// absolute or escaping. register.sources inputs, the hiss.exceptions documents, the declared
// exceptions list and the readers of other repository files that name paths the same way, such
// as docs/credits.yaml (internal/supplychain), share it.
func ValidRepositoryPath(value string) bool {
	return value != "" && len(value) <= maxRepositoryPath && !strings.ContainsAny(value, "\\\x00\r\n") &&
		!strings.HasPrefix(value, "/") && path.Clean(value) == value && value != "." &&
		value != ".." && !strings.HasPrefix(value, "../")
}

func validRegisterSourceSelector(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > 16 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 128 || strings.ContainsAny(part, "\x00\r\n") {
			return false
		}
	}
	return true
}

func validRegisterSourceDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[len("sha256:"):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
