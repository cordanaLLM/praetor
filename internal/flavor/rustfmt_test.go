// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

// #567: rust-systems scaffolded rustfmt.toml with edition 2024 whatever the crates declared.
// cargo fmt passes each crate's edition to rustfmt, so it stayed clean, while rustfmt run
// directly, as a pre-commit hook on staged files does, formatted in the 2024 style and failed
// code cargo fmt accepts. The edition now comes from the root Cargo.toml, and an unedited copy
// of an earlier scaffold is refreshed by a plain apply.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// rustfmtPriorFixtures holds every rustfmt.toml earlier releases scaffolded, one file per text,
// named edition-<edition>.rustfmt.toml.
const rustfmtPriorFixtures = "testdata/rustfmt-prior"

// rustfmtSettings is what every scaffolded rustfmt.toml sets beside the edition.
const rustfmtSettings = "max_width = 100\nnewline_style = \"Unix\"\nuse_small_heuristics = \"Default\"\n"

// rustfmtFor is the rustfmt.toml rust-systems scaffolds for edition, "" for none.
func rustfmtFor(edition string) string {
	if edition == "" {
		return rustfmtSettings
	}
	return "edition = \"" + edition + "\"\n" + rustfmtSettings
}

// applyRust applies rust-systems to a repository holding files and returns the report and the
// rustfmt.toml it leaves.
func applyRust(t *testing.T, files map[string]string, force bool) (*flavor.ApplyReport, string) {
	t.Helper()
	repo := repoWithFiles(t, files)
	report, err := flavor.ApplyFlavor(t.Context(), repo, "rust-systems", force)
	if err != nil {
		t.Fatalf("apply rust-systems: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "rustfmt.toml"))
	if err != nil {
		t.Fatalf("read rustfmt.toml: %v", err)
	}
	return report, string(data)
}

func TestScaffoldedRustfmtFollowsTheCrateEdition(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, edition string
	}{
		// Positive: the edition a workspace gives its members, and a single crate's own.
		{"workspace", "[workspace]\nmembers = [\"crates/*\"]\nresolver = \"2\"\n\n[workspace.package]\nversion = \"0.1.0\"\nedition = \"2021\"\n", "2021"},
		{"single-crate", "[package]\nname = \"widget\"\nversion = \"0.1.0\"\nedition = \"2018\"\n", "2018"},
		{"single-crate-2024", "[package]\nname = \"widget\"\nedition = \"2024\"\n", "2024"},
		// Boundary: the workspace edition wins over the root package's, in either order, and
		// padded dotted keys, literal strings, comments and CRLF manifests read the same.
		{"root-package-inherits", "[package]\nname = \"widget\"\nedition.workspace = true\n\n[workspace.package]\nedition = \"2021\"\n", "2021"},
		{"workspace-first", "[workspace.package]\nedition = \"2021\"\n\n[package]\nname = \"widget\"\nedition = \"2024\"\n", "2021"},
		{"padded-header", "[ workspace . package ]\nedition = \"2021\" # shared by every member\n", "2021"},
		{"dotted-top-level", "package . edition = '2018' # the crate edition\n", "2018"},
		{"crlf", "[package]\r\nname = \"widget\"\r\nedition = \"2021\"\r\n", "2021"},
		// Negative: no edition, so the key is left out and rustfmt follows Cargo, both on 2015.
		{"no-edition", "[package]\nname = \"widget\"\nversion = \"0.1.0\"\n", ""},
		{"virtual-workspace", "[workspace]\nmembers = [\"a\", \"b\"]\n", ""},
		{"inherited-without-workspace-edition", "[package]\nname = \"widget\"\nedition = { workspace = true }\n", ""},
		{"other-tables", "[package]\nname = \"widget\"\n\n[package.metadata.docs]\nedition = \"2021\"\n\n[lib]\nedition = \"2021\"\n", ""},
		{"commented-out", "[package]\nname = \"widget\"\n# edition = \"2021\"\n", ""},
		{"not-an-edition", "[package]\nname = \"widget\"\nedition = \"latest\"\n", ""},
		{"escaped-value", "[package]\nname = \"widget\"\nedition = \"2021\\\"\\nmax_width = 1\"\n", ""},
		{"unquoted", "[package]\nname = \"widget\"\nedition = 2021\n", ""},
		{"no-manifest", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			if tc.manifest != "" {
				files["Cargo.toml"] = tc.manifest
			}
			report, got := applyRust(t, files, false)
			if want := rustfmtFor(tc.edition); got != want {
				t.Errorf("rustfmt.toml = %q, want %q", got, want)
			}
			if !slices.Contains(report.CreatedTemplates, "rustfmt.toml") || len(report.UnmetTemplates) > 0 {
				t.Errorf("rustfmt.toml not reported created: created %v, unmet %v", report.CreatedTemplates, report.UnmetTemplates)
			}
		})
	}
}

// readRustfmtPriors returns every earlier scaffold text by the edition it named, in LF: the
// texts were written with LF, and a Windows checkout under core.autocrlf may convert the
// fixtures, which the tests below then turn into CRLF themselves where they mean to.
func readRustfmtPriors(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(rustfmtPriorFixtures)
	if err != nil {
		t.Fatalf("read %s: %v", rustfmtPriorFixtures, err)
	}
	priors := make(map[string]string, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(rustfmtPriorFixtures, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		edition := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "edition-"), ".rustfmt.toml")
		priors[edition], _ = util.NormalizeLineEndings(string(data))
	}
	if len(priors) == 0 {
		t.Fatalf("%s holds no earlier scaffold", rustfmtPriorFixtures)
	}
	return priors
}

