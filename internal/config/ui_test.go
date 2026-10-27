// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func mustParseUIManifest(t *testing.T, body string) *Manifest {
	t.Helper()
	m, err := ParseManifest("manifest.yaml", []byte(body))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return m
}

// TestUIPolicy_Positive verifies that a valid ui section decodes input_latency_ms and routes,
// and defaults routes to ["/"] when omitted (order engine-fr-rule-checks-20261027).
func TestUIPolicy_Positive(t *testing.T) {
	m := mustParseUIManifest(t, "ui:\n  input_latency_ms: 100\n  routes:\n    - /\n    - /settings\n")
	got := m.DeclaredUI()
	if got == nil || got.InputLatencyMs != 100 || !slices.Equal(got.Routes, []string{"/", "/settings"}) {
		t.Fatalf("DeclaredUI() = %+v, want InputLatencyMs=100 Routes=[/ /settings]", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}

	defaultRoutes := mustParseUIManifest(t, "ui:\n  input_latency_ms: 50\n")
	if !slices.Equal(defaultRoutes.UIRoutes(), []string{"/"}) {
		t.Fatalf("UIRoutes() = %v, want [/]", defaultRoutes.UIRoutes())
	}
}

// TestUIPolicy_Negative verifies that input_latency_ms at 0, negative, or above bound, and
// routes above MaxUIRoutes or invalid, fail validation (order engine-fr-rule-checks-20261027).
func TestUIPolicy_Negative(t *testing.T) {
	var manyRoutes strings.Builder
	manyRoutes.WriteString("ui:\n  input_latency_ms: 100\n  routes:\n")
	for i := 0; i <= MaxUIRoutes; i++ {
		fmt.Fprintf(&manyRoutes, "    - /route-%d\n", i)
	}

	cases := map[string]struct {
		body string
		want string
	}{
		"latency zero": {
			body: "ui:\n  input_latency_ms: 0\n",
			want: "ui.input_latency_ms must be an integer from 1 to 10000; got 0",
		},
		"latency negative": {
			body: "ui:\n  input_latency_ms: -5\n",
			want: "ui.input_latency_ms must be an integer from 1 to 10000; got -5",
		},
		"latency above bound": {
			body: "ui:\n  input_latency_ms: 10001\n",
			want: "ui.input_latency_ms must be an integer from 1 to 10000; got 10001",
		},
		"routes above bound": {
			body: manyRoutes.String(),
			want: "ui.routes has 65 entries; maximum is 64",
		},
		"route missing leading slash": {
			body: "ui:\n  input_latency_ms: 100\n  routes:\n    - settings\n",
			want: "must start with '/'",
		},
		"duplicate route": {
			body: "ui:\n  input_latency_ms: 100\n  routes:\n    - /\n    - /\n",
			want: "repeats \"/\"",
		},
		"unknown key": {
			body: "ui:\n  input_latency_ms: 100\n  extra: 1\n",
			want: "unknown or duplicated ui field \"extra\"",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseManifest("manifest.yaml", []byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseManifest(%s) error = %v, want substring %q", name, err, tc.want)
			}
		})
	}
}

// TestUIPolicy_Boundary verifies the exact bounds MinUIInputLatencyMs (1), MaxUIInputLatencyMs
// (10000), and MaxUIRoutes (64) (order engine-fr-rule-checks-20261027).
func TestUIPolicy_Boundary(t *testing.T) {
	low := mustParseUIManifest(t, "ui:\n  input_latency_ms: 1\n")
	if low.UI == nil || low.UI.InputLatencyMs != MinUIInputLatencyMs {
		t.Fatalf("min boundary failed: %+v", low.UI)
	}

	var maxRoutes strings.Builder
	maxRoutes.WriteString("ui:\n  input_latency_ms: 10000\n  routes:\n")
	for i := 0; i < MaxUIRoutes; i++ {
		fmt.Fprintf(&maxRoutes, "    - /r-%d\n", i)
	}
	high := mustParseUIManifest(t, maxRoutes.String())
	if high.UI == nil || high.UI.InputLatencyMs != MaxUIInputLatencyMs || len(high.UI.Routes) != MaxUIRoutes {
		t.Fatalf("max boundary failed: %+v", high.UI)
	}
}
