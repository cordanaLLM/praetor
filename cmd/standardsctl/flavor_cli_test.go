package main

import (
	"strings"
	"testing"
)

// Positive: flavor apply names the templates it leaves to their producer, so an operator
// is told what is still missing instead of receiving a comment placeholder in its place.
func TestFlavorApply_Positive_ReportsTemplatesLeftToTheirProducer(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"apply", t.TempDir(), "--flavor=go-library"})
	})
	if err != nil {
		t.Fatalf("flavor apply: %v\n%s", err, out)
	}
	mustContain(t, out, "Left to Producer  (2): .standards.yaml (praetorctl adopt), .standards.lock (praetorctl adopt)")
}

// Negative: a flavor whose every template has a scaffolded body leaves nothing to a producer
// and prints no such line.
func TestFlavorApply_Negative_NoProducerLineWhenNothingIsDeferred(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"apply", t.TempDir(), "--flavor=go-service"})
	})
	if err != nil {
		t.Fatalf("flavor apply: %v\n%s", err, out)
	}
	if strings.Contains(out, "Left to Producer") {
		t.Fatalf("go-service defers nothing, got:\n%s", out)
	}
}

// Boundary: inspect names the producer of each producer-owned template and nothing for a
// scaffolded one.
func TestFlavorInspect_Boundary_NamesTheProducer(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"inspect", "agentic-autonomous"})
	})
	if err != nil {
		t.Fatalf("flavor inspect: %v\n%s", err, out)
	}
	mustContain(t, out, "written by: praetorctl adopt", "written by: praetorctl compile-context")
	scaffolded, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"inspect", "go-service"})
	})
	// Only the templates are checked: two go-service settings name their producer
	// (TestFlavorInspect_Boundary_NamesSettingProducers).
	templates, _, _ := strings.Cut(scaffolded, "Required Settings:")
	if err != nil || strings.Contains(templates, "written by:") {
		t.Fatalf("go-service templates are scaffolded, not produced elsewhere: %v\n%s", err, scaffolded)
	}
}

// Boundary: a template whose body cannot work in the repository is named with what the
// repository lacks, one per line, instead of being written or silently dropped.
func TestFlavorApply_Boundary_ReportsTemplatesWithAnUnmetRequirement(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"apply", t.TempDir(), "--flavor=typescript-node"})
	})
	if err != nil {
		t.Fatalf("flavor apply: %v\n%s", err, out)
	}
	mustContain(t, out, "Unmet Requirement (1):\n    - .github/workflows/ci.yml: package.json is unreadable")
	if strings.Contains(out, "Created Templates (3)") {
		t.Fatalf("the CI job was reported created:\n%s", out)
	}
}

// imageForgeFixture writes an os-image repository whose settings conform, plus one YAML
// mapping under each given yamllint configuration name.
func imageForgeFixture(t *testing.T, yamllintNames ...string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "mkosi.conf", "[Output]\nFormat=disk\n")
	writeFixtureFile(t, dir, "lefthook.yml", "pre-commit:\n  jobs: []\n")
	writeFixtureFile(t, dir, ".github/rulesets/main.json", `{"name": "main"}`)
	for _, name := range yamllintNames {
		writeFixtureFile(t, dir, name, "extends: default\n")
	}
	return dir
}

// Positive (#505): with two yamllint configurations present the audit passes and names the
// one yamllint reads and the one it ignores.
func TestFlavorAudit_Positive_NamesTheYamllintConfigInUse(t *testing.T) {
	dir := imageForgeFixture(t, ".yamllint.yaml", ".yamllint.yml")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"audit", dir, "--flavor=os-image"})
	})
	if err != nil {
		t.Fatalf("flavor audit: %v\n%s", err, out)
	}
	mustContain(t, out, "Templates:   1/1 present",
		"Shadowed Templates (advisory):\n  - yamllint reads .yamllint.yaml; ignored: .yamllint.yml")
}

// Negative: one configuration shadows nothing, so no such block is printed.
func TestFlavorAudit_Negative_NoShadowBlockForOneYamllintConfig(t *testing.T) {
	dir := imageForgeFixture(t, ".yamllint.yaml")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"audit", dir, "--flavor=os-image"})
	})
	if err != nil || strings.Contains(out, "Shadowed Templates") {
		t.Fatalf("flavor audit: %v\n%s", err, out)
	}
}

// Boundary: apply names the configuration it kept instead of writing a rival yamllint would
// ignore, and inspect lists every name in yamllint's order.
func TestFlavorApply_Boundary_ReportsTheYamllintConfigItKept(t *testing.T) {
	dir := imageForgeFixture(t, ".yamllint")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"apply", dir, "--flavor=os-image"})
	})
	if err != nil {
		t.Fatalf("flavor apply: %v\n%s", err, out)
	}
	mustContain(t, out, "Created Templates (0)", "Kept Existing Config (1):\n    - .yamllint (.yamllint.yml not written)")
	inspect, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"inspect", "os-image"})
	})
	if err != nil {
		t.Fatalf("flavor inspect: %v\n%s", err, inspect)
	}
	mustContain(t, inspect, "yamllint reads the first of: .yamllint, .yamllint.yaml, .yamllint.yml")
}
