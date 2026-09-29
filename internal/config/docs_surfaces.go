// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"
	"unicode"
)

// Bounds of the docs_surfaces list (HISS-02).
const (
	// MaxDocsSurfaces bounds the surfaces one manifest declares.
	MaxDocsSurfaces = 128
	// MaxDocsSurfaceGlobs bounds each glob list of one surface.
	MaxDocsSurfaceGlobs = 32
	// MaxDocsSurfaceNameBytes bounds a surface name.
	MaxDocsSurfaceNameBytes = 128
)

// DocsSurface is one user-facing surface of the repository, such as a script and its flags, a
// Makefile variable, a workflow trigger or a manifest schema, and the documentation that
// describes it (#608).
//
// Paths and Exclude select the surface's files: a file belongs to the surface when it matches
// a Paths glob and no Exclude glob. Docs lists the documents that describe it. A change that
// touches the surface passes `praetorctl docs references --base=<rev>` only when it also edits
// a file matching a Docs glob, or carries a waiver. A surface without Docs is declared but
// unmapped: a change to it is reported as unmapped rather than passing. Every glob is
// repository-relative under the StyleExclusionProblem rules; a "**" segment spans
// directories and "*" stays inside one.
type DocsSurface struct {
	Name    string   `yaml:"name"`
	Paths   []string `yaml:"paths"`
	Exclude []string `yaml:"exclude,omitempty"`
	Docs    []string `yaml:"docs,omitempty"`
}

// ValidateDocsSurfaces refuses a docs_surfaces list the drift check could not apply as
// written: too many surfaces, a missing, oversized or repeated name, a surface without paths,
// or a glob that is not repository-relative.
func ValidateDocsSurfaces(surfaces []DocsSurface) error {
	if len(surfaces) > MaxDocsSurfaces {
		return fmt.Errorf("docs_surfaces has %d surfaces; maximum is %d", len(surfaces), MaxDocsSurfaces)
	}
	names := make(map[string]int, len(surfaces))
	for index := 0; index < len(surfaces) && index < MaxDocsSurfaces; index++ {
		surface := surfaces[index]
		if err := surface.validate(index); err != nil {
			return err
		}
		if first, repeated := names[surface.Name]; repeated {
			return fmt.Errorf("docs_surfaces[%d] repeats the name %q of docs_surfaces[%d]", index, surface.Name, first)
		}
		names[surface.Name] = index
	}
	return nil
}

// validate checks one surface; index places it in the error.
func (s DocsSurface) validate(index int) error {
	prefix := fmt.Sprintf("docs_surfaces[%d]", index)
	if problem := docsSurfaceNameProblem(s.Name); problem != "" {
		return fmt.Errorf("%s.name %s", prefix, problem)
	}
	if len(s.Paths) == 0 {
		return fmt.Errorf("%s.paths must list at least one glob", prefix)
	}
	lists := []struct {
		key   string
		globs []string
	}{{"paths", s.Paths}, {"exclude", s.Exclude}, {"docs", s.Docs}}
	for _, list := range lists {
		if err := validateDocsSurfaceGlobs(prefix+"."+list.key, list.globs); err != nil {
			return err
		}
	}
	return nil
}

// validateDocsSurfaceGlobs applies the repository-relative glob rules to one list.
func validateDocsSurfaceGlobs(key string, globs []string) error {
	if len(globs) > MaxDocsSurfaceGlobs {
		return fmt.Errorf("%s has %d globs; maximum is %d", key, len(globs), MaxDocsSurfaceGlobs)
	}
	for index := 0; index < len(globs) && index < MaxDocsSurfaceGlobs; index++ {
		if problem := StyleExclusionProblem(globs[index]); problem != "" {
			return fmt.Errorf("%s[%d] %s", key, index, problem)
		}
	}
	return nil
}

// docsSurfaceNameProblem names why a surface name is refused, or returns "". The name is what
// a finding and a waived line print, so it must be one readable line.
func docsSurfaceNameProblem(name string) string {
	switch {
	case strings.TrimSpace(name) == "":
		return "must be a non-empty string"
	case len(name) > MaxDocsSurfaceNameBytes:
		return fmt.Sprintf("exceeds %d bytes", MaxDocsSurfaceNameBytes)
	case strings.ContainsFunc(name, unicode.IsControl):
		return "must not contain a control character"
	}
	return ""
}
