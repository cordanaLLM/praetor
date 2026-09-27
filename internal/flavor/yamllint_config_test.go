package flavor_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/templates"
)

// yamllintPolicy is a yamllint configuration the os-image template accepts.
const yamllintPolicy = "extends: default\n"

// yamllintNames is the order yamllint 1.38.0 searches (cli.find_project_config_filepath).
var yamllintNames = []string{".yamllint", ".yamllint.yaml", ".yamllint.yml"}

// imageForge returns an os-image repository whose settings conform, plus the given files, so
// the yamllint template alone decides the audit.
func imageForge(t *testing.T, files map[string]string) string {
	t.Helper()
	all := map[string]string{
		"mkosi.conf":                 "[Output]\nFormat=disk\n",
		"lefthook.yml":               "pre-commit:\n  jobs: []\n",
		".github/rulesets/main.json": `{"name": "main"}`,
	}
	for name, body := range files {
		all[name] = body
	}
	return repoWithFiles(t, all)
}

// yamllintTemplate returns the os-image template the audit and apply read the names from.
func yamllintTemplate(t *testing.T) flavor.TemplateItem {
	t.Helper()
	f, err := flavor.Get("os-image")
	if err != nil {
		t.Fatalf("os-image is not registered: %v", err)
	}
	for _, item := range f.RequiredTemplates() {
		if item.Search != nil && item.Search.Tool == "yamllint" {
			return item
		}
	}
	t.Fatal("os-image declares no template yamllint searches for")
	return flavor.TemplateItem{}
}

