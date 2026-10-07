// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

// #567: rust-systems scaffolded rustfmt.toml with edition 2024 whatever the crates declared.
// cargo fmt passes each crate's edition to rustfmt, so it stayed clean, while rustfmt run
// directly, as a pre-commit hook on staged files does, formatted in the 2024 style and failed
// code cargo fmt accepts. The edition is now the one every crate of the workspace is on, the
// scaffold is withheld where they share none, and an unedited copy of an earlier scaffold is
// refreshed by a plain apply.

import (
	"errors"
	"fmt"
	"maps"
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
// rustfmt.toml it leaves, "" when it leaves none.
func applyRust(t *testing.T, files map[string]string, force bool) (*flavor.ApplyReport, string) {
	t.Helper()
	repo := repoWithFiles(t, files)
	report, err := flavor.ApplyFlavor(t.Context(), repo, "rust-systems", force)
	if err != nil {
		t.Fatalf("apply rust-systems: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "rustfmt.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return report, ""
	}
	if err != nil {
		t.Fatalf("read rustfmt.toml: %v", err)
	}
	return report, string(data)
}

// crate is a member manifest on edition, "" declaring none.
func crate(name, edition string) string {
	if edition == "" {
		return "[package]\nname = \"" + name + "\"\nversion = \"0.1.0\"\n"
	}
	return "[package]\nname = \"" + name + "\"\nedition = \"" + edition + "\"\n"
}

func TestScaffoldedRustfmtFollowsTheCrateEdition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		edition string
	}{
		// Positive: the edition every crate shares, whether each declares it, inherits it from
		// [workspace.package] or is the single root crate.
		{"workspace-inherited", map[string]string{
			"Cargo.toml":          "[workspace]\nmembers = [\"crates/*\"]\nresolver = \"2\"\n\n[workspace.package]\nversion = \"0.1.0\"\nedition = \"2021\"\n",
			"crates/a/Cargo.toml": "[package]\nname = \"a\"\nedition.workspace = true\n",
			"crates/b/Cargo.toml": "[package]\nname = \"b\"\nedition = { workspace = true }\n",
		}, "2021"},
		{"virtual-workspace-member-editions", map[string]string{
			"Cargo.toml": "[workspace]\nmembers = [\"a\", \"b\"]\n", "a/Cargo.toml": crate("a", "2024"), "b/Cargo.toml": crate("b", "2024"),
		}, "2024"},
		{"single-crate", map[string]string{"Cargo.toml": "[package]\nname = \"widget\"\nversion = \"0.1.0\"\nedition = \"2018\"\n"}, "2018"},
		{"root-crate-and-member", map[string]string{
			"Cargo.toml": crate("root", "2021") + "\n[workspace]\nmembers = [\"tools/x\"]\n", "tools/x/Cargo.toml": crate("x", "2021"),
		}, "2021"},
		// Boundary: members over several lines with comments, a glob whose excluded directory
		// and plain files are no members, padded headers and dotted keys, literal strings and
		// CRLF read the same; a root crate's own edition wins over the [workspace.package] it
		// does not inherit, and a workspace with no crate to read, no members key or a glob
		// matching only files, takes [workspace.package]'s, as cargo metadata does.
		{"multi-line-glob-exclude", map[string]string{
			"Cargo.toml":            "[workspace]\nmembers = [\n    \"crates/*\", # every crate\n    'tools/c',\n]\nexclude = [\"crates/old\"]\n",
			"crates/a/Cargo.toml":   crate("a", "2021"),
			"crates/old/Cargo.toml": crate("old", "2015"),
			"crates/README.md":      "not a crate\n",
			"tools/c/Cargo.toml":    crate("c", "2021"),
		}, "2021"},
		{"root-package-inherits", map[string]string{"Cargo.toml": "[package]\nname = \"widget\"\nedition.workspace = true\n\n[workspace.package]\nedition = \"2021\"\n"}, "2021"},
		{"root-package-own-edition", map[string]string{"Cargo.toml": "[workspace.package]\nedition = \"2021\"\n\n[package]\nname = \"widget\"\nedition = \"2024\"\n"}, "2024"},
		{"padded-headers", map[string]string{
			"Cargo.toml":          "[ workspace ]\nmembers = [\"crates/*\"]\n[ workspace . package ]\nedition = \"2021\" # shared by every member\n",
			"crates/a/Cargo.toml": "[ package ]\nname = \"a\"\nedition . workspace = true\n",
		}, "2021"},
		{"no-members-key", map[string]string{"Cargo.toml": "[workspace]\nresolver = \"2\"\n\n[workspace.package]\nedition = \"2021\"\n"}, "2021"},
		{"glob-matching-only-files", map[string]string{
			"Cargo.toml": "[workspace]\nmembers = [\"crates/*\"]\n[workspace.package]\nedition = \"2021\"\n", "crates/README.md": "not a crate\n",
		}, "2021"},
		{"dotted-top-level", map[string]string{"Cargo.toml": "package . edition = '2018' # the crate edition\n"}, "2018"},
		{"crlf", map[string]string{
			"Cargo.toml": "[workspace]\r\nmembers = [\r\n  \"a\",\r\n]\r\n", "a/Cargo.toml": "[package]\r\nname = \"a\"\r\nedition = \"2021\"\r\n",
		}, "2021"},
		// Negative: no crate declares an edition, so the key is left out and rustfmt follows
		// Cargo, both on 2015.
		{"no-edition", map[string]string{"Cargo.toml": crate("widget", "")}, ""},
		{"virtual-workspace-no-member-edition", map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"a\"]\n", "a/Cargo.toml": crate("a", "")}, ""},
		{"other-tables", map[string]string{"Cargo.toml": "[package]\nname = \"widget\"\n\n[package.metadata.docs]\nedition = \"2021\"\n\n[lib]\nedition = \"2021\"\n"}, ""},
		{"commented-out", map[string]string{"Cargo.toml": "[package]\nname = \"widget\"\n# edition = \"2021\"\n"}, ""},
		{"not-an-edition", map[string]string{"Cargo.toml": "[package]\nname = \"widget\"\nedition = \"latest\"\n"}, ""},
		{"escaped-value", map[string]string{"Cargo.toml": "[package]\nname = \"widget\"\nedition = \"2021\\\"\\nmax_width = 1\"\n"}, ""},
		{"unquoted", map[string]string{"Cargo.toml": "[package]\nname = \"widget\"\nedition = 2021\n"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, got := applyRust(t, tc.files, false)
			if want := rustfmtFor(tc.edition); got != want {
				t.Errorf("rustfmt.toml = %q, want %q", got, want)
			}
			if !slices.Contains(report.CreatedTemplates, "rustfmt.toml") || len(report.UnmetTemplates) > 0 {
				t.Errorf("rustfmt.toml not reported created: created %v, unmet %v", report.CreatedTemplates, report.UnmetTemplates)
			}
		})
	}
}

