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

func TestReleasePolicy_Positive(t *testing.T) {
	manifestYAML := `version: 1
repository:
  owner: acme
  name: service
profiles:
  - app-service
facets: []
release:
  e2e:
    paths:
      - e2e/**
      - "**/*_e2e_test.go"
    tests:
      - login-flow
      - checkout:smoke/v1
    since: a1b2c3d4e5f60123456789abcdef0123456789ab
`
	manifest, err := ParseManifest(".standards.yaml", []byte(manifestYAML))
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}
	if manifest.Release == nil || manifest.Release.E2E == nil {
		t.Fatalf("expected release.e2e policy to be parsed")
	}
	got := manifest.Release.E2E
	wantPaths := []string{"e2e/**", "**/*_e2e_test.go"}
	if !slices.Equal(got.EffectivePaths(), wantPaths) {
		t.Fatalf("EffectivePaths = %v, want %v", got.EffectivePaths(), wantPaths)
	}
	wantTests := []string{"login-flow", "checkout:smoke/v1"}
	if !slices.Equal(got.Tests, wantTests) {
		t.Fatalf("Tests = %v, want %v", got.Tests, wantTests)
	}
	if got.Since != "a1b2c3d4e5f60123456789abcdef0123456789ab" {
		t.Fatalf("Since = %q", got.Since)
	}
}

func TestReleasePolicy_DefaultPathsBoundary(t *testing.T) {
	var nilPolicy *ReleaseE2EPolicy
	want := []string{
		"e2e/**",
		"tests/e2e/**",
		"test/e2e/**",
		"**/*_e2e_test.go",
		"**/*.e2e.ts",
		"**/*.e2e.js",
		"playwright/**",
		"cypress/**",
	}
	if !slices.Equal(nilPolicy.EffectivePaths(), want) {
		t.Fatalf("nil EffectivePaths = %v, want %v", nilPolicy.EffectivePaths(), want)
	}
	emptyPolicy := &ReleaseE2EPolicy{}
	if !slices.Equal(emptyPolicy.EffectivePaths(), want) {
		t.Fatalf("empty EffectivePaths = %v, want %v", emptyPolicy.EffectivePaths(), want)
	}
	shortSince := &ReleasePolicy{E2E: &ReleaseE2EPolicy{Since: "0123456"}}
	if err := shortSince.Validate(); err != nil {
		t.Fatalf("expected 7-char hex SHA to be valid, got: %v", err)
	}
}

func TestReleasePolicy_Negative(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unknown release key",
			yaml: "version: 1\nrelease:\n  unknown: true\n",
			want: "unknown key",
		},
		{
			name: "unknown release.e2e key",
			yaml: "version: 1\nrelease:\n  e2e:\n    extra: []\n",
			want: "unknown key",
		},
		{
			name: "duplicate path glob",
			yaml: "version: 1\nrelease:\n  e2e:\n    paths:\n      - e2e/**\n      - e2e/**\n",
			want: "duplicates release.e2e.paths[0]",
		},
		{
			name: "invalid path glob parent traversal",
			yaml: "version: 1\nrelease:\n  e2e:\n    paths:\n      - ../e2e/**\n",
			want: "release.e2e.paths[0]",
		},
		{
			name: "duplicate test id",
			yaml: "version: 1\nrelease:\n  e2e:\n    tests:\n      - login-flow\n      - login-flow\n",
			want: "duplicates release.e2e.tests[0]",
		},
		{
			name: "invalid test id characters",
			yaml: "version: 1\nrelease:\n  e2e:\n    tests:\n      - \"login flow\"\n",
			want: "release.e2e.tests[0]",
		},
		{
			name: "since too short",
			yaml: "version: 1\nrelease:\n  e2e:\n    since: abcdef\n",
			want: "release.e2e.since",
		},
		{
			name: "since non-hex",
			yaml: "version: 1\nrelease:\n  e2e:\n    since: 012345g\n",
			want: "hexadecimal",
		},
	}
	for i := 0; i < len(cases) && i < 16; i++ {
		tc := cases[i]
		_, err := ParseManifest(".standards.yaml", []byte(tc.yaml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got err=%v, want substring %q", tc.name, err, tc.want)
		}
	}
}

func TestReleasePolicy_MaxBounds(t *testing.T) {
	tooManyTests := make([]string, MaxReleaseE2ETests+1)
	for i := 0; i < len(tooManyTests) && i <= MaxReleaseE2ETests; i++ {
		tooManyTests[i] = fmt.Sprintf("test-%d", i)
	}
	policy := &ReleasePolicy{E2E: &ReleaseE2EPolicy{Tests: tooManyTests}}
	if err := policy.Validate(); err == nil || !strings.Contains(err.Error(), "release.e2e.tests") {
		t.Fatalf("expected max tests error, got %v", err)
	}
}