// applyForge applies os-image without --force and fails the test on any error.
func applyForge(t *testing.T, repo string) *flavor.ApplyReport {
	t.Helper()
	report, err := flavor.ApplyFlavor(t.Context(), repo, "os-image", false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(report.Errors) > 0 {
		t.Fatalf("apply recorded errors: %v", report.Errors)
	}
	return report
}

func auditForge(t *testing.T, repo string) *flavor.FlavorAuditReport {
	t.Helper()
	report, err := flavor.AuditFlavor(repo, "os-image")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	return report
}

// The os-image template names exactly the files yamllint reads, in yamllint's order, and
// scaffolds the last of them.
func TestOSImageYamllintTemplate_SearchesYamllintsOrder(t *testing.T) {
	item := yamllintTemplate(t)
	if !slices.Equal(item.Search.Names, yamllintNames) {
		t.Fatalf("searched names %v, want yamllint's order %v", item.Search.Names, yamllintNames)
	}
	if item.Path != ".yamllint.yml" || len(item.AltPaths) != 0 {
		t.Fatalf("path %q alternatives %v, want .yamllint.yml and no AltPaths", item.Path, item.AltPaths)
	}
}

// #505, positive: each name yamllint reads satisfies the template, so a forge keeping its
// policy in .yamllint.yaml passes instead of failing for lacking .yamllint.yml.
func TestAuditFlavor_Positive_EveryYamllintNameSatisfiesOSImage(t *testing.T) {
	for _, name := range yamllintNames {
		report := auditForge(t, imageForge(t, map[string]string{name: yamllintPolicy}))
		if !report.Passed || len(report.MissingTemplates) != 0 {
			t.Errorf("%s: passed %v, missing %v", name, report.Passed, missingPaths(report))
		}
		if len(report.ShadowedTemplates) != 0 {
			t.Errorf("%s: one file shadows nothing, got %+v", name, report.ShadowedTemplates)
		}
	}
}

// #505, two present: the audit passes and names the file yamllint reads, and every later
// name as ignored.
func TestAuditFlavor_Positive_ReportsWhichYamllintConfigIsRead(t *testing.T) {
	cases := []struct {
		present []string
		want    flavor.ShadowedTemplate
	}{
		{[]string{".yamllint.yaml", ".yamllint.yml"},
			flavor.ShadowedTemplate{Path: ".yamllint.yml", Tool: "yamllint", InUse: ".yamllint.yaml", Ignored: []string{".yamllint.yml"}}},
		{[]string{".yamllint", ".yamllint.yml"},
			flavor.ShadowedTemplate{Path: ".yamllint.yml", Tool: "yamllint", InUse: ".yamllint", Ignored: []string{".yamllint.yml"}}},
		{yamllintNames,
			flavor.ShadowedTemplate{Path: ".yamllint.yml", Tool: "yamllint", InUse: ".yamllint", Ignored: []string{".yamllint.yaml", ".yamllint.yml"}}},
	}
	for _, tc := range cases {
		files := map[string]string{}
		for _, name := range tc.present {
			files[name] = yamllintPolicy
		}
		report := auditForge(t, imageForge(t, files))
		if !report.Passed {
			t.Errorf("%v: audit failed, missing %v", tc.present, missingPaths(report))
		}
		if len(report.ShadowedTemplates) != 1 || !shadowEqual(report.ShadowedTemplates[0], tc.want) {
			t.Errorf("%v: shadowed %+v, want %+v", tc.present, report.ShadowedTemplates, tc.want)
		}
	}
}

func shadowEqual(a, b flavor.ShadowedTemplate) bool {
	return a.Path == b.Path && a.Tool == b.Tool && a.InUse == b.InUse && slices.Equal(a.Ignored, b.Ignored)
}

// Negative: the audit judges the file yamllint reads. An empty .yamllint shadows a valid
// .yamllint.yml, and yamllint never reads the valid one, so the template is missing. A name
// yamllint does not look for satisfies nothing.
func TestAuditFlavor_Negative_ShadowedValidYamllintConfigDoesNotCount(t *testing.T) {
	report := auditForge(t, imageForge(t, map[string]string{".yamllint": "", ".yamllint.yml": yamllintPolicy}))
	if report.Passed || !slices.Equal(missingPaths(report), []string{".yamllint.yml"}) {
		t.Errorf("passed %v, missing %v; want the template missing", report.Passed, missingPaths(report))
	}
	want := flavor.ShadowedTemplate{Path: ".yamllint.yml", Tool: "yamllint", InUse: ".yamllint", Ignored: []string{".yamllint.yml"}}
	if len(report.ShadowedTemplates) != 1 || !shadowEqual(report.ShadowedTemplates[0], want) {
		t.Errorf("shadowed %+v, want %+v", report.ShadowedTemplates, want)
	}
	for _, unread := range []string{"yamllint.yml", ".yamllint.json", ".yamllintrc"} {
		report := auditForge(t, imageForge(t, map[string]string{unread: yamllintPolicy}))
		if report.Passed || len(report.MissingTemplates) != 1 {
			t.Errorf("%s: a name yamllint never reads satisfied the template", unread)
		}
	}
}

// Boundary: yamllint skips a directory under a searched name and reads the next file, and so
// does the audit.
func TestAuditFlavor_Boundary_DirectoryUnderYamllintNameIsSkipped(t *testing.T) {
	repo := imageForge(t, map[string]string{".yamllint.yaml": yamllintPolicy})
	if err := os.Mkdir(filepath.Join(repo, ".yamllint"), 0o755); err != nil {
		t.Fatal(err)
	}
	report := auditForge(t, repo)
	if !report.Passed || len(report.ShadowedTemplates) != 0 {
		t.Errorf("passed %v, missing %v, shadowed %+v", report.Passed, missingPaths(report), report.ShadowedTemplates)
	}
}

// #505, apply: a repository carrying any other yamllint name keeps it, gets no .yamllint.yml
// yamllint would ignore, and the report names the file in use.
func TestApplyFlavor_Positive_KeepsExistingYamllintConfig(t *testing.T) {
	for _, name := range yamllintNames[:2] {
		repo := imageForge(t, map[string]string{name: yamllintPolicy})
		report := applyForge(t, repo)
		if _, err := os.Stat(filepath.Join(repo, ".yamllint.yml")); !os.IsNotExist(err) {
			t.Errorf("%s: apply wrote .yamllint.yml beside it: %v", name, err)
		}
		want := []flavor.CoveredTemplate{{Path: ".yamllint.yml", InUse: name}}
		if !slices.Equal(report.CoveredTemplates, want) || len(report.CreatedTemplates) != 0 {
			t.Errorf("%s: covered %+v created %v, want %+v and nothing created", name, report.CoveredTemplates, report.CreatedTemplates, want)
		}
		if got := readFile(t, repo, name); got != yamllintPolicy {
			t.Errorf("%s: apply changed the existing configuration: %q", name, got)
		}
	}
}

// Boundary: the scaffolded name already present is an existing file, kept as before, not a
// covered template.
func TestApplyFlavor_Boundary_ScaffoldNameAlreadyPresentIsSkipped(t *testing.T) {
	repo := imageForge(t, map[string]string{".yamllint.yml": yamllintPolicy})
	report := applyForge(t, repo)
	if !slices.Equal(report.SkippedTemplates, []string{".yamllint.yml"}) || len(report.CoveredTemplates) != 0 {
		t.Errorf("skipped %v covered %+v, want .yamllint.yml skipped", report.SkippedTemplates, report.CoveredTemplates)
	}
}

// Negative: a repository with no yamllint configuration gets the default body, and the audit
// then accepts it.
func TestApplyFlavor_Negative_NoYamllintConfigScaffoldsTheDefault(t *testing.T) {
	repo := imageForge(t, nil)
	report := applyForge(t, repo)
	if !slices.Equal(report.CreatedTemplates, []string{".yamllint.yml"}) || len(report.CoveredTemplates) != 0 {
		t.Fatalf("created %v covered %+v, want .yamllint.yml created", report.CreatedTemplates, report.CoveredTemplates)
	}
	body, err := templates.RenderFile("osimage/.yamllint.yml.tmpl", templates.Context{})
	if err != nil {
		t.Fatalf("render the default: %v", err)
	}
	if got := readFile(t, repo, ".yamllint.yml"); got != body {
		t.Errorf("scaffolded %q, want the default body %q", got, body)
	}
	if audit := auditForge(t, repo); !audit.Passed {
		t.Errorf("the scaffolded default did not satisfy the audit: missing %v", missingPaths(audit))
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}
