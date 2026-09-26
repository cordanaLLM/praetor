// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package clientid names the agent clients Praetor knows. It is the one list: clientsetup
// keys its adapter table by these identifiers, and the operator settings in internal/config
// accept exactly these identifiers. It sits below both so neither has to import the other.
package clientid

import (
	"fmt"
	"slices"
	"strings"
)

// ID identifies one agent client.
type ID string

// The known clients. Adding one here without an adapter row and a global-root row fails
// clientsetup's tests.
const (
	AGY        ID = "agy"
	Claude     ID = "claude"
	Cline      ID = "cline"
	Codex      ID = "codex"
	Continue   ID = "continue"
	Gemini     ID = "gemini"
	Kilo       ID = "kilo"
	OpenCodeV1 ID = "opencode-v1"
)

var known = []ID{AGY, Claude, Cline, Codex, Continue, Gemini, Kilo, OpenCodeV1}

// Known returns every known client in sorted order. The slice is a copy.
func Known() []ID { return slices.Clone(known) }

// Parse returns the known client named by value. An unknown name is an error that lists
// the supported set, so a misspelled client in a settings file names its correction.
func Parse(value string) (ID, error) {
	if slices.Contains(known, ID(value)) {
		return ID(value), nil
	}
	names := make([]string, len(known))
	for i, id := range known {
		names[i] = string(id)
	}
	return "", fmt.Errorf("unknown client %q; supported: %s", value, strings.Join(names, ", "))
}
