// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
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

// TestUIPolicy_Positive verifies that a valid ui section decodes input_latency_ms, routes, and
// api_pattern, and defaults routes to ["/"] and api_pattern to "**/api/**" when omitted
// (order engine-fr-rule-checks-20261027).
func TestUIPolicy_Positive(t *testing.T) {
	m := mustParseUIManifest(t, "ui:\n  input_latency_ms: 100\n  api_pattern: \"**/v1/**\"\n  routes:\n    - /\n    - /settings\n")
	got := m.DeclaredUI()
	if got == nil || got.InputLatencyMs != 100 || got.APIPattern != "**/v1/**" || !slices.Equal(got.Routes, []string{"/", "/settings"}) {
		t.Fatalf("DeclaredUI() = %+v, want InputLatencyMs=100 APIPattern=**/v1/** Routes=[/ /settings]", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
	if m.UIAPIPattern() != "**/v1/**" {
		t.Fatalf("UIAPIPattern() = %q, want %q", m.UIAPIPattern(), "**/v1/**")
	}

	defaultRoutes := mustParseUIManifest(t, "ui:\n  input_latency_ms: 50\n")
	if !slices.Equal(defaultRoutes.UIRoutes(), []string{"/"}) {
		t.Fatalf("UIRoutes() = %v, want [/]", defaultRoutes.UIRoutes())
	}
	if defaultRoutes.UIAPIPattern() != DefaultUIAPIPattern {
		t.Fatalf("UIAPIPattern() = %q, want %q", defaultRoutes.UIAPIPattern(), DefaultUIAPIPattern)
	}
}

// TestUIPolicy_Negative verifies that input_latency_ms at 0, negative, or above bound,
// routes above MaxUIRoutes or invalid, and invalid or overlong api_pattern values fail
// validation (order engine-fr-rule-checks-20261027).
func TestUIPolicy_Negative(t *testing.T) {
	var manyRoutes strings.Builder
	manyRoutes.WriteString("ui:\n  input_latency_ms: 100\n  routes:\n")
	for i := 0; i <= MaxUIRoutes; i++ {
		fmt.Fprintf(&manyRoutes, "    - /route-%d\n", i)
	}

	cases := map[string]struct{ body, want string }{
		"latency zero":                {body: "ui:\n  input_latency_ms: 0\n", want: "ui.input_latency_ms must be an integer from 1 to 10000; got 0"},
		"latency negative":            {body: "ui:\n  input_latency_ms: -5\n", want: "ui.input_latency_ms must be an integer from 1 to 10000; got -5"},
		"latency above bound":         {body: "ui:\n  input_latency_ms: 10001\n", want: "ui.input_latency_ms must be an integer from 1 to 10000; got 10001"},
		"routes above bound":          {body: manyRoutes.String(), want: "ui.routes has 65 entries; maximum is 64"},
		"route missing leading slash": {body: "ui:\n  input_latency_ms: 100\n  routes:\n    - settings\n", want: "must start with '/'"},
		"duplicate route":             {body: "ui:\n  input_latency_ms: 100\n  routes:\n    - /\n    - /\n", want: "repeats \"/\""},
		"api_pattern empty":           {body: "ui:\n  input_latency_ms: 100\n  api_pattern: \"\"\n", want: "ui.api_pattern must not be empty"},
		"api_pattern overlong":        {body: "ui:\n  input_latency_ms: 100\n  api_pattern: \"/" + strings.Repeat("a", MaxUIAPIPatternBytes) + "\"\n", want: "ui.api_pattern exceeds 256 bytes"},
		"api_pattern whitespace":      {body: "ui:\n  input_latency_ms: 100\n  api_pattern: \"**/api/** /bad\"\n", want: "must not contain whitespace"},
		"unknown key":                 {body: "ui:\n  input_latency_ms: 100\n  extra: 1\n", want: "unknown or duplicated ui field \"extra\""},
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
// (10000), MaxUIRoutes (64), and MaxUIAPIPatternBytes (256) (order engine-fr-rule-checks-20261027).
func TestUIPolicy_Boundary(t *testing.T) {
	low := mustParseUIManifest(t, "ui:\n  input_latency_ms: 1\n")
	if low.UI == nil || low.UI.InputLatencyMs != MinUIInputLatencyMs {
		t.Fatalf("min boundary failed: %+v", low.UI)
	}

	exactPattern := "/" + strings.Repeat("p", MaxUIAPIPatternBytes-1)
	var maxRoutes strings.Builder
	fmt.Fprintf(&maxRoutes, "ui:\n  input_latency_ms: 10000\n  api_pattern: %q\n  routes:\n", exactPattern)
	for i := 0; i < MaxUIRoutes; i++ {
		fmt.Fprintf(&maxRoutes, "    - /r-%d\n", i)
	}
	high := mustParseUIManifest(t, maxRoutes.String())
	if high.UI == nil || high.UI.InputLatencyMs != MaxUIInputLatencyMs || len(high.UI.Routes) != MaxUIRoutes || len(high.UI.APIPattern) != MaxUIAPIPatternBytes {
		t.Fatalf("max boundary failed: %+v", high.UI)
	}
}

func TestUIScreenMatrixPolicy(t *testing.T) {
	cases := []struct {
		name, value string
		valid       bool
	}{
		{"positive", `"tests/screen-matrix.json"`, true},
		{"absolute", `"/matrix.json"`, false},
		{"escape", `"../matrix.json"`, false},
		{"nested escape", `"tests/../../matrix.json"`, false},
		{"windows absolute", `"C:/matrix.json"`, false},
		{"empty", `""`, false},
		{"not string", "12", false},
		{"null", "null", false},
		{"bound", fmt.Sprintf("%q", strings.Repeat("a", maxRepositoryPath)), true},
		{"overlong", fmt.Sprintf("%q", strings.Repeat("a", maxRepositoryPath+1)), false},
		{"duplicate", "a.json\n  screen_matrix: b.json", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest("manifest.yaml", []byte("ui:\n  screen_matrix: "+tc.value+"\n"))
			if (err == nil) != tc.valid {
				t.Fatalf("screen_matrix valid=%v, error=%v", tc.valid, err)
			}
			if tc.valid && m.DeclaredUI().ScreenMatrix != m.UI.ScreenMatrix {
				t.Fatal("DeclaredUI lost screen_matrix")
			}
		})
	}
}