// withheldRustfmtWorkspaces are repositories whose crates share no edition, or one of whose
// crates cannot be read, the root manifest included, keyed to the reason apply reports. No
// single edition in rustfmt.toml is known to agree with cargo fmt on every crate there.
var withheldRustfmtWorkspaces = map[string]struct {
	files  map[string]string
	reason string
}{
	// No root manifest, as where the crate sits in a subdirectory, and one that is no regular
	// file: the crate editions are unknown, which is not "no crate declares one".
	"no-manifest":           {map[string]string{"rust/Cargo.toml": crate("widget", "2024")}, "the repository has no root Cargo.toml to read the crate editions from"},
	"manifest-not-a-file":   {map[string]string{"Cargo.toml/keep": ""}, "the root Cargo.toml cannot be read"},
	"glob-matching-nothing": {map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"crates/*\"]\n[workspace.package]\nedition = \"2021\"\n"}, `the workspace member pattern "crates/*" matches nothing`},
	"mixed-members": {map[string]string{
		"Cargo.toml": "[workspace]\nmembers = [\"a\", \"b\"]\n", "a/Cargo.toml": crate("a", "2021"), "b/Cargo.toml": crate("b", "2024"),
	}, "the workspace crates are on different editions (2021, 2024)"},
	"root-default-member-2021": {map[string]string{
		"Cargo.toml": crate("root", "") + "[workspace]\nmembers = [\"a\"]\n", "a/Cargo.toml": crate("a", "2021"),
	}, "the workspace crates are on different editions (2015, 2021)"},
	"member-without-manifest":   {map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"a\"]\n"}, "the workspace member a/Cargo.toml cannot be read"},
	"member-without-package":    {map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"a\"]\n", "a/Cargo.toml": "[lib]\n"}, "the workspace member a/Cargo.toml declares no [package]"},
	"inherits-undeclared":       {map[string]string{"Cargo.toml": "[package]\nname = \"widget\"\nedition = { workspace = true }\n"}, "the root package inherits an edition [workspace.package] does not declare"},
	"recursive-glob":            {map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"crates/**\"]\n"}, `the workspace member pattern "crates/**"`},
	"negated-class-glob":        {map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"crates/[!a]\"]\n"}, `the workspace member pattern "crates/[!a]"`},
	"glob-outside-the-root":     {map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"../*\"]\n"}, `the workspace member pattern "../*"`},
	"members-never-closed":      {map[string]string{"Cargo.toml": "[workspace]\nmembers = [\n  \"a\",\n"}, "workspace.members in Cargo.toml never closes its array"},
	"members-not-plain-strings": {map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"a\\qb\"]\n"}, "workspace.members in Cargo.toml is not an array of plain strings"},
}

