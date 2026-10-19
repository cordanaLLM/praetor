// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package needs

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

const depguardFrameworkModule = "github.com/fw"

const depguardContract = `version: 1
framework: github.com/fw
packages:
  - import: github.com/fw/yaml
    domain: yaml
    capabilities: [encoding.yaml]
    replaces: [go.yaml.in/yaml/v3]
`

func setupDepguardFramework(t *testing.T) *FrameworkIndex {
	t.Helper()
	root := setupFrameworkCheckout(t, depguardFrameworkModule, "yaml")
	writeFixture(t, root, "yaml/yaml.go", "package yaml\n\nfunc Unmarshal() {}\n")
	writeFixture(t, root, FrameworkContractFile, depguardContract)
	index, err := InspectFramework(t.Context(), acmeSource(root))
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func setupDepguardConsumer(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFixture(t, repo, "go.mod", "module example.com/consumer\n\ngo 1.27\n\nrequire go.yaml.in/yaml/v3 v3.0.1\n")
	writeFixture(t, repo, "main.go", "package main\n\nimport _ \"go.yaml.in/yaml/v3\"\n\nfunc main() {}\n")
	return repo
}

// TestDepguardDeclinedByPolicy_DenyForm verifies that a proposed replacement matching a
// depguard deny prefix is reported as declined-by-policy, naming the rule and denied prefix,
// and not counted as a readiness gap.
func TestDepguardDeclinedByPolicy_DenyForm(t *testing.T) {
	index := setupDepguardFramework(t)
	consumer := setupDepguardConsumer(t)
	golangciYaml := `linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          deny:
            - pkg: "github.com/"
              desc: "no github packages"
`
	writeFixture(t, consumer, ".golangci.yml", golangciYaml)

	report, err := ScanRepoWithFramework(t.Context(), consumer, index, nil)
	if err != nil {
		t.Fatal(err)
	}
	dep := demandFor(t, report, "go.yaml.in/yaml/v3")
	if dep.Status != StatusDeclinedByPolicy {
		t.Errorf("got status %q, want %q", dep.Status, StatusDeclinedByPolicy)
	}
	if dep.FrameworkReplacement != "github.com/fw/yaml" {
		t.Errorf("got replacement %q, want %q", dep.FrameworkReplacement, "github.com/fw/yaml")
	}
	if !strings.Contains(dep.Notes, `"main"`) || !strings.Contains(dep.Notes, "github.com/") {
		t.Errorf("dep.Notes %q does not name rule 'main' and denied prefix 'github.com/'", dep.Notes)
	}
	if report.Readiness.GapDeps != 0 {
		t.Errorf("got GapDeps = %d, want 0", report.Readiness.GapDeps)
	}

	formatted := FormatLibraryRelationships(report)
	if !strings.Contains(formatted, "○ go.yaml.in/yaml/v3") {
		t.Errorf("formatted library relationships does not show ○ marker:\n%s", formatted)
	}
	if !strings.Contains(formatted, "declined by policy: github.com/fw/yaml") {
		t.Errorf("formatted library relationships does not show declined by policy:\n%s", formatted)
	}
}

// TestDepguardDeclinedByPolicy_StrictForm verifies that list-mode: strict with allow: [$gostd]
// declines any non-standard library replacement candidate.
func TestDepguardDeclinedByPolicy_StrictForm(t *testing.T) {
	index := setupDepguardFramework(t)
	consumer := setupDepguardConsumer(t)
	golangciYaml := `linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        strict_stdlib:
          list-mode: strict
          allow:
            - "$gostd"
`
	writeFixture(t, consumer, ".golangci.yml", golangciYaml)

	report, err := ScanRepoWithFramework(t.Context(), consumer, index, nil)
	if err != nil {
		t.Fatal(err)
	}
	dep := demandFor(t, report, "go.yaml.in/yaml/v3")
	if dep.Status != StatusDeclinedByPolicy {
		t.Errorf("got status %q, want %q", dep.Status, StatusDeclinedByPolicy)
	}
	if !strings.Contains(dep.Notes, `"strict_stdlib"`) {
		t.Errorf("dep.Notes %q does not name rule 'strict_stdlib'", dep.Notes)
	}
	if report.Readiness.GapDeps != 0 {
		t.Errorf("got GapDeps = %d, want 0", report.Readiness.GapDeps)
	}
}

// TestDepguardDeclinedByPolicy_Negative_WithoutDepguard verifies that without depguard
// the row stays a normal covered replacement candidate.
func TestDepguardDeclinedByPolicy_Negative_WithoutDepguard(t *testing.T) {
	index := setupDepguardFramework(t)
	consumer := setupDepguardConsumer(t)

	report, err := ScanRepoWithFramework(t.Context(), consumer, index, nil)
	if err != nil {
		t.Fatal(err)
	}
	dep := demandFor(t, report, "go.yaml.in/yaml/v3")
	if dep.Status != StatusCovered {
		t.Errorf("got status %q, want %q", dep.Status, StatusCovered)
	}
	if dep.FrameworkReplacement != "github.com/fw/yaml" {
		t.Errorf("got replacement %q, want %q", dep.FrameworkReplacement, "github.com/fw/yaml")
	}
	if report.Readiness.CoveredDeps != 1 || report.Readiness.GapDeps != 0 {
		t.Errorf("readiness = %+v, want 1 covered, 0 gap", report.Readiness)
	}
}

// TestDepguardDeclinedByPolicy_Boundary_FilesExcludesModule verifies that a deny rule
// whose files pattern excludes the module's files does not decline the replacement candidate.
func TestDepguardDeclinedByPolicy_Boundary_FilesExcludesModule(t *testing.T) {
	index := setupDepguardFramework(t)
	consumer := setupDepguardConsumer(t)
	golangciYaml := `linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          files:
            - "!**/*.go"
          deny:
            - pkg: "github.com/"
`
	writeFixture(t, consumer, ".golangci.yml", golangciYaml)

	report, err := ScanRepoWithFramework(t.Context(), consumer, index, nil)
	if err != nil {
		t.Fatal(err)
	}
	dep := demandFor(t, report, "go.yaml.in/yaml/v3")
	if dep.Status != StatusCovered {
		t.Errorf("got status %q, want %q (rule files excluded module files)", dep.Status, StatusCovered)
	}
}

type depguardDecisionTestCase struct {
	name       string
	allow      []string
	deny       []string
	pkg        string
	wantDeny   bool
	wantPrefix string
}

func runDecisionTestTable(t *testing.T, mode string, cases []depguardDecisionTestCase) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := config.DepguardRuleConfig{
				ListMode: mode,
				Allow:    tc.allow,
			}
			for _, d := range tc.deny {
				rule.Deny = append(rule.Deny, config.DepguardDenyEntry{Pkg: d})
			}
			declined, entry := checkRuleDecision(rule, tc.pkg)
			if declined != tc.wantDeny || entry != tc.wantPrefix {
				t.Fatalf("checkRuleDecision = (%v, %q), want (%v, %q)", declined, entry, tc.wantDeny, tc.wantPrefix)
			}
		})
	}
}

