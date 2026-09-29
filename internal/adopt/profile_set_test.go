package adopt

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// profileSetManifest is an operator-written manifest: comments, a blank line and a line comment
// on the facets key, all of which profile set must keep.
const profileSetManifest = "# Repository manifest\n" +
	"version: 1\n" +
	"repository:\n" +
	"  owner: acme\n" +
	"  name: widgets\n" +
	"\n" +
	"# The primary profile.\n" +
	"profiles:\n" +
	"  - framework\n" +
	"facets: # review floor\n" +
	"  - security:high\n"

// profileSetRepo is an adopted repository: a git work tree declaring profileSetManifest, with the
// lock and the vendored catalog adoption writes for it from source, which it returns too.
func profileSetRepo(t *testing.T) (root, source string) {
	t.Helper()
	root = t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, root, "")
	source = newAdoptLockSource(t)
	mustWrite(t, filepath.Join(root, manifestFile), profileSetManifest)
	s := &adoptSession{repoPath: root, report: &AdoptReport{}, opts: AdoptOptions{Path: root, LockSourceRoot: source}}
	for _, step := range []adoptStep{reconcileLockfile, reconcilePolicyCatalog} {
		if err := step(t.Context(), s); err != nil {
			t.Fatalf("adopt the fixture lock and catalog: %v", err)
		}
	}
	return root, source
}

// changedPaths lists, sorted, every repository path whose entry differs between two
// snapshotTree results, the private ledger (backups) and git metadata aside.
func changedPaths(before, after map[string]string) []string {
	changed := make([]string, 0)
	seen := make(map[string]bool, len(before)+len(after))
	for _, snap := range []map[string]string{before, after} {
		for rel := range snap {
			slash := filepath.ToSlash(rel)
			if seen[slash] || underDir(slash, ".git") || underDir(slash, workingDirPath) {
				continue
			}
			seen[slash] = true
			if before[rel] != after[rel] {
				changed = append(changed, slash)
			}
		}
	}
	sort.Strings(changed)
	return changed
}

// underDir reports whether the slash path rel is dir or lies below it.
func underDir(rel, dir string) bool {
	return rel == dir || strings.HasPrefix(rel, dir+"/")
}

// assertDeclared decodes root's manifest, requires it to declare profiles and facets, and
// requires the lock to verify against the catalog root vendors, with no source bundle.
func assertDeclared(t *testing.T, root string, profiles, facets []string) {
	t.Helper()
	manifest, err := decodeTargetManifest([]byte(mustRead(t, filepath.Join(root, manifestFile))))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(manifest.Profiles, profiles) || !slices.Equal(manifest.Facets, facets) {
		t.Fatalf("declared profiles %v facets %v, want %v %v", manifest.Profiles, manifest.Facets, profiles, facets)
	}
	result, err := config.ValidateLockfileWithOptions(t.Context(), config.LockValidationOptions{Root: root, RequireSources: true}, manifest)
	if err != nil || result.Profiles != len(profiles) || result.Facets != len(facets) {
		t.Fatalf("the lock must pin the declaration against the vendored catalog: %+v, %v", result, err)
	}
}

// Positive (#123): changing the profile rewrites the profiles list, rebuilds the lock and vendors
// the new archetype, and every other manifest line, comments included, stays as written.
// Boundary: those three are the only paths that change, and a second identical run changes none.
func TestSetProfile_Positive_ChangesTheProfileAndNothingElse(t *testing.T) {
	root, source := profileSetRepo(t)
	before := snapshotTree(t, root)
	rep, err := SetProfile(t.Context(), ProfileSetOptions{Path: root, Profile: "os-image", LockSourceRoot: source})
	if err != nil || len(rep.Errors) != 0 {
		t.Fatalf("profile set: %v %v", err, rep)
	}
	want := []string{manifestFile, lockFile, ".config/archetypes/os-image.yaml"}
	sort.Strings(want)
	if got := changedPaths(before, snapshotTree(t, root)); !slices.Equal(got, want) {
		t.Fatalf("changed %v, want exactly %v", got, want)
	}
	if got := mustRead(t, filepath.Join(root, manifestFile)); got != strings.Replace(profileSetManifest, "  - framework\n", "  - os-image\n", 1) {
		t.Fatalf("only the profiles list may change:\n%s", got)
	}
	assertDeclared(t, root, []string{"os-image"}, []string{"security:high"})
	if rep.Archetype != "os-image" || rep.EffectivePolicy == nil || len(rep.Replaced()) != 1 || rep.Replaced()[0].Path != lockFile {
		t.Fatalf("report: archetype %q, policy %v, replaced %+v", rep.Archetype, rep.EffectivePolicy != nil, rep.Replaced())
	}

	settled := snapshotTree(t, root)
	if _, err := SetProfile(t.Context(), ProfileSetOptions{Path: root, Profile: "os-image", LockSourceRoot: source}); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := changedPaths(settled, snapshotTree(t, root)); len(got) != 0 {
		t.Fatalf("an unchanged declaration and source rewrote %v", got)
	}
}

