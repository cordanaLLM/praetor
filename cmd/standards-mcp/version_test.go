// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import "testing"

// declaredDefaultVersion snapshots the shipped default before any test assigns to version.
var declaredDefaultVersion = version

// Negative: the shipped default must be empty. The v1.0.0 default was reported by every
// build that did not pass -X main.mcpVersion, releases included, because the release config
// writes main.version (#666).
func TestShippedDefaultVersionIsEmpty(t *testing.T) {
	if declaredDefaultVersion != "" {
		t.Errorf("the default version must be empty so an injected one is observable, got %q", declaredDefaultVersion)
	}
}