func TestDepguardDecision_OriginalMode_3D(t *testing.T) {
	runDecisionTestTable(t, "original", originalModeTestCases())
}

func originalModeTestCases() []depguardDecisionTestCase {
	return []depguardDecisionTestCase{
		{
			name:       "positive: deny matches prefix",
			allow:      nil,
			deny:       []string{"github.com/bad"},
			pkg:        "github.com/bad/pkg",
			wantDeny:   true,
			wantPrefix: "github.com/bad",
		},
		{
			name:       "negative: empty allow and no deny matches",
			allow:      nil,
			deny:       []string{"github.com/bad"},
			pkg:        "github.com/good/pkg",
			wantDeny:   false,
			wantPrefix: "",
		},
		{
			name:       "negative: allow matches and no deny matches",
			allow:      []string{"github.com/good"},
			deny:       []string{"github.com/bad"},
			pkg:        "github.com/good/pkg",
			wantDeny:   false,
			wantPrefix: "",
		},
		{
			name:       "boundary: not in allow list when allow not empty",
			allow:      []string{"github.com/other"},
			deny:       nil,
			pkg:        "github.com/good/pkg",
			wantDeny:   true,
			wantPrefix: "",
		},
		{
			name:       "boundary: deny match overrides longer allow in original",
			allow:      []string{"github.com/fw/pkg"},
			deny:       []string{"github.com/fw"},
			pkg:        "github.com/fw/pkg",
			wantDeny:   true,
			wantPrefix: "github.com/fw",
		},
	}
}

func TestDepguardDecision_StrictMode_3D(t *testing.T) {
	runDecisionTestTable(t, "strict", strictModeTestCases())
}

