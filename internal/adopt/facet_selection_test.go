package adopt

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// declaredFacetsManifest declares the framework profile and facets for repository name.
func declaredFacetsManifest(name, facets string) string {
	return "version: 1\nrepository:\n  owner: acme\n  name: " + name + "\nprofiles:\n  - framework\n" + facets
}

// TestAdoptionFacets_3D: an existing manifest's declaration stands over --facets, an empty one
// included (positive); without one, --facets names the facets and the defaults stand in for
// none (negative: neither is reported as declared); an unreadable manifest resolves no facets
// and no origin rather than the defaults a first adoption declares (boundary).
func TestAdoptionFacets_3D(t *testing.T) {
	cases := []struct {
		name       string
		declared   *config.Manifest
		unreadable bool
		requested  []string
		want       []string
		origin     FacetOrigin
	}{
		{"declared over flag", &config.Manifest{Facets: []string{"security:high"}}, false, []string{"docs:seo-portal"}, []string{"security:high"}, FacetsDeclared},
		{"declared empty", &config.Manifest{}, false, []string{"docs:seo-portal"}, nil, FacetsDeclared},
		{"requested", nil, false, []string{"docs:seo-portal"}, []string{"docs:seo-portal"}, FacetsRequested},
		{"defaulted", nil, false, nil, config.DefaultFacets(), FacetsDefaulted},
		{"unreadable", nil, true, []string{"docs:seo-portal"}, nil, ""},
	}
	for _, tc := range cases {
		got, origin := adoptionFacets(tc.declared, tc.unreadable, tc.requested)
		if !slices.Equal(got, tc.want) || origin != tc.origin {
			t.Errorf("%s: adoptionFacets = %v (%q), want %v (%q)", tc.name, got, origin, tc.want, tc.origin)
		}
	}
}

// TestAdopt_Positive_ReportsTheDeclaredFacets (#592): with a manifest declaring custom:facet,
// the report shows custom:facet with or without --facets, adds no default note, and plans no
// documentation gate for a --facets docs:seo-portal the run never applies.
func TestAdopt_Positive_ReportsTheDeclaredFacets(t *testing.T) {
	for _, requested := range [][]string{nil, {"docs:seo-portal"}} {
		repo := newTestRepo(t, "declared-facets")
		mustWrite(t, filepath.Join(repo, manifestFile), declaredFacetsManifest("declared-facets", "facets:\n  - custom:facet\n"))
		report, err := Adopt(t.Context(), AdoptOptions{Path: repo, Facets: requested, DryRun: true, SkipGitValidation: true})
		if err != nil {
			t.Fatalf("adopt --facets=%v: %v", requested, err)
		}
		if !slices.Equal(report.Facets, []string{"custom:facet"}) || report.FacetOrigin != FacetsDeclared || len(report.FacetNotes) != 0 {
			t.Fatalf("--facets=%v reported %v (%q, notes %v), want the declared [custom:facet]",
				requested, report.Facets, report.FacetOrigin, report.FacetNotes)
		}
		if contains(report.CreatedFiles, DocumentationWorkflowFile) {
			t.Fatalf("--facets=%v planned the documentation gate the manifest does not declare", requested)
		}
	}
}

// TestAdopt_Negative_WarnsAboutIgnoredFacets (#592): a --facets that differs from the declared
// facets, an explicit empty one included, is ignored with a warning naming both lists and the
// profile set command that changes them; one naming the declared list warns nothing.
func TestAdopt_Negative_WarnsAboutIgnoredFacets(t *testing.T) {
	cases := []struct {
		opts AdoptOptions
		want string
	}{
		{AdoptOptions{Facets: []string{"docs:seo-portal"}}, "--facets=docs:seo-portal ignored: .standards.yaml declares facets [custom:facet], " +
			"and adoption never rewrites declared facets; change them with praetorctl profile set --facets=docs:seo-portal --lock-source-root=<praetor checkout>"},
		{AdoptOptions{SetFacets: true}, "--facets= ignored: .standards.yaml declares facets [custom:facet]"},
		{AdoptOptions{Facets: []string{"custom:facet"}, SetFacets: true}, ""},
	}
	for _, tc := range cases {
		repo := newTestRepo(t, "ignored-facets")
		mustWrite(t, filepath.Join(repo, manifestFile), declaredFacetsManifest("ignored-facets", "facets:\n  - custom:facet\n"))
		tc.opts.Path, tc.opts.DryRun, tc.opts.SkipGitValidation = repo, true, true
		report, err := Adopt(t.Context(), tc.opts)
		if err != nil {
			t.Fatal(err)
		}
		warnings := strings.Join(report.Warnings, "\n")
		if tc.want == "" && strings.Contains(warnings, "--facets") || tc.want != "" && !strings.Contains(warnings, tc.want) {
			t.Fatalf("--facets=%v (set %t): warnings %q, want %q", tc.opts.Facets, tc.opts.SetFacets, warnings, tc.want)
		}
	}
}

