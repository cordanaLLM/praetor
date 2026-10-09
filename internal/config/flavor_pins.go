// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"
)

// Bounds of the flavors list (HISS-02).
const (
	// MaxFlavorPins bounds the pins one manifest declares.
	MaxFlavorPins = 32
)

// FlavorPin pins one flavor to the repository, or to one directory of it (#1103).
//
// Without pins the flavor is detected from the declared profile and the repository's markers.
// A pin replaces detection for every command that resolves a flavor: `flavor audit`, `gate run`
// and the generated pre-push hook, which runs the audit. Path scopes the pin to a repository
// relative directory, so a repository with several components audits each against its own
// flavor; an empty Path is the repository root. The flavor name is checked against the registry
// by internal/flavor, which owns it.
type FlavorPin struct {
	Name string `yaml:"name"`
	Path string `yaml:"path,omitempty"`
}

// ValidateFlavorPins refuses a flavors list the resolver could not apply as written: too many
// pins, a missing name, a path that leaves the repository, or the same name and path twice.
func ValidateFlavorPins(pins []FlavorPin) error {
	if len(pins) > MaxFlavorPins {
		return fmt.Errorf("flavors has %d pins; maximum is %d", len(pins), MaxFlavorPins)
	}
	seen := make(map[FlavorPin]int, len(pins))
	for i := 0; i < len(pins) && i < MaxFlavorPins; i++ {
		if err := pins[i].validate(i); err != nil {
			return err
		}
		key := FlavorPin{Name: strings.TrimSpace(pins[i].Name), Path: pins[i].CleanPath()}
		if first, repeated := seen[key]; repeated {
			return fmt.Errorf("flavors[%d] repeats flavors[%d] (%s at %q)", i, first, key.Name, key.Path)
		}
		seen[key] = i
	}
	return nil
}

// CleanPath returns the pin's repository-relative path as written, trimmed; empty and "." both
// mean the repository root and come back as ".". Any other spelling must already be a clean
// path, which validate checks with ValidRepositoryPath.
func (p FlavorPin) CleanPath() string {
	trimmed := strings.TrimSpace(p.Path)
	if trimmed == "" {
		return "."
	}
	return trimmed
}

func (p FlavorPin) validate(index int) error {
	prefix := fmt.Sprintf("flavors[%d]", index)
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%s.name must name a flavor (praetorctl flavor list names each)", prefix)
	}
	clean := p.CleanPath()
	if clean != "." && (!ValidRepositoryPath(clean) || strings.ContainsRune(clean, ':')) {
		return fmt.Errorf("%s.path %q must be a clean forward-slash directory inside the repository", prefix, p.Path)
	}
	return nil
}
