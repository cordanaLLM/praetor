// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestVerificationPolicy_Positive: a declared verification section is read as written, a bound it
// omits stays zero (undeclared), it survives RenderManifest, and DeclaredVerification returns it;
// an absent section and a nil manifest declare none.
func TestVerificationPolicy_Positive(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, "version: 1\nverification:\n  max_entries: 131072\n  max_depth: 40\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.DeclaredVerification(); got == nil || *got != (VerificationPolicy{MaxEntries: 131072, MaxDepth: 40}) {
		t.Fatalf("declared verification = %+v; want max_entries 131072, max_depth 40, max_files undeclared", got)
	}
	rendered, err := RenderManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadManifest(writeManifest(t, string(rendered)))
	if err != nil || again.DeclaredVerification() == nil || *again.DeclaredVerification() != *m.Verification {
		t.Fatalf("rendered manifest reloads as %+v, %v:\n%s", again, err, rendered)
	}
	absent, err := LoadManifest(writeManifest(t, "version: 1\n"))
	if err != nil || absent.DeclaredVerification() != nil || strings.Contains(mustRender(t, absent), "verification") {
		t.Fatalf("absent section = %+v, %v; want none, and none rendered", absent, err)
	}
	var none *Manifest
	if none.DeclaredVerification() != nil {
		t.Fatal("a nil manifest declares a verification section")
	}
}

// TestVerificationPolicy_Negative: an unknown or repeated key, a non-integer value and a value
// outside 1..its ceiling are refused naming the key, by DecodeManifest too, which skips the policy
// validations, so no reader walks under a bound the flags would refuse.
func TestVerificationPolicy_Negative(t *testing.T) {
	cases := map[string]struct{ section, want string }{
		"unknown key":      {"max_entires: 10", `unknown or duplicated verification field "max_entires"`},
		"repeated key":     {"max_depth: 10\n  max_depth: 11", `unknown or duplicated verification field "max_depth"`},
		"string value":     {`max_files: "many"`, "verification max_files must be an integer"},
		"zero entries":     {"max_entries: 0", "verification.max_entries must be an integer from 1 to 200000; got 0"},
		"negative depth":   {"max_depth: -1", "verification.max_depth must be an integer from 1 to 64; got -1"},
		"entries ceiling+": {fmt.Sprintf("max_entries: %d", util.DiscoveryEntriesCeiling+1), "verification.max_entries must be an integer from 1 to 200000"},
		"files ceiling+":   {fmt.Sprintf("max_files: %d", util.DiscoveryFilesCeiling+1), "verification.max_files must be an integer from 1 to 512"},
	}
	for name, tc := range cases {
		data := "version: 1\nverification:\n  " + tc.section + "\n"
		if _, err := LoadManifest(writeManifest(t, data)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: LoadManifest = %v; want %q", name, err, tc.want)
		}
		if _, err := DecodeManifest([]byte(data)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: DecodeManifest = %v; want %q", name, err, tc.want)
		}
	}
	if _, err := DecodeManifest([]byte("verification: [1]\n")); err == nil || !strings.Contains(err.Error(), "verification must be a mapping") {
		t.Fatalf("a non-mapping section = %v; want a refusal", err)
	}
}

// TestVerificationPolicy_Boundary: 1 and each ceiling itself are accepted, the ceilings are the
// ones the flags are held to, and an empty section declares no bound.
func TestVerificationPolicy_Boundary(t *testing.T) {
	data := fmt.Sprintf("version: 1\nverification:\n  max_entries: %d\n  max_files: %d\n  max_depth: 1\n",
		util.DiscoveryEntriesCeiling, util.DiscoveryFilesCeiling)
	m, err := LoadManifest(writeManifest(t, data))
	if err != nil {
		t.Fatal(err)
	}
	want := VerificationPolicy{MaxEntries: util.DiscoveryEntriesCeiling, MaxFiles: util.DiscoveryFilesCeiling, MaxDepth: 1}
	if *m.Verification != want {
		t.Fatalf("ceilings = %+v; want %+v", *m.Verification, want)
	}
	if _, err := DecodeManifest(fmt.Appendf(nil, "verification:\n  max_depth: %d\n", util.DiscoveryDepthCeiling)); err != nil {
		t.Fatalf("the depth ceiling itself is refused: %v", err)
	}
	empty, err := DecodeManifest([]byte("verification: {}\n"))
	if err != nil || empty.Verification == nil || *empty.Verification != (VerificationPolicy{}) {
		t.Fatalf("empty section = %+v, %v; want a section declaring no bound", empty, err)
	}
}

// mustRender is RenderManifest's text of m.
func mustRender(t *testing.T, m *Manifest) string {
	t.Helper()
	data, err := RenderManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