// Negative: where no edition holds for every crate, apply writes no rustfmt.toml, --force
// included, and names the reason under the unmet templates.
func TestRustfmtApply_Negative_NoCommonEditionWithholdsTheScaffold(t *testing.T) {
	for name, tc := range withheldRustfmtWorkspaces {
		for _, force := range []bool{false, true} {
			report, got := applyRust(t, tc.files, force)
			if got != "" || slices.Contains(report.CreatedTemplates, "rustfmt.toml") {
				t.Errorf("%s, force %v: rustfmt.toml written: %q", name, force, got)
			}
			if len(report.UnmetTemplates) != 1 || !strings.HasPrefix(report.UnmetTemplates[0], "rustfmt.toml: "+tc.reason) {
				t.Errorf("%s, force %v: unmet %v, want the reason %q", name, force, report.UnmetTemplates, tc.reason)
			}
		}
	}
}

// Boundary: an earlier scaffold where no edition holds is kept byte for byte, not refreshed to a
// rendering that may disagree with cargo fmt on some crate, and apply names why it was kept
// under the unmet templates.
func TestRustfmtApply_Boundary_NoCommonEditionKeepsAnEarlierScaffold(t *testing.T) {
	for edition, text := range readRustfmtPriors(t) {
		for name, tc := range withheldRustfmtWorkspaces {
			files := maps.Clone(tc.files)
			files["rustfmt.toml"] = text
			report, got := applyRust(t, files, false)
			if got != text || len(report.RefreshedTemplates) > 0 || slices.Contains(report.SkippedTemplates, "rustfmt.toml") {
				t.Errorf("edition %s scaffold, %s: got %q, refreshed %v, skipped %v", edition, name, got,
					report.RefreshedTemplates, report.SkippedTemplates)
			}
			want := "rustfmt.toml: the earlier Praetor text there is kept: " + tc.reason
			if len(report.UnmetTemplates) != 1 || !strings.HasPrefix(report.UnmetTemplates[0], want) {
				t.Errorf("edition %s scaffold, %s: unmet %v, want %q", edition, name, report.UnmetTemplates, want)
			}
		}
	}
}

// Negative: an edited rustfmt.toml where no edition holds is the repository's configuration: it
// is kept and reported skipped, as anywhere else, with no unmet requirement to act on.
func TestRustfmtApply_Negative_NoCommonEditionSkipsAnEditedConfig(t *testing.T) {
	edited := rustfmtFor("2024") + "imports_granularity = \"Crate\"\n"
	for name, tc := range withheldRustfmtWorkspaces {
		files := maps.Clone(tc.files)
		files["rustfmt.toml"] = edited
		report, got := applyRust(t, files, false)
		if got != edited || len(report.UnmetTemplates) > 0 || !slices.Contains(report.SkippedTemplates, "rustfmt.toml") {
			t.Errorf("%s: got %q, unmet %v, skipped %v", name, got, report.UnmetTemplates, report.SkippedTemplates)
		}
	}
}