func strictModeTestCases() []depguardDecisionTestCase {
	return []depguardDecisionTestCase{
		{
			name:       "positive: not in allow list",
			allow:      []string{"example.com/other"},
			deny:       nil,
			pkg:        "example.com/target",
			wantDeny:   true,
			wantPrefix: "",
		},
		{
			name:       "negative: allow matches and no deny matches",
			allow:      []string{"example.com/target"},
			deny:       nil,
			pkg:        "example.com/target/sub",
			wantDeny:   false,
			wantPrefix: "",
		},
		{
			name:       "boundary: allow longer than deny decides allow",
			allow:      []string{"example.com/target/sub"},
			deny:       []string{"example.com/target"},
			pkg:        "example.com/target/sub",
			wantDeny:   false,
			wantPrefix: "",
		},
		{
			name:       "boundary: deny longer than allow decides deny",
			allow:      []string{"example.com/target"},
			deny:       []string{"example.com/target/sub"},
			pkg:        "example.com/target/sub",
			wantDeny:   true,
			wantPrefix: "example.com/target/sub",
		},
		{
			name:       "boundary: equal length allow and deny decides deny",
			allow:      []string{"example.com/target"},
			deny:       []string{"example.com/target"},
			pkg:        "example.com/target",
			wantDeny:   true,
			wantPrefix: "example.com/target",
		},
	}
}

func TestDepguardDecision_LaxMode_3D(t *testing.T) {
	runDecisionTestTable(t, "lax", laxModeTestCases())
}

func laxModeTestCases() []depguardDecisionTestCase {
	return []depguardDecisionTestCase{
		{
			name:       "positive: deny matches and no allow matches",
			allow:      nil,
			deny:       []string{"example.com/target"},
			pkg:        "example.com/target/sub",
			wantDeny:   true,
			wantPrefix: "example.com/target",
		},
		{
			name:       "negative: neither matches is allowed in lax",
			allow:      []string{"example.com/other"},
			deny:       []string{"example.com/bad"},
			pkg:        "example.com/target",
			wantDeny:   false,
			wantPrefix: "",
		},
		{
			name:       "boundary: allow longer than deny decides allow",
			allow:      []string{"example.com/target/sub"},
			deny:       []string{"example.com/target"},
			pkg:        "example.com/target/sub",
			wantDeny:   false,
			wantPrefix: "",
		},
		{
			name:       "boundary: deny longer than allow decides deny",
			allow:      []string{"example.com/target"},
			deny:       []string{"example.com/target/sub"},
			pkg:        "example.com/target/sub",
			wantDeny:   true,
			wantPrefix: "example.com/target/sub",
		},
	}
}

type packageMatchingCase struct {
	name       string
	entry      string
	pkg        string
	wantMatch  bool
	wantLength int
}

func TestDepguardDecision_PackageMatching_3D(t *testing.T) {
	for _, tc := range packageMatchingCases() {
		t.Run(tc.name, func(t *testing.T) {
			matched, mlen := matchPackageEntry(tc.entry, tc.pkg)
			if matched != tc.wantMatch || mlen != tc.wantLength {
				t.Fatalf("matchPackageEntry(%q, %q) = (%v, %d), want (%v, %d)",
					tc.entry, tc.pkg, matched, mlen, tc.wantMatch, tc.wantLength)
			}
		})
	}
}

func packageMatchingCases() []packageMatchingCase {
	return []packageMatchingCase{
		{
			name:       "exact match with dollar",
			entry:      "example.com/foo$",
			pkg:        "example.com/foo",
			wantMatch:  true,
			wantLength: 16,
		},
		{
			name:       "exact match with dollar rejects child",
			entry:      "example.com/foo$",
			pkg:        "example.com/foo/bar",
			wantMatch:  false,
			wantLength: 0,
		},
		{
			name:       "prefix match allows child",
			entry:      "example.com/foo",
			pkg:        "example.com/foo/bar",
			wantMatch:  true,
			wantLength: 15,
		},
		{
			name:       "gostd matches stdlib",
			entry:      "$gostd",
			pkg:        "os/exec",
			wantMatch:  true,
			wantLength: 2,
		},
		{
			name:       "gostd rejects third-party",
			entry:      "$gostd",
			pkg:        "example.com/foo",
			wantMatch:  false,
			wantLength: 0,
		},
		{
			name:       "entry without dot or slash rejects import with dot",
			entry:      "os",
			pkg:        "os.example.com/foo",
			wantMatch:  false,
			wantLength: 0,
		},
		{
			name:       "entry without dot or slash matches stdlib prefix",
			entry:      "os",
			pkg:        "os/exec",
			wantMatch:  true,
			wantLength: 2,
		},
	}
}

