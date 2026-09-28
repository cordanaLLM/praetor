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
type RegisterSources struct {
	Expected      int                   `yaml:"expected"`
	NotApplicable int                   `yaml:"not_applicable,omitempty"`
	SHA256        string                `yaml:"sha256"`
	Inputs        []RegisterSourceInput `yaml:"inputs"`
}

func (s *RegisterSources) validate() error {
	if s == nil {
		return nil
	}
	if err := s.validateCoverage(); err != nil {
		return err
	}
	return s.validateInputs()
}

func (s *RegisterSources) validateCoverage() error {
	if s.Expected < 1 || s.Expected > MaxRegisterSourceOutputs {
		return fmt.Errorf("register sources expected must be 1..%d", MaxRegisterSourceOutputs)
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
	if !validRepositoryPath(s.Path) {
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

// validRepositoryPath reports whether value names a file inside the repository the way the
// manifest spells one: a clean forward-slash path of at most maxRepositoryPath bytes, never
// absolute or escaping. register.sources inputs and the hiss.exceptions documents share it.
func validRepositoryPath(value string) bool {
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