func TestReleaseTestLevelsPolicy_Positive(t *testing.T) {
	manifestYAML := `version: 1
repository:
  owner: acme
  name: client
profiles:
  - library-client
facets: []
release:
  test_levels:
    unit:
      paths:
        - "**/*_test.go"
      run: "go test"
    integration:
      paths:
        - "**/*_integration_test.go"
      pattern: "func TestIntegration"
      job: "integration"
      run: "go test -tags=integration"
    chaos:
      paths:
        - "chaos/**"
      job: "chaos-suite"
`
	manifest, err := ParseManifest(".standards.yaml", []byte(manifestYAML))
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}
	if manifest.Release == nil || len(manifest.Release.TestLevels) != 3 {
		t.Fatalf("expected 3 release.test_levels entries, got %+v", manifest.Release)
	}
	integ := manifest.Release.TestLevels["integration"]
	if integ.Pattern != "func TestIntegration" || integ.Job != "integration" || integ.Run != "go test -tags=integration" {
		t.Fatalf("unexpected integration spec: %+v", integ)
	}
}

func TestReleaseTestLevelsPolicy_Negative_DropRequiredLevelFailsValidation(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "omits required integration level for library-client",
			yaml: "version: 1\nprofiles:\n  - library-client\nrelease:\n  test_levels:\n    unit:\n      paths:\n        - \"**/*_test.go\"\n",
			want: "cannot drop required test level \"integration\" for profile \"library-client\"",
		},
		{
			name: "nulls required integration level",
			yaml: "version: 1\nprofiles:\n  - library-client\nrelease:\n  test_levels:\n    unit:\n      paths:\n        - \"**/*_test.go\"\n    integration: null\n",
			want: "cannot drop a required test level",
		},
		{
			name: "empty paths on level",
			yaml: "version: 1\nprofiles:\n  - library-client\nrelease:\n  test_levels:\n    unit:\n      paths:\n        - \"**/*_test.go\"\n    integration:\n      paths: []\n",
			want: "must declare at least one path glob",
		},
		{
			name: "unknown key in level spec",
			yaml: "version: 1\nrelease:\n  test_levels:\n    unit:\n      paths:\n        - \"**/*_test.go\"\n      skip: true\n",
			want: "unknown key",
		},
		{
			name: "invalid regex in pattern",
			yaml: "version: 1\nrelease:\n  test_levels:\n    unit:\n      paths:\n        - \"**/*_test.go\"\n      pattern: \"(\"\n",
			want: "invalid regular expression",
		},
	}
	for i := 0; i < len(cases) && i < 16; i++ {
		tc := cases[i]
		_, err := ParseManifest(".standards.yaml", []byte(tc.yaml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got err=%v, want substring %q", tc.name, err, tc.want)
		}
	}
}

func TestReleaseTestLevelsPolicy_Boundary_ArchetypeDefaults(t *testing.T) {
	byProfile, err := ProfileReleaseTestLevels(t.Context(), "", []string{
		"app-service", "library-client", "web-package", "native-gpu-systems",
		"container-image", "gitops-infra", "os-image", "framework", "org-health",
	})
	if err != nil {
		t.Fatalf("ProfileReleaseTestLevels: %v", err)
	}
	if !slices.Equal(byProfile["app-service"], []string{"unit", "integration", "e2e", "load_soak", "recovery_restore"}) {
		t.Fatalf("app-service = %v", byProfile["app-service"])
	}
	if !slices.Equal(byProfile["library-client"], []string{"unit", "integration"}) {
		t.Fatalf("library-client = %v", byProfile["library-client"])
	}
	if !slices.Equal(byProfile["web-package"], []string{"unit", "e2e", "a11y"}) {
		t.Fatalf("web-package = %v", byProfile["web-package"])
	}
	if !slices.Equal(byProfile["native-gpu-systems"], []string{"unit", "integration"}) {
		t.Fatalf("native-gpu-systems = %v", byProfile["native-gpu-systems"])
	}
	for _, smokeProfile := range []string{"container-image", "gitops-infra", "os-image"} {
		if !slices.Equal(byProfile[smokeProfile], []string{"smoke"}) {
			t.Fatalf("%s = %v, want [smoke]", smokeProfile, byProfile[smokeProfile])
		}
	}
	if len(byProfile["framework"]) != 0 || len(byProfile["org-health"]) != 0 {
		t.Fatalf("expected none for framework and org-health, got %v", byProfile)
	}
}