// Boundary: a root Cargo.toml of exactly the read limit (1 MiB) is read, and one byte more
// leaves the crate editions unknown, so the scaffold is withheld rather than written without
// an edition.
func TestRustfmtApply_Boundary_RootManifestSizeLimit(t *testing.T) {
	const limit = 1 << 20
	manifest := crate("widget", "2021") + "# "
	for _, size := range []int{limit, limit + 1} {
		padded := manifest + strings.Repeat("x", size-len(manifest)-1) + "\n"
		report, got := applyRust(t, map[string]string{"Cargo.toml": padded}, false)
		if size == limit && (got != rustfmtFor("2021") || len(report.UnmetTemplates) > 0) {
			t.Errorf("%d bytes: rustfmt.toml %q, unmet %v; want edition 2021", size, got, report.UnmetTemplates)
		}
		if size > limit && (got != "" || len(report.UnmetTemplates) != 1 ||
			!strings.HasPrefix(report.UnmetTemplates[0], "rustfmt.toml: the root Cargo.toml cannot be read")) {
			t.Errorf("%d bytes: rustfmt.toml %q, unmet %v; want withheld", size, got, report.UnmetTemplates)
		}
	}
}

// Positive: members patterns are matched below the repository, so a glob metacharacter in the
// checkout's own path ("ws[1]") does not turn the members into none.
func TestRustfmtApply_Positive_GlobBelowACheckoutPathWithMetacharacters(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "ws[1]")
	for name, body := range map[string]string{
		"Cargo.toml": "[workspace]\nmembers = [\"crates/*\"]\n", "crates/a/Cargo.toml": crate("a", "2021"),
	} {
		full := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := flavor.ApplyFlavor(t.Context(), repo, "rust-systems", false)
	if err != nil {
		t.Fatalf("apply rust-systems: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(repo, "rustfmt.toml"))
	if err != nil || string(got) != rustfmtFor("2021") {
		t.Errorf("rustfmt.toml %q (err %v), unmet %v; want edition 2021", got, err, report.UnmetTemplates)
	}
}

// readPriorFixtures returns every earlier scaffold text under dir by its file name, in LF: the
// texts were written with LF, and a Windows checkout under core.autocrlf may convert the
// fixtures, which the tests then turn into CRLF themselves where they mean to.
func readPriorFixtures(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	priors := make(map[string]string, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		priors[entry.Name()], _ = util.NormalizeLineEndings(string(data))
	}
	if len(priors) == 0 {
		t.Fatalf("%s holds no earlier scaffold", dir)
	}
	return priors
}

// assertPriorFixturesRecorded fails unless the texts fixtures (by file name, from dir) and the
// digests prior records (TemplateItem.Prior) are one set: each fixture's digest is recorded and
// each recorded digest has a fixture.
func assertPriorFixturesRecorded(t *testing.T, prior map[string]string, fixtures map[string]string, dir string) {
	t.Helper()
	seen := make(map[string]bool, len(prior))
	for name, text := range fixtures {
		digest, _, err := util.CanonicalTextDigest([]byte(text))
		if err != nil {
			t.Fatalf("%s/%s: %v", dir, name, err)
		}
		if _, ok := prior[digest]; !ok {
			t.Errorf("the earlier scaffold %s/%s (%s) is not a recorded earlier text", dir, name, digest)
		}
		seen[digest] = true
	}
	for digest, origin := range prior {
		if !seen[digest] {
			t.Errorf("recorded earlier text %s (%s) has no fixture under %s", digest, origin, dir)
		}
	}
}

// readRustfmtPriors returns every earlier rustfmt.toml scaffold text by the edition it named,
// in LF (readPriorFixtures).
func readRustfmtPriors(t *testing.T) map[string]string {
	t.Helper()
	fixtures := readPriorFixtures(t, rustfmtPriorFixtures)
	priors := make(map[string]string, len(fixtures))
	for name, text := range fixtures {
		priors[strings.TrimSuffix(strings.TrimPrefix(name, "edition-"), ".rustfmt.toml")] = text
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
	assertPriorFixturesRecorded(t, rustfmtTemplate(t).Prior, readPriorFixtures(t, rustfmtPriorFixtures), rustfmtPriorFixtures)
	for edition, text := range readRustfmtPriors(t) {
		rendered, err := templates.RenderFile("rust/rustfmt.toml.tmpl", templates.Context{RustEdition: edition})
		if err != nil || rendered != text {
			t.Errorf("edition %s renders %q (err %v), want the earlier text %q", edition, rendered, err, text)
		}
	}
}

// Positive: an unedited earlier scaffold is refreshed to the edition the crates share without
// --force, in its own line-ending style, and reported refreshed rather than created or skipped.
// A crate declaring no edition is on 2015, which rustfmt also formats under without one.
func TestRustfmtApply_Positive_RefreshesAnEarlierScaffold(t *testing.T) {
	workspaces := map[string]map[string]string{
		"2018": {"Cargo.toml": crate("widget", "2018")},
		"2015": {"Cargo.toml": "[workspace]\nmembers = [\"a\"]\n[workspace.package]\nedition = \"2015\"\n", "a/Cargo.toml": "[package]\nname = \"a\"\nedition.workspace = true\n"},
		"":     {"Cargo.toml": crate("widget", "")},
	}
	for edition, text := range readRustfmtPriors(t) {
		for crateEdition, files := range workspaces {
			for _, crlf := range []bool{false, true} {
				files := maps.Clone(files)
				files["rustfmt.toml"] = util.RestoreLineEndings(text, crlf)
				report, got := applyRust(t, files, false)
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

// Boundary: an earlier text that already is the rendering for the crates' edition stays as it
// is, a virtual workspace whose members declare that edition included, and one with mixed line
// endings is not an earlier text, so it is kept too.
func TestRustfmtApply_Boundary_EarlierTextOnTheCrateEditionOrMixedEndingsStays(t *testing.T) {
	current := rustfmtFor("2024")
	mixed := strings.Replace(current, "\n", "\r\n", 1)
	for name, files := range map[string]map[string]string{
		"already-current":   {"Cargo.toml": crate("widget", "2024"), "rustfmt.toml": current},
		"virtual-workspace": {"Cargo.toml": "[workspace]\nmembers = [\"a\"]\n", "a/Cargo.toml": crate("a", "2024"), "rustfmt.toml": current},
		"mixed-endings":     {"Cargo.toml": crate("widget", "2024"), "rustfmt.toml": mixed},
	} {
		report, got := applyRust(t, files, false)
		if got != files["rustfmt.toml"] || len(report.RefreshedTemplates) > 0 || !slices.Contains(report.SkippedTemplates, "rustfmt.toml") {
			t.Errorf("%s: got %q, refreshed %v, skipped %v", name, got, report.RefreshedTemplates, report.SkippedTemplates)
		}
	}
}

// Boundary: a workspace of maxCargoMembers (256) members is read to the last member, however many
// files the pattern also matches before it, and one more member leaves the members unknown, so
// the scaffold is withheld.
func TestRustfmtApply_Boundary_MemberCountLimit(t *testing.T) {
	for _, tc := range []struct {
		members, files int
		last, unmet    string
	}{
		{256, 0, "2021", ""},
		{256, 64, "2024", "different editions (2021, 2024)"},
		{257, 0, "2021", "more than 256 members"},
	} {
		files := map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"crates/*\"]\n"}
		for i := range tc.members {
			name, edition := fmt.Sprintf("c%03d", i), "2021"
			if i == tc.members-1 {
				edition = tc.last
			}
			files["crates/"+name+"/Cargo.toml"] = crate(name, edition)
		}
		// The files sort before every member directory.
		for i := range tc.files {
			files[fmt.Sprintf("crates/a%03d.md", i)] = "not a crate\n"
		}
		report, got := applyRust(t, files, false)
		if tc.unmet == "" && (got != rustfmtFor("2021") || len(report.UnmetTemplates) > 0) {
			t.Errorf("%+v: rustfmt.toml %q, unmet %v; want edition 2021", tc, got, report.UnmetTemplates)
		}
		if tc.unmet != "" && (got != "" || len(report.UnmetTemplates) != 1 || !strings.Contains(report.UnmetTemplates[0], tc.unmet)) {
			t.Errorf("%+v: rustfmt.toml %q, unmet %v; want withheld for %q", tc, got, report.UnmetTemplates, tc.unmet)
		}
	}
}
