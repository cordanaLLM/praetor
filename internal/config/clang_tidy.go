// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
	"fmt"
)

// MaxClangTidyLanes bounds the lanes one manifest declares (HISS-02).
const MaxClangTidyLanes = 64

// ClangTidyPolicy is the manifest's clang_tidy section: the lanes in which the repository runs
// clang-tidy. The translation-unit coverage gate (internal/tidycoverage) fails on every
// tracked C, C++, CUDA, HIP or Objective-C++ translation unit that no lane reads and no live
// exceptions entry excuses. It is repository-only, like DocsSurfaces.
type ClangTidyPolicy struct {
	Lanes []ClangTidyLane `yaml:"lanes"`
}

// ClangTidyLane is one clang-tidy lane and what it reads: either the compile database its
// build writes (CompileDatabase, a compile_commands.json) or a list of the files it checks
// (Files, a text file with one repository-relative path per line). Both are repository-relative
// paths; a lane whose input cannot be read fails the gate rather than reading as empty.
type ClangTidyLane struct {
	Name            string `yaml:"name"`
	CompileDatabase string `yaml:"compile_database,omitempty"`
	Files           string `yaml:"files,omitempty"`
}

// Source returns the repository-relative path the lane reads its translation units from.
func (l ClangTidyLane) Source() string {
	if l.CompileDatabase != "" {
		return l.CompileDatabase
	}
	return l.Files
}

// ValidateClangTidy refuses a clang_tidy section the coverage gate could not apply as written:
// no lanes or more than MaxClangTidyLanes, a missing, oversized or repeated lane name, a lane
// naming both or neither of compile_database and files, or a path that is not one clean
// repository-relative file. A nil section declares no lane.
func ValidateClangTidy(policy *ClangTidyPolicy) error {
	if policy == nil {
		return nil
	}
	switch {
	case len(policy.Lanes) == 0:
		return errors.New("clang_tidy.lanes must list at least one lane")
	case len(policy.Lanes) > MaxClangTidyLanes:
		return fmt.Errorf("clang_tidy.lanes has %d lanes; maximum is %d", len(policy.Lanes), MaxClangTidyLanes)
	}
	names := make(map[string]int, len(policy.Lanes))
	for index := 0; index < len(policy.Lanes) && index < MaxClangTidyLanes; index++ {
		lane := policy.Lanes[index]
		if err := lane.validate(fmt.Sprintf("clang_tidy.lanes[%d]", index)); err != nil {
			return err
		}
		if first, repeated := names[lane.Name]; repeated {
			return fmt.Errorf("clang_tidy.lanes[%d] repeats the name %q of clang_tidy.lanes[%d]", index, lane.Name, first)
		}
		names[lane.Name] = index
	}
	return nil
}

// validate checks one lane; prefix places it in the error. The name is a printed label, held
// to the rules generated artefact and docs surface names follow.
func (l ClangTidyLane) validate(prefix string) error {
	if problem := docsSurfaceNameProblem(l.Name); problem != "" {
		return fmt.Errorf("%s.name %s", prefix, problem)
	}
	if (l.CompileDatabase == "") == (l.Files == "") {
		return fmt.Errorf("%s must name exactly one of compile_database and files", prefix)
	}
	if !ValidRepositoryPath(l.Source()) {
		return fmt.Errorf("%s path %q must be one clean repository-relative file path of at most %d bytes",
			prefix, l.Source(), maxRepositoryPath)
	}
	return nil
}