// Positive, over a full adoption: the change touches the declaration files alone, never the
// hooks, rulesets, personas or editor files adoption wrote beside them (#123).
func TestSetProfile_Positive_LeavesAnAdoptedTreeAlone(t *testing.T) {
	repo := newTestRepo(t, "profiles")
	source := newAdoptLockSource(t)
	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: source, Path: repo, Profile: "framework",
		Facets: []string{"security:high"}, SkipHookActivation: true}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	before := snapshotTree(t, repo)
	if _, err := SetProfile(t.Context(), ProfileSetOptions{Path: repo, Profile: "os-image", LockSourceRoot: source}); err != nil {
		t.Fatalf("profile set: %v", err)
	}
	want := []string{manifestFile, lockFile, ".config/archetypes/os-image.yaml"}
	sort.Strings(want)
	if got := changedPaths(before, snapshotTree(t, repo)); !slices.Equal(got, want) {
		t.Fatalf("changed %v, want exactly %v", got, want)
	}
	assertDeclared(t, repo, []string{"os-image"}, []string{"security:high"})
}

// Negative: every refusal comes before the first write, so the tree is left as it was. A profile
// the source bundle lacks names the bundle's catalog version (#123); no source, no manifest, a
// malformed id and a repeated one are refused too.
func TestSetProfile_Negative_RefusalsWriteNothing(t *testing.T) {
	root, source := profileSetRepo(t)
	before := snapshotTree(t, root)
	for _, tc := range []struct {
		name string
		opts ProfileSetOptions
		want error
		text string
	}{
		{"absent profile", ProfileSetOptions{Profile: "web-package", LockSourceRoot: source}, config.ErrLockSourceMissing, "v0.0.0+catalog."},
		{"no source", ProfileSetOptions{Profile: "os-image"}, ErrLockSourceRequired, ""},
		{"spaced facet", ProfileSetOptions{Facets: []string{"security: high"}, SetFacets: true, LockSourceRoot: source}, nil, "is not an id"},
		{"repeated facet", ProfileSetOptions{Facets: []string{"security:high", "security:high"}, SetFacets: true, LockSourceRoot: source}, nil, "listed twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Path = root
			_, err := SetProfile(t.Context(), tc.opts)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("got %v, want %v containing %q", err, tc.want, tc.text)
			}
			if got := changedPaths(before, snapshotTree(t, root)); len(got) != 0 {
				t.Fatalf("a refused change wrote %v", got)
			}
		})
	}
	bare := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, bare, "")
	if _, err := SetProfile(t.Context(), ProfileSetOptions{Path: bare, Profile: "os-image", LockSourceRoot: source}); !errors.Is(err, ErrNotAdopted) {
		t.Fatalf("a repository without a manifest must be refused: %v", err)
	}
}

// Boundary: an explicitly empty facet list declares none, distinct from keeping the declared
// facets; the facet file stays vendored. A dry run writes nothing and previews the diff of every
// file a real run changes.
func TestSetProfile_Boundary_EmptyFacetsAndDryRun(t *testing.T) {
	root, source := profileSetRepo(t)
	before := snapshotTree(t, root)
	rep, err := SetProfile(t.Context(), ProfileSetOptions{Path: root, Profile: "os-image", SetFacets: true, LockSourceRoot: source, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := changedPaths(before, snapshotTree(t, root)); len(got) != 0 {
		t.Fatalf("a dry run wrote %v", got)
	}
	previewed := make([]string, 0, len(rep.Previews))
	for _, preview := range rep.Previews {
		previewed = append(previewed, preview.Path+":"+string(preview.Action))
	}
	sort.Strings(previewed)
	if want := []string{".config/archetypes/os-image.yaml:create", lockFile + ":update", manifestFile + ":update"}; !slices.Equal(previewed, want) {
		t.Fatalf("previews %v, want %v", previewed, want)
	}

	if _, err := SetProfile(t.Context(), ProfileSetOptions{Path: root, SetFacets: true, LockSourceRoot: source}); err != nil {
		t.Fatalf("declare no facets: %v", err)
	}
	assertDeclared(t, root, []string{"framework"}, nil)
	if got := mustRead(t, filepath.Join(root, manifestFile)); !strings.HasSuffix(got, "facets: [] # review floor\n") {
		t.Fatalf("an empty facet list must be declared explicitly:\n%s", got)
	}
	mustRead(t, filepath.Join(root, ".config", "archetypes", "facets", "security-high.yaml"))
}

// Positive (#123 comment): with the declaration unchanged, profile set re-pins the lock and
// replaces the vendored texts whose values the newer source changes, without --force and without
// touching the manifest.
func TestSetProfile_Positive_RepinsAChangedCatalog(t *testing.T) {
	root, _ := profileSetRepo(t)
	declared := &config.Manifest{Version: 1, Profiles: []string{"framework"}, Facets: []string{"security:high"}}
	newer := newCatalogLockSource(t, declared, map[string]string{
		"framework": "id: framework\nname: Framework\ndescription: a value the newer catalog adds\n",
	})
	manifest := mustRead(t, filepath.Join(root, manifestFile))
	before := snapshotTree(t, root)
	rep, err := SetProfile(t.Context(), ProfileSetOptions{Path: root, LockSourceRoot: newer})
	if err != nil {
		t.Fatalf("re-pin: %v", err)
	}
	if got := changedPaths(before, snapshotTree(t, root)); !slices.Contains(got, lockFile) ||
		!slices.Contains(got, ".config/archetypes/framework.yaml") || slices.Contains(got, manifestFile) {
		t.Fatalf("a re-pin must rewrite the lock and the changed text only: %v", got)
	}
	if mustRead(t, filepath.Join(root, manifestFile)) != manifest {
		t.Fatal("a re-pin rewrote the manifest")
	}
	assertDeclared(t, root, []string{"framework"}, []string{"security:high"})
	if detail := actionDetail(rep, manifestFile, actionReconcile); !strings.HasPrefix(detail, "Declaration unchanged") {
		t.Fatalf("the manifest must be reported unchanged: %q", detail)
	}
}
