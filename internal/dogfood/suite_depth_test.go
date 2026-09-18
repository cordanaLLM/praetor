// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dogfood

import (
	"path/filepath"
	"strings"
	"testing"
)

// The 128-component bound on a suite path must hold whichever separator spells it. On
// Windows both '/' and '\' separate components, and the bound previously counted only
// '\', so a slash-separated path of any depth was accepted.
func TestValidateSuitePathDepthBoundHoldsForEverySeparator(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + "/"
	atBound := root + strings.Repeat("x/", 126) + "x"
	if err := validateSuitePath(atBound); err != nil {
		t.Fatalf("path at the depth bound refused: %v", err)
	}
	overBound := root + strings.Repeat("x/", 200) + "x"
	if err := validateSuitePath(overBound); err == nil {
		t.Fatal("slash-separated path past the depth bound accepted")
	}
	hostOver := filepath.FromSlash(overBound)
	if err := validateSuitePath(hostOver); err == nil {
		t.Fatal("host-separated path past the depth bound accepted")
	}
}