// TestAdopt_Positive_DefaultFacetsSayWhatEachRaises (#596): a first adoption without --facets
// reports each default facet as a default, names what each one raises over the profile alone
// from the pinned catalog, and names the commands that choose others.
func TestAdopt_Positive_DefaultFacetsSayWhatEachRaises(t *testing.T) {
	source := newCatalogLockSource(t, &config.Manifest{Version: 1, Profiles: []string{"template-seed"}, Facets: config.DefaultFacets()},
		map[string]string{"security:high": "id: \"security:high\"\nbranch_protection:\n  required_approving_reviewers: 2\nsupply_chain:\n  slsa_level: 3\n"})
	report, err := Adopt(t.Context(), AdoptOptions{Path: newTestRepo(t, "default-facets"), LockSourceRoot: source, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.FacetOrigin != FacetsDefaulted || !slices.Equal(report.Facets, config.DefaultFacets()) {
		t.Fatalf("a first adoption without --facets must report the defaults as such: %v (%q)", report.Facets, report.FacetOrigin)
	}
	want := []string{
		"default facets: --facets was omitted, so adoption declares " + strings.Join(config.DefaultFacets(), ", ") + " in the .standards.yaml it creates",
		"security:high raises over template-seed alone: branch_protection.required_approving_reviewers 1 -> 2, supply_chain.slsa_level 1 -> 3",
		"api:public-contract raises over template-seed alone: no branch-protection or supply-chain setting",
		"docs:seo-portal raises over template-seed alone: no branch-protection or supply-chain setting",
		"agent:sandboxed raises over template-seed alone: no branch-protection or supply-chain setting",
		"choose other facets with --facets=<id>,... on a first adoption, or afterwards with praetorctl profile set --facets=<id>,... " +
			"--lock-source-root=" + source + " (--facets= declares none)",
	}
	if !slices.Equal(report.FacetNotes, want) {
		t.Fatalf("default facet notes:\n%s\nwant:\n%s", strings.Join(report.FacetNotes, "\n"), strings.Join(want, "\n"))
	}
}

// TestAdopt_Negative_RequestedFacetsCarryNoDefaultNote (#596): facets --facets names are no
// default, so the report adds no default note.
func TestAdopt_Negative_RequestedFacetsCarryNoDefaultNote(t *testing.T) {
	report, err := Adopt(t.Context(), AdoptOptions{Path: newTestRepo(t, "requested-facets"), LockSourceRoot: newAdoptLockSource(t),
		Facets: []string{"custom:facet"}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.FacetOrigin != FacetsRequested || len(report.FacetNotes) != 0 {
		t.Fatalf("requested facets carried a default note: %q %v", report.FacetOrigin, report.FacetNotes)
	}
}

// TestAdopt_Boundary_EmptyFacetsFlagAndNoPolicy (#596): an explicitly empty --facets is told
// apart from an omitted one, and a run that resolved no pinned policy says why it lists no
// effects instead of listing none.
func TestAdopt_Boundary_EmptyFacetsFlagAndNoPolicy(t *testing.T) {
	report, err := Adopt(t.Context(), AdoptOptions{Path: newTestRepo(t, "empty-facets"), SetFacets: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.FacetOrigin != FacetsDefaulted || len(report.FacetNotes) != 3 {
		t.Fatalf("an empty --facets must still report the defaults with three notes: %q %v", report.FacetOrigin, report.FacetNotes)
	}
	if !strings.HasPrefix(report.FacetNotes[0], "default facets: --facets named no facet, so adoption declares ") ||
		report.FacetNotes[1] != "what each facet raises is not listed: the run resolved no pinned policy to read it from" {
		t.Fatalf("empty --facets without a pinned policy: %v", report.FacetNotes)
	}
}

// TestProfileSetCommand_3D: the command names the selected source bundle (positive), a
// placeholder when none was selected (negative), and no stray argument for empty args
// (boundary), the form the lock-mismatch remedy prints.
func TestProfileSetCommand_3D(t *testing.T) {
	cases := map[string][2]string{
		"praetorctl profile set os-image --lock-source-root=/src":                {"os-image", "/src"},
		"praetorctl profile set --facets= --lock-source-root=<praetor checkout>": {"--facets=", ""},
		"praetorctl profile set --lock-source-root=/src":                         {"", "/src"},
	}
	for want, in := range cases {
		if got := profileSetCommand(in[0], in[1]); got != want {
			t.Errorf("profileSetCommand(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
