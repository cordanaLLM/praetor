// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"reflect"
	"strings"
	"testing"
)

// universalSocialRow is the social row of a repository that states no convention and keeps no
// changelog fragment directory: only what the engine asserts for every repository (#328).
const universalSocialRow = "| social | forge: issues, PR bodies, review comments, commit bodies | " +
	"`social-text` skill: BLUF, full sentences, scannable, enough and no more; conventional commit subject unchanged |"

// repositoryClauses are conventions of one repository that the engine used to render for every
// adopter. None may appear without the repository stating it or keeping the lane.
var repositoryClauses = []string{"PR template", "receipt fence", "changelog fragment"}

func renderSocialRow(t *testing.T, policy RegisterPolicy) string {
	t.Helper()
	block, err := RenderRegisterBlock(policy, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "| social |") {
			return line
		}
	}
	t.Fatalf("block has no social row:\n%s", block)
	return ""
}

// TestRegisterSocialFormIsUniversal: the default block and the prompt directive carry only the
// engine-universal social form. Negative: none of the repository clauses #328 reported.
func TestRegisterSocialFormIsUniversal(t *testing.T) {
	if row := renderSocialRow(t, DefaultRegisterPolicy()); row != universalSocialRow {
		t.Fatalf("default social row = %q, want %q", row, universalSocialRow)
	}
	directive := RegisterDirective(TextRegisterSocial)
	for _, clause := range repositoryClauses {
		if strings.Contains(directive, clause) {
			t.Errorf("social directive asserts %q for every repository: %q", clause, directive)
		}
	}
	if !strings.Contains(directive, "BLUF, full sentences") || !strings.Contains(directive, "conventional commit subject unchanged") {
		t.Errorf("social directive lost its universal form: %q", directive)
	}
}

// TestRegisterConventionsPositive: a manifest's conventions render after the universal form of
// their register, the social one and any other.
func TestRegisterConventionsPositive(t *testing.T) {
	m, err := loadRegisterManifest(t, `register:
  conventions:
    social: PR template and release notes section unchanged
    docs: ADR template unchanged
`)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	policy := m.EffectiveRegister()
	want := strings.TrimSuffix(universalSocialRow, " |") + "; PR template and release notes section unchanged |"
	if row := renderSocialRow(t, policy); row != want {
		t.Fatalf("social row = %q, want %q", row, want)
	}
	block, err := RenderRegisterBlock(policy, false)
	if err != nil || !strings.Contains(block, "no restated code; ADR template unchanged |") {
		t.Fatalf("docs convention not rendered (%v):\n%s", err, block)
	}
	// A detected fragment directory never overrides what the manifest states.
	if row := renderSocialRow(t, policy.WithDetectedConventions(true)); row != want {
		t.Fatalf("detection replaced the manifest convention: %q", row)
	}
	if strings.Contains(RegisterDirective(TextRegisterSocial), "release notes") {
		t.Error("a repository convention leaked into the engine-wide directive")
	}
}

// TestRegisterConventionsDetection: the fragment clause follows the detected directory when the
// manifest writes no social key, and an explicit empty key states that there is none.
func TestRegisterConventionsDetection(t *testing.T) {
	detected := DefaultRegisterPolicy().WithDetectedConventions(true)
	want := strings.TrimSuffix(universalSocialRow, " |") + "; " + FragmentConvention + " |"
	if row := renderSocialRow(t, detected); row != want {
		t.Fatalf("detected social row = %q, want %q", row, want)
	}
	if row := renderSocialRow(t, DefaultRegisterPolicy().WithDetectedConventions(false)); row != universalSocialRow {
		t.Fatalf("no fragment directory still renders a clause: %q", row)
	}
	declined := RegisterPolicy{Conventions: map[TextRegister]string{TextRegisterSocial: ""}}
	if row := renderSocialRow(t, declined.WithDetectedConventions(true)); row != universalSocialRow {
		t.Fatalf("an explicit empty social key must win over detection: %q", row)
	}
	// Detection copies: the policy it was called on keeps its own map.
	docsOnly := RegisterPolicy{Conventions: map[TextRegister]string{TextRegisterDocs: "ADR template unchanged"}}
	if got := docsOnly.WithDetectedConventions(true).Conventions; got[TextRegisterSocial] != FragmentConvention || got[TextRegisterDocs] == "" {
		t.Fatalf("detection lost the docs convention or did not add the fragment one: %+v", got)
	}
	if _, mutated := docsOnly.Conventions[TextRegisterSocial]; mutated {
		t.Error("WithDetectedConventions mutated the receiver's map")
	}
}

