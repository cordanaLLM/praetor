// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"slices"
	"strings"
	"testing"
)

// preservedReason is the one reason a plan with a preserved custom verify-all gives.
const preservedReason = "Existing custom verify-all is preserved; execute and review it before claiming project verification."

// A repository's own verify-all is its verification contract. Adoption said in one warning that
// the contract was absent and that it was preserved, because the planner's reasons for the
// commands it could not derive (no build step for a Python tool, no marker at all) were kept
// beside the preserved one (#1239). Positive: a preserved verify-all gives that reason alone, with
// no build step and with no marker. Negative: the same trees without the target still say what is
// absent. Boundary: a Makefile holding adoption's failing placeholder is no contract, and its
// plan keeps every reason.
func TestPreservedVerifyAll_3D_ReasonsSayOnlyPreserved(t *testing.T) {
	custom := "verify-all:\n\tpython3 -m unittest\n"
	for name, files := range map[string]map[string]string{
		"python without a build step": {"pytest.ini": "[pytest]\n"},
		"no marker":                   {"README.md": "x\n"},
	} {
		_, plan := verificationFixture(t, withMakefile(files, custom))
		if plan.Status != verificationPreserved || !slices.Equal(plan.Reasons, []string{preservedReason}) {
			t.Errorf("%s with a custom verify-all: %s %q, want preserved with the one reason", name, plan.Status, plan.Reasons)
		}
		_, bare := verificationFixture(t, files)
		if bare.Status != verificationUnavailable || slices.Contains(bare.Reasons, preservedReason) ||
			!slices.ContainsFunc(bare.Reasons, func(reason string) bool { return strings.Contains(reason, "absent") || strings.Contains(reason, "No supported") }) {
			t.Errorf("%s without a Makefile: %s %q, want unavailable naming what is absent", name, bare.Status, bare.Reasons)
		}
	}
	_, placeholder := verificationFixture(t, withMakefile(map[string]string{"pytest.ini": "[pytest]\n"},
		"verify-all:\n"+unavailableVerificationRecipe))
	if placeholder.Status != verificationUnavailable || len(placeholder.Reasons) < 2 ||
		!slices.ContainsFunc(placeholder.Reasons, func(reason string) bool { return strings.Contains(reason, "failing placeholder recipe") }) {
		t.Errorf("a placeholder verify-all: %s %q, want unavailable keeping every reason", placeholder.Status, placeholder.Reasons)
	}
}

// withMakefile returns a copy of files with a Makefile holding makefile.
func withMakefile(files map[string]string, makefile string) map[string]string {
	out := map[string]string{"Makefile": makefile}
	for name, text := range files {
		out[name] = text
	}
	return out
}
