package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

const priorAuditorFixture = "../../internal/adopt/testdata/personas/repo-auditor/caveman-wrapped.repo-auditor.md.prior"

func readPriorAuditorPersona(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(priorAuditorFixture))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Positive: compile-context recognises an unedited earlier generated persona and names both
// supported remedies for its now-required model tier.
func TestCompileContextPersonaModelRemedy_Positive_EarlierTemplateNamesRemedy(t *testing.T) {
	dir := newContextFixture(t, false)
	writeFixtureFile(t, dir, ".agents/agents/repo-auditor.md", readPriorAuditorPersona(t))

	_, err := runCompileContextCmd(t, dir)
	if !errors.Is(err, compiler.ErrPersonaModelTier) {
		t.Fatalf("want ErrPersonaModelTier, got %v", err)
	}
	for _, want := range []string{
		".agents/agents/repo-auditor.md: front matter lacks the required key \"model\"",
		"unedited copy of an earlier engine's template",
		"praetorctl adopt replaces it",
		"run the engine the repository pins",
	} {
		mustErrContain(t, err, want)
	}
}

// Negative: repository-edited persona text keeps the model-tier refusal and is not presented as
// engine-owned text that adoption can replace; the remedy names the tier adoption adds (#1426).
func TestCompileContextPersonaModelRemedy_Negative_EditedPersonaKeepsCurrentError(t *testing.T) {
	dir := newContextFixture(t, false)
	edited := readPriorAuditorPersona(t) + "\nRepository edit.\n"
	writeFixtureFile(t, dir, ".agents/agents/repo-auditor.md", edited)

	_, err := runCompileContextCmd(t, dir)
	if !errors.Is(err, compiler.ErrPersonaModelTier) {
		t.Fatalf("want ErrPersonaModelTier, got %v", err)
	}
	mustErrContain(t, err, "persona model tier missing or invalid")
	mustErrContain(t, err, "praetorctl adopt (no --force needed) adds the template's model tier")
	if strings.Contains(err.Error(), "unedited copy") || strings.Contains(err.Error(), "praetorctl adopt replaces it") {
		t.Fatalf("edited persona got earlier-template remedy: %v", err)
	}
}

// Boundary: adding an allowed model tier to the earlier persona makes it valid, so no remedy or
// model-tier error is produced.
func TestCompileContextPersonaModelRemedy_Boundary_ModelTierNeedsNoRemedy(t *testing.T) {
	dir := newContextFixture(t, false)
	withModel := strings.Replace(readPriorAuditorPersona(t), "description:", "model: pro\ndescription:", 1)
	writeFixtureFile(t, dir, ".agents/agents/repo-auditor.md", withModel)

	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
}
