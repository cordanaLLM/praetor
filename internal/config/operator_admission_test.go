// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func loadAdmissionLayers(t *testing.T, fleet, workstation string) (*EffectivePolicy, error) {
	t.Helper()
	dir := t.TempDir()
	opts := EffectiveOptions{Root: policyFixture(t, "", "", ""), FleetPath: writePolicyFile(t, dir, "fleet.yaml", fleet)}
	if workstation != "" {
		opts.WorkstationPath = writePolicyFile(t, dir, "workstation.yaml", workstation)
	}
	return LoadEffectivePolicyContext(t.Context(), opts)
}

func admissionDocument(from, count int) string {
	var body strings.Builder
	body.WriteString("hooks:\n  admission:\n    commands:\n")
	for i := from; i < from+count; i++ {
		fmt.Fprintf(&body, "      - '^tool-%03d\\b'\n", i)
	}
	return body.String()
}

// Positive: a host opts in, and the command shapes of two layers append.
func TestOperatorAdmission_Positive_EnforceAndCommandsMerge(t *testing.T) {
	policy, err := loadAdmissionLayers(t, "hooks:\n  admission:\n    commands: ['^just\\s+build\\b']\n",
		"hooks:\n  admission:\n    enforce: true\n    commands: ['^bazel\\b']\n")
	if err != nil {
		t.Fatal(err)
	}
	got := policy.OperatorSettings().Hooks.Admission
	if !got.Enforce || got.MaxJobsDivisor != DefaultAdmissionMaxJobsDivisor ||
		!reflect.DeepEqual(got.Commands, []string{`^just\s+build\b`, `^bazel\b`}) {
		t.Fatalf("admission settings %+v", got)
	}
	scoped, err := loadAdmissionLayers(t, "hooks:\n  admission:\n    enforce: true\n    scope_form: true\n    max_jobs_divisor: 4\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := scoped.OperatorSettings().Hooks.Admission; !got.Enforce || !got.ScopeForm || got.MaxJobsDivisor != 4 {
		t.Fatalf("scoped admission settings %+v", got)
	}
	if err := ValidateAdmissionMaxJobsDivisor(DefaultAdmissionMaxJobsDivisor); err != nil {
		t.Fatal(err)
	}
}

// Negative: a shape that does not compile is refused, divisor below 1 is refused, and once a
// layer turns enforcement on a later layer cannot turn it off.
func TestOperatorAdmission_Negative_RefusedAndCannotLoosen(t *testing.T) {
	if _, err := loadAdmissionLayers(t, "hooks:\n  admission:\n    commands: ['(']\n", ""); err == nil {
		t.Fatal("an admission shape that does not compile was accepted")
	}
	if _, err := loadAdmissionLayers(t, "hooks:\n  admission:\n    enfroce: true\n", ""); err == nil {
		t.Fatal("a misspelled admission key was accepted")
	}
	for _, badDivisor := range []string{"0", "-2", `"3"`} {
		if _, err := loadAdmissionLayers(t, "hooks:\n  admission:\n    max_jobs_divisor: "+badDivisor+"\n", ""); err == nil {
			t.Fatalf("max_jobs_divisor %s was accepted", badDivisor)
		}
	}
	if err := ValidateAdmissionMaxJobsDivisor(0); err == nil {
		t.Fatal("divisor 0 accepted")
	}
	_, err := loadAdmissionLayers(t, "hooks: {admission: {enforce: true}}\n", "hooks: {admission: {enforce: false}}\n")
	if err == nil || !strings.Contains(err.Error(), "fleet") || !strings.Contains(err.Error(), "workstation") {
		t.Fatalf("turning admission off in a later layer must fail naming both layers: %v", err)
	}
}

// Boundary: the bound applies to the merged list, and a document without the section leaves
// the defaults, enforcement off.
func TestOperatorAdmission_Boundary_MergedBoundAndDefault(t *testing.T) {
	if _, err := loadAdmissionLayers(t, admissionDocument(0, 20), admissionDocument(8, 24)); err != nil {
		t.Fatalf("32 distinct merged shapes refused: %v", err)
	}
	if _, err := loadAdmissionLayers(t, admissionDocument(0, 20), admissionDocument(7, 26)); err == nil {
		t.Fatal("33 distinct merged shapes accepted")
	}
	policy, err := loadAdmissionLayers(t, "hooks: {scope: governed}\n", "")
	if err != nil || policy.OperatorSettings().Hooks.Admission.Enforce || len(policy.OperatorSettings().Hooks.Admission.Commands) != 0 {
		t.Fatalf("a document without the section: %+v, %v", policy.OperatorSettings().Hooks.Admission, err)
	}
	if err := ValidateAdmissionMaxJobsDivisor(1); err != nil {
		t.Fatal(err)
	}
}