func TestDepguardEnablement_3D(t *testing.T) {
	tests := []struct {
		name        string
		enable      []string
		defaultMode string
		disable     []string
		wantEnabled bool
	}{
		{"positive: linters.enable lists depguard", []string{"depguard"}, "", nil, true},
		{"positive: default all without disable", nil, "all", []string{"errcheck"}, true},
		{"negative: default all with disable depguard", nil, "all", []string{"depguard"}, false},
		{"negative: enable lists other linter", []string{"errcheck"}, "", nil, false},
		{"boundary: empty config", nil, "", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := config.IsDepguardEnabled(tc.enable, tc.defaultMode, tc.disable)
			if got != tc.wantEnabled {
				t.Fatalf("IsDepguardEnabled = %v, want %v", got, tc.wantEnabled)
			}
		})
	}
}

func TestDepguardNoRules_ImplicitStdlib(t *testing.T) {
	index := setupDepguardFramework(t)
	consumer := setupDepguardConsumer(t)
	golangciYaml := `linters:
  enable:
    - depguard
`
	writeFixture(t, consumer, ".golangci.yml", golangciYaml)

	report, err := ScanRepoWithFramework(t.Context(), consumer, index, nil)
	if err != nil {
		t.Fatal(err)
	}
	dep := demandFor(t, report, "go.yaml.in/yaml/v3")
	if dep.Status != StatusDeclinedByPolicy {
		t.Fatalf("got status %q, want %q", dep.Status, StatusDeclinedByPolicy)
	}
	if !strings.Contains(dep.Notes, "not in the allow list") {
		t.Fatalf("dep.Notes %q does not state 'not in the allow list'", dep.Notes)
	}
}

func TestDepguardNotice_BadListMode(t *testing.T) {
	index := setupDepguardFramework(t)
	consumer := setupDepguardConsumer(t)
	golangciYaml := `linters:
  enable:
    - depguard
  settings:
    depguard:
      rules:
        main:
          list-mode: invalid_mode
          deny:
            - pkg: "github.com/"
`
	writeFixture(t, consumer, ".golangci.yml", golangciYaml)

	report, err := ScanRepoWithFramework(t.Context(), consumer, index, nil)
	if err != nil {
		t.Fatal(err)
	}
	dep := demandFor(t, report, "go.yaml.in/yaml/v3")
	if dep.Status != StatusCovered {
		t.Fatalf("got status %q, want %q (bad list-mode must decline nothing)", dep.Status, StatusCovered)
	}
	if len(report.Deprecations) != 1 {
		t.Fatalf("expected 1 notice in Deprecations, got %v", report.Deprecations)
	}
	if !strings.Contains(report.Deprecations[0], ".golangci.yml") || !strings.Contains(report.Deprecations[0], "unknown list-mode") {
		t.Fatalf("notice %q does not name file and problem", report.Deprecations[0])
	}
}

func TestDepguardNotEnabled_DeclinesNothing(t *testing.T) {
	index := setupDepguardFramework(t)
	consumer := setupDepguardConsumer(t)
	golangciYaml := `linters:
  enable:
    - errcheck
  settings:
    depguard:
      rules:
        main:
          deny:
            - pkg: "github.com/"
`
	writeFixture(t, consumer, ".golangci.yml", golangciYaml)

	report, err := ScanRepoWithFramework(t.Context(), consumer, index, nil)
	if err != nil {
		t.Fatal(err)
	}
	dep := demandFor(t, report, "go.yaml.in/yaml/v3")
	if dep.Status != StatusCovered {
		t.Fatalf("got status %q, want %q (depguard disabled must decline nothing)", dep.Status, StatusCovered)
	}
}

func TestDepguardNotice_InvalidYAML(t *testing.T) {
	index := setupDepguardFramework(t)
	consumer := setupDepguardConsumer(t)
	writeFixture(t, consumer, ".golangci.yml", "linters: [unclosed")

	report, err := ScanRepoWithFramework(t.Context(), consumer, index, nil)
	if err != nil {
		t.Fatal(err)
	}
	dep := demandFor(t, report, "go.yaml.in/yaml/v3")
	if dep.Status != StatusCovered {
		t.Fatalf("got status %q, want %q (invalid YAML must decline nothing)", dep.Status, StatusCovered)
	}
	if len(report.Deprecations) != 1 {
		t.Fatalf("expected 1 notice in Deprecations, got %v", report.Deprecations)
	}
	if !strings.Contains(report.Deprecations[0], ".golangci.yml") {
		t.Fatalf("notice %q does not name file", report.Deprecations[0])
	}
}
