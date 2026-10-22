// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const (
	// MaxDeclaredStatusContexts bounds the repository's additional required contexts.
	MaxDeclaredStatusContexts = 256
	// MaxStatusContextCharacters bounds one declared context in Unicode characters.
	MaxStatusContextCharacters = 255
)

// DeclaredStatusContexts returns a copy of the repository-only required contexts.
// Catalog layers cannot declare them, so sync can read them before resolving policy.
func (o Overrides) DeclaredStatusContexts() []string {
	if o.BranchProtection == nil {
		return nil
	}
	return slices.Clone(o.BranchProtection.StatusContexts)
}

func validateStatusContextNodes(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.SequenceNode {
		return errors.New("status_contexts must be a list of strings")
	}
	if len(node.Content) > MaxDeclaredStatusContexts {
		return fmt.Errorf("status_contexts exceeds %d entries", MaxDeclaredStatusContexts)
	}
	for i := 0; i < len(node.Content) && i < MaxDeclaredStatusContexts; i++ {
		if node.Content[i].Kind != yaml.ScalarNode || node.Content[i].Tag != "!!str" {
			return fmt.Errorf("status_contexts entry %d must be a string", i)
		}
	}
	return nil
}

func validateStatusContexts(contexts []string) error {
	if len(contexts) > MaxDeclaredStatusContexts {
		return fmt.Errorf("status_contexts exceeds %d entries", MaxDeclaredStatusContexts)
	}
	seen := make(map[string]bool, len(contexts))
	for i := 0; i < len(contexts) && i < MaxDeclaredStatusContexts; i++ {
		if err := validateStatusContext(contexts[i]); err != nil {
			return fmt.Errorf("status_contexts entry %d: %w", i, err)
		}
		if seen[contexts[i]] {
			return fmt.Errorf("status_contexts duplicate context %q", contexts[i])
		}
		seen[contexts[i]] = true
	}
	return nil
}

func validateStatusContext(value string) error {
	switch {
	case strings.TrimSpace(value) == "":
		return errors.New("context cannot be empty")
	case strings.ContainsFunc(value, unicode.IsControl):
		return errors.New("context cannot contain control characters")
	case strings.TrimSpace(value) != value:
		return errors.New("context cannot have surrounding whitespace")
	case !utf8.ValidString(value):
		return errors.New("context must be valid UTF-8")
	case utf8.RuneCountInString(value) > MaxStatusContextCharacters:
		return fmt.Errorf("context exceeds %d characters", MaxStatusContextCharacters)
	}
	return nil
}
