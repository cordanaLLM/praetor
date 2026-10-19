// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package needs

import (
	"strings"
	"testing"
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
