// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"strings"
	"testing"
	"time"
)

func TestGatePolicy_Constant_MaxDeclaredVerifyTimeout(t *testing.T) {
	if got := MaxDeclaredVerifyTimeout; got != 30*time.Minute {
		t.Fatalf("MaxDeclaredVerifyTimeout = %v, want 30m", got)
	}
}

func TestGatePolicy_Positive_DeclaredVerifyTimeout(t *testing.T) {
	manifest, err := ParseManifest("test", []byte(`version: 1
gate:
  declared_verify_timeout: 20m
`))
	if err != nil {
		t.Fatalf("expected valid manifest, got: %v", err)
	}
	if manifest.Gate == nil {
		t.Fatal("expected manifest.Gate to be populated")
	}
	if got := manifest.Gate.DeclaredVerifyTimeoutDuration(); got != 20*time.Minute {
		t.Errorf("DeclaredVerifyTimeoutDuration() = %v, want 20m", got)
	}
}

func TestGatePolicy_Negative_DeclaredVerifyTimeoutRefused(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		wantErr  string
		wantSubs []string
	}{
		{
			name: "above ceiling 31m",
			yaml: `version: 1
gate:
  declared_verify_timeout: 31m
`,
			wantSubs: []string{"gate.declared_verify_timeout", "31m", "ceiling", "30m0s"},
		},
		{
			name: "not positive 0s",
			yaml: `version: 1
gate:
  declared_verify_timeout: 0s
`,
			wantSubs: []string{"gate.declared_verify_timeout", "0s", "ceiling", "30m0s"},
		},
		{
			name: "not positive negative duration",
			yaml: `version: 1
gate:
  declared_verify_timeout: -5m
`,
			wantSubs: []string{"gate.declared_verify_timeout", "-5m", "ceiling", "30m0s"},
		},
		{
			name: "invalid duration string",
			yaml: `version: 1
gate:
  declared_verify_timeout: not-a-duration
`,
			wantSubs: []string{"gate.declared_verify_timeout", "not-a-duration", "ceiling"},
		},
		{
			name: "unknown key",
			yaml: `version: 1
gate:
  unknown_key: 10m
`,
			wantSubs: []string{"gate", "unknown_key"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest("test", []byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			msg := err.Error()
			for _, sub := range tc.wantSubs {
				if !strings.Contains(msg, sub) {
					t.Errorf("error %q does not contain expected substring %q", msg, sub)
				}
			}
		})
	}
}

func TestGatePolicy_Boundary_CeilingAndOmitted(t *testing.T) {
	exactCeiling, err := ParseManifest("test", []byte(`version: 1
gate:
  declared_verify_timeout: 30m
`))
	if err != nil {
		t.Fatalf("exact ceiling 30m must be accepted: %v", err)
	}
	if got := exactCeiling.Gate.DeclaredVerifyTimeoutDuration(); got != 30*time.Minute {
		t.Errorf("DeclaredVerifyTimeoutDuration() = %v, want 30m", got)
	}

	oneNano, err := ParseManifest("test", []byte(`version: 1
gate:
  declared_verify_timeout: 1ns
`))
	if err != nil {
		t.Fatalf("smallest positive 1ns must be accepted: %v", err)
	}
	if got := oneNano.Gate.DeclaredVerifyTimeoutDuration(); got != time.Nanosecond {
		t.Errorf("DeclaredVerifyTimeoutDuration() = %v, want 1ns", got)
	}

	omitted, err := ParseManifest("test", []byte(`version: 1`))
	if err != nil {
		t.Fatalf("omitted gate section must be accepted: %v", err)
	}
	if got := omitted.Gate.DeclaredVerifyTimeoutDuration(); got != 0 {
		t.Errorf("omitted gate section duration = %v, want 0", got)
	}
}
