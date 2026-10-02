// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"regexp"
	"strings"
)

// conventionalHeader is a Conventional Commits 1.0.0 header: a type, an optional scope, an
// optional "!" and ": " before a non-blank description.
var conventionalHeader = regexp.MustCompile(`^([a-z][a-z0-9-]*)(?:\(([^()\r\n]+)\))?!?: \S`)

// Matches reports whether a change on branch titled title is a regeneration change: the branch,
// with or without refs/heads/, starts with the prefix and names something after it, and the
// title's conventional type, and its scope when the marker names one, are the marker's. Both
// must hold; an empty branch or title carries no marker.
func (m Marker) Matches(branch, title string) bool {
	branch = strings.TrimPrefix(branch, "refs/heads/")
	if m.BranchPrefix == "" || len(branch) <= len(m.BranchPrefix) || !strings.HasPrefix(branch, m.BranchPrefix) {
		return false
	}
	return m.titleCarries(title)
}

// titleCarries reports whether title's conventional header carries the marker's type and scope.
func (m Marker) titleCarries(title string) bool {
	wantType, wantScope, scoped := strings.Cut(m.TitleType, "(")
	if wantType == "" {
		return false
	}
	header := conventionalHeader.FindStringSubmatch(strings.TrimSpace(title))
	if header == nil || header[1] != wantType {
		return false
	}
	return !scoped || header[2] == strings.TrimSuffix(wantScope, ")")
}

// String renders the marker the way a refusal names it.
func (m Marker) String() string {
	return "branch " + m.BranchPrefix + "<name> and a title " + m.TitleType + ": <summary>"
}