// rustfmtTemplate is rust-systems' rustfmt.toml template.
func rustfmtTemplate(t *testing.T) flavor.TemplateItem {
	t.Helper()
	for _, tmpl := range (&flavor.RustSystemsFlavor{}).RequiredTemplates() {
		if tmpl.Path == "rustfmt.toml" {
			return tmpl
		}
	}
	t.Fatal("rust-systems requires no rustfmt.toml")
	return flavor.TemplateItem{}
}

// Every recorded earlier text has a fixture and every fixture is recorded, and each is exactly
// what the template renders for the edition it names, so a crate on that edition keeps it.
func TestRustfmtPriorTextsAreEarlierRenderings(t *testing.T) {
	prior := rustfmtTemplate(t).Prior
	seen := make(map[string]bool, len(prior))
	for edition, text := range readRustfmtPriors(t) {
		digest, _, err := util.CanonicalTextDigest([]byte(text))
		if err != nil {
			t.Fatalf("edition %s: %v", edition, err)
		}
		if _, ok := prior[digest]; !ok {
			t.Errorf("the edition %s scaffold (%s) is not a recorded earlier text", edition, digest)
		}
		seen[digest] = true
		rendered, err := templates.RenderFile("rust/rustfmt.toml.tmpl", templates.Context{RustEdition: edition})
		if err != nil || rendered != text {
			t.Errorf("edition %s renders %q (err %v), want the earlier text %q", edition, rendered, err, text)
		}
	}
	for digest, origin := range prior {
		if !seen[digest] {
			t.Errorf("recorded earlier text %s (%s) has no fixture under %s", digest, origin, rustfmtPriorFixtures)
		}
	}
}

// Positive: an unedited earlier scaffold is refreshed to the crate's edition without --force,
// in its own line-ending style, and reported refreshed rather than created or skipped.
func TestRustfmtApply_Positive_RefreshesAnEarlierScaffold(t *testing.T) {
	manifests := map[string]string{
		"2018": "[package]\nname = \"widget\"\nedition = \"2018\"\n",
		"":     "[workspace]\nmembers = [\"a\"]\n",
	}
	for edition, text := range readRustfmtPriors(t) {
		for crateEdition, manifest := range manifests {
			for _, crlf := range []bool{false, true} {
				report, got := applyRust(t, map[string]string{
					"Cargo.toml":   manifest,
					"rustfmt.toml": util.RestoreLineEndings(text, crlf),
				}, false)
				if want := util.RestoreLineEndings(rustfmtFor(crateEdition), crlf); got != want {
					t.Errorf("edition %s scaffold, crate %q, crlf %v: rustfmt.toml = %q, want %q", edition, crateEdition, crlf, got, want)
				}
				if !slices.Equal(report.RefreshedTemplates, []string{"rustfmt.toml"}) ||
					slices.Contains(report.CreatedTemplates, "rustfmt.toml") || slices.Contains(report.SkippedTemplates, "rustfmt.toml") {
					t.Errorf("edition %s scaffold, crate %q: refreshed %v, created %v, skipped %v", edition, crateEdition,
						report.RefreshedTemplates, report.CreatedTemplates, report.SkippedTemplates)
				}
			}
		}
	}
}

// Negative: an edited scaffold is the repository's configuration and stays byte for byte
// without --force, reported skipped; --force replaces it with the rendering.
func TestRustfmtApply_Negative_EditedScaffoldIsKept(t *testing.T) {
	manifest := "[workspace.package]\nedition = \"2021\"\n"
	edited := rustfmtFor("2024") + "imports_granularity = \"Crate\"\n"
	report, got := applyRust(t, map[string]string{"Cargo.toml": manifest, "rustfmt.toml": edited}, false)
	if got != edited || len(report.RefreshedTemplates) > 0 || !slices.Contains(report.SkippedTemplates, "rustfmt.toml") {
		t.Errorf("edited rustfmt.toml was not kept: got %q, refreshed %v, skipped %v", got, report.RefreshedTemplates, report.SkippedTemplates)
	}
	report, got = applyRust(t, map[string]string{"Cargo.toml": manifest, "rustfmt.toml": edited}, true)
	if got != rustfmtFor("2021") || !slices.Contains(report.CreatedTemplates, "rustfmt.toml") || len(report.RefreshedTemplates) > 0 {
		t.Errorf("--force did not replace the edited rustfmt.toml: got %q, created %v, refreshed %v", got, report.CreatedTemplates, report.RefreshedTemplates)
	}
}

// Boundary: an earlier text that already is the rendering for the crate's edition stays as it
// is, and one with mixed line endings is not an earlier text, so it is kept too.
func TestRustfmtApply_Boundary_EarlierTextOnTheCrateEditionOrMixedEndingsStays(t *testing.T) {
	current := rustfmtFor("2024")
	mixed := strings.Replace(current, "\n", "\r\n", 1)
	for name, existing := range map[string]string{"already-current": current, "mixed-endings": mixed} {
		report, got := applyRust(t, map[string]string{
			"Cargo.toml":   "[package]\nname = \"widget\"\nedition = \"2024\"\n",
			"rustfmt.toml": existing,
		}, false)
		if got != existing || len(report.RefreshedTemplates) > 0 || !slices.Contains(report.SkippedTemplates, "rustfmt.toml") {
			t.Errorf("%s: got %q, refreshed %v, skipped %v", name, got, report.RefreshedTemplates, report.SkippedTemplates)
		}
	}
}
