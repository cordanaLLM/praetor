package forge

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// The issue forms and the Renovate configuration apply labels by name. GitHub creates a label
// an issue form names only when a maintainer files the issue, and a label reconciliation
// writes only what .config/labels.yaml names, so a label missing from the taxonomy either
// never exists or exists with an unmanaged color and description (BUG-548).

// engineCheckout is the checkout root, relative to this package.
var engineCheckout = filepath.Join("..", "..")

// issueForm is the part of a GitHub issue form that applies labels.
type issueForm struct {
	Labels []string `yaml:"labels"`
}

// renovateLabels is the part of renovate.json that applies labels, globally and per rule.
type renovateLabels struct {
	Labels       []string `yaml:"labels"`
	AddLabels    []string `yaml:"addLabels"`
	PackageRules []struct {
		Labels    []string `yaml:"labels"`
		AddLabels []string `yaml:"addLabels"`
	} `yaml:"packageRules"`
}

// labelConsumers returns every label the engine's issue forms and renovate.json apply, mapped
// to the files that apply it.
func labelConsumers(t *testing.T) map[string][]string {
	t.Helper()
	used := map[string][]string{}
	forms, err := filepath.Glob(filepath.Join(engineCheckout, ".github", "ISSUE_TEMPLATE", "*.yml"))
	if err != nil || len(forms) == 0 {
		t.Fatalf("no issue forms found: %v", err)
	}
	for _, path := range forms {
		var form issueForm
		decodeConsumer(t, path, &form)
		addConsumer(used, path, form.Labels)
	}
	path := filepath.Join(engineCheckout, "renovate.json")
	var renovate renovateLabels
	decodeConsumer(t, path, &renovate)
	addConsumer(used, path, renovate.Labels, renovate.AddLabels)
	for _, rule := range renovate.PackageRules {
		addConsumer(used, path, rule.Labels, rule.AddLabels)
	}
	return used
}

func decodeConsumer(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func addConsumer(used map[string][]string, path string, lists ...[]string) {
	for _, list := range lists {
		for _, name := range list {
			used[name] = append(used[name], filepath.ToSlash(path))
		}
	}
}

// missingFromTaxonomy returns the used label names the taxonomy does not name, compared
// exactly: GitHub matches label names case-insensitively, but reconciliation writes the
// taxonomy's spelling, so "Bug" in a form and "bug" in the taxonomy are still drift.
func missingFromTaxonomy(used map[string][]string, taxonomy []Label) []string {
	names := make(map[string]bool, len(taxonomy))
	for _, label := range taxonomy {
		names[label.Name] = true
	}
	var missing []string
	for name := range used {
		if !names[name] {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}

// Positive: every label the issue forms and renovate.json apply is in the canonical taxonomy
// and in praetor's own .config/labels.yaml.
func TestLabelConsumers_Positive_EveryAppliedLabelIsInTheTaxonomy(t *testing.T) {
	used := labelConsumers(t)
	for _, want := range []string{"bug", "triage", "enhancement", "rfc", "api-review", "breaking-change", "dependencies", "flavor:latest"} {
		if len(used[want]) == 0 {
			t.Errorf("consumer scan did not find %q; the scan no longer reads the forms or renovate.json", want)
		}
	}
	own, err := os.ReadFile(filepath.Join(engineCheckout, ".config", "labels.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, taxonomy := range map[string][]byte{"default taxonomy": DefaultLabelTaxonomy(), ".config/labels.yaml": own} {
		if missing := missingFromTaxonomy(used, parseTaxonomy(t, taxonomy)); len(missing) > 0 {
			t.Errorf("%s lacks labels the forms or renovate.json apply: %v", name, missing)
		}
	}
}

// Negative: a taxonomy without a label a consumer applies is reported, with nothing else.
func TestLabelConsumers_Negative_UnknownLabelIsReported(t *testing.T) {
	used := map[string][]string{"triage": {"bug.yml"}, "bug": {"bug.yml"}}
	got := missingFromTaxonomy(used, []Label{{Name: "bug", Color: "d73a4a"}})
	if !slices.Equal(got, []string{"triage"}) {
		t.Fatalf("missing labels = %v, want [triage]", got)
	}
}

// Boundary: names match exactly, so a case-only difference is reported as missing.
func TestLabelConsumers_Boundary_NamesMatchCaseSensitively(t *testing.T) {
	used := map[string][]string{"bug": {"bug.yml"}}
	if got := missingFromTaxonomy(used, []Label{{Name: "Bug", Color: "d73a4a"}}); !slices.Equal(got, []string{"bug"}) {
		t.Fatalf("case-only difference not reported: %v", got)
	}
	if got := missingFromTaxonomy(used, []Label{{Name: "bug", Color: "d73a4a"}}); len(got) != 0 {
		t.Fatalf("exact match reported as missing: %v", got)
	}
}