// TestRegisterConventionsNegative: a value that cannot stay in its table cell, and a key that is
// no register, are rejected when the manifest loads or the policy validates.
func TestRegisterConventionsNegative(t *testing.T) {
	cases := map[string]struct{ section, want string }{
		"unknown register":  {"register: {conventions: {loud: x}}\n", `unsupported text register "loud"`},
		"line break":        {"register: {conventions: {social: \"one\\ntwo\"}}\n", "register conventions.social must be one line"},
		"carriage return":   {"register: {conventions: {social: \"one\\rtwo\"}}\n", "register conventions.social must be one line"},
		"table pipe":        {"register: {conventions: {social: \"a | b\"}}\n", "without control characters or '|'"},
		"over the bound":    {"register: {conventions: {social: " + strings.Repeat("x", MaxRegisterConventionBytes+1) + "}}\n", "exceeds 160 bytes"},
		"mapping value":     {"register: {conventions: {social: {text: x}}}\n", "cannot unmarshal !!map into string"},
		"misspelled key":    {"register: {convention: {social: x}}\n", "convention"},
		"duplicated social": {"register: {conventions: {social: a, social: b}}\n", "already defined"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadRegisterManifest(t, tc.section)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
	// A policy built in code passes the same check as one decoded from YAML.
	invalid := RegisterPolicy{Conventions: map[TextRegister]string{"loud": "x"}}
	if err := invalid.validate(); err == nil || !strings.Contains(err.Error(), `unsupported text register "loud"`) {
		t.Fatalf("unknown register in code: error = %v", err)
	}
}

// TestRegisterConventionsBoundary: the byte bound itself, an empty value, an empty section and a
// value with surrounding spaces.
func TestRegisterConventionsBoundary(t *testing.T) {
	atBound := strings.Repeat("x", MaxRegisterConventionBytes)
	m, err := loadRegisterManifest(t, "register: {conventions: {social: "+atBound+"}}\n")
	if err != nil {
		t.Fatalf("a %d-byte value must load: %v", MaxRegisterConventionBytes, err)
	}
	if row := renderSocialRow(t, m.EffectiveRegister()); !strings.HasSuffix(row, "; "+atBound+" |") {
		t.Fatalf("value at the bound not rendered whole: %q", row)
	}
	m, err = loadRegisterManifest(t, "register: {conventions: {social: \"\"}}\n")
	if err != nil {
		t.Fatalf("empty value: %v", err)
	}
	if got, written := m.EffectiveRegister().Conventions[TextRegisterSocial]; !written || got != "" {
		t.Fatalf("an empty social value must stay a written key, got %q (written %v)", got, written)
	}
	m, err = loadRegisterManifest(t, "register: {conventions: {}}\n")
	if err != nil {
		t.Fatalf("empty section: %v", err)
	}
	if got := m.EffectiveRegister(); !reflect.DeepEqual(got, DefaultRegisterPolicy()) {
		t.Fatalf("empty conventions = %+v, want the defaults", got)
	}
	padded := RegisterPolicy{Conventions: map[TextRegister]string{TextRegisterSocial: "  PR template unchanged  "}}
	if row := renderSocialRow(t, padded); !strings.HasSuffix(row, "unchanged; PR template unchanged |") {
		t.Fatalf("surrounding spaces must not reach the cell: %q", row)
	}
}

// TestRegisterConventionsAreCopied: neither EffectiveRegister nor the authority hands out the
// manifest's own map, so a caller cannot rewrite what a later render reads.
func TestRegisterConventionsAreCopied(t *testing.T) {
	authority, err := ParseRegisterAuthority([]byte("version: 1\nregister: {conventions: {social: PR template unchanged}}\n"), ".standards.yaml")
	if err != nil {
		t.Fatal(err)
	}
	first := authority.Policy()
	first.Conventions[TextRegisterSocial] = "changed by a caller"
	if got := authority.Policy().Conventions[TextRegisterSocial]; got != "PR template unchanged" {
		t.Fatalf("authority convention = %q after a caller edited its copy", got)
	}
	if AbsentRegisterAuthority().Policy().Conventions != nil {
		t.Error("a repository without a manifest must state no convention")
	}
}
