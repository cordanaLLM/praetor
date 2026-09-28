package adopt

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

// priorCatalogFixtures holds, flat, every catalog text priorCatalogDigests names.
const priorCatalogFixtures = "testdata/catalog-prior"

// shippedCatalog is praetor's own .config/archetypes, the catalog adoption vendors.
var shippedCatalog = filepath.Join("..", "..", ".config", "archetypes")

func TestPriorCatalogDigests_Positive_ReproducedByFixtures(t *testing.T) {
	assertPriorDigestsReproduced(t, priorCatalogFixtures, priorCatalogDigests)
}

// Positive: every earlier text decodes to exactly the values of the current file it names, so
// re-pinning an unmodified earlier catalog changes its layout and never a policy value.
func TestPriorCatalogTexts_Positive_DecodeToTheShippedValues(t *testing.T) {
	for _, prior := range readFixtureDir(t, priorCatalogFixtures) {
		sum := sha256.Sum256(prior)
		rel := priorCatalogDigests[hex.EncodeToString(sum[:])]
		current, err := os.ReadFile(filepath.Join(shippedCatalog, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		var before, after any
		if err := yaml.Unmarshal(prior, &before); err != nil {
			t.Fatalf("%s: prior text: %v", rel, err)
		}
		if err := yaml.Unmarshal(current, &after); err != nil {
			t.Fatalf("%s: shipped text: %v", rel, err)
		}
		if !deepEqual(before, after) {
			t.Errorf("%s: the shipped text changed a value of the earlier one", rel)
		}
		if isPriorRendering(current, priorCatalogDigests) {
			t.Errorf("%s: the shipped text is still an earlier rendering", rel)
		}
	}
}

// catalogBodies reads the catalog text of framework and security:high from dir, which holds
// them flat (testdata/catalog-prior) or in the catalog layout (.config/archetypes).
func catalogBodies(t *testing.T, framework, facet string) map[string]string {
	t.Helper()
	return map[string]string{"framework": mustRead(t, framework), "security:high": mustRead(t, facet)}
}

// earlierCatalogRepo adopts the lock and catalog of a repository from a source bundle holding
// the earlier framework and security:high texts, as an earlier Praetor wrote them.
func earlierCatalogRepo(t *testing.T) (*adoptSession, *config.Manifest) {
	t.Helper()
	manifest := &config.Manifest{Version: 1, Profiles: []string{"framework"}, Facets: []string{"security:high"}}
	earlier := newCatalogLockSource(t, manifest, catalogBodies(t,
		filepath.Join(priorCatalogFixtures, "framework.yaml"), filepath.Join(priorCatalogFixtures, "security-high.yaml")))
	s := lockAdoptSession(t)
	mustWrite(t, filepath.Join(s.repoPath, manifestFile), "version: 1\nprofiles: [framework]\nfacets: [\"security:high\"]\n")
	s.opts.LockSourceRoot = earlier
	for _, step := range []adoptStep{reconcileLockfile, reconcilePolicyCatalog} {
		if err := step(t.Context(), s); err != nil {
			t.Fatalf("adopt the earlier catalog: %v", err)
		}
	}
	return s, manifest
}

// currentCatalogSource is a source bundle holding the shipped framework and security:high texts.
func currentCatalogSource(t *testing.T, manifest *config.Manifest) string {
	t.Helper()
	return newCatalogLockSource(t, manifest, catalogBodies(t,
		filepath.Join(shippedCatalog, "framework.yaml"), filepath.Join(shippedCatalog, "facets", "security-high.yaml")))
}

func runLockAndCatalog(t *testing.T, s *adoptSession) error {
	t.Helper()
	s.report = &AdoptReport{}
	for _, step := range []adoptStep{reconcileLockfile, reconcilePolicyCatalog} {
		if err := step(t.Context(), s); err != nil {
			return err
		}
	}
	return nil
}

// Positive and boundary: plain re-adoption against the current catalog re-pins a lock that
// pins only earlier Praetor texts and refreshes those files, and the run after it changes
// nothing.
func TestAdoptRepinsAnUnmodifiedEarlierCatalog(t *testing.T) {
	s, manifest := earlierCatalogRepo(t)
	s.opts.LockSourceRoot = currentCatalogSource(t, manifest)
	if err := runLockAndCatalog(t, s); err != nil {
		t.Fatalf("re-adoption of an unmodified earlier catalog must succeed: %v", err)
	}
	if detail := lastLockDetail(t, s); !strings.HasPrefix(detail, "Re-pinned an unmodified earlier Praetor catalog") {
		t.Errorf("re-pin not reported: %q", detail)
	}
	for rel, shipped := range map[string]string{"framework.yaml": "framework.yaml", "facets/security-high.yaml": "facets/security-high.yaml"} {
		got := mustRead(t, filepath.Join(s.repoPath, ".config", "archetypes", filepath.FromSlash(rel)))
		if got != mustRead(t, filepath.Join(shippedCatalog, filepath.FromSlash(shipped))) {
			t.Errorf("%s was not refreshed to the current text", rel)
		}
	}
	if _, err := config.ValidateLockfileWithOptions(t.Context(), config.LockValidationOptions{Root: s.repoPath, RequireSources: true}, manifest); err != nil {
		t.Fatalf("the re-pinned lock must verify against the refreshed local catalog: %v", err)
	}

	lock := mustRead(t, filepath.Join(s.repoPath, lockFile))
	if err := runLockAndCatalog(t, s); err != nil {
		t.Fatalf("second re-adoption: %v", err)
	}
	if detail := lastLockDetail(t, s); detail != "Verified existing version pins and content digests" {
		t.Errorf("boundary: a re-pinned lock must verify on the next run, got %q", detail)
	}
	if mustRead(t, filepath.Join(s.repoPath, lockFile)) != lock {
		t.Error("boundary: the next run rewrote the re-pinned lock")
	}
}

// Boundary: a dry run plans the re-pin a real run makes and writes nothing.
func TestAdoptDryRunPlansTheEarlierCatalogRepin(t *testing.T) {
	s, manifest := earlierCatalogRepo(t)
	before := snapshotTree(t, s.repoPath)
	s.opts.LockSourceRoot = currentCatalogSource(t, manifest)
	s.opts.DryRun = true
	if err := runLockAndCatalog(t, s); err != nil {
		t.Fatalf("a dry run of the re-pin must plan it: %v", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, s.repoPath))
}

// Negative: an edited catalog file, or a lock pinning texts Praetor never shipped, is the
// repository's own policy; re-adoption still refuses it without --force.
func TestAdoptDoesNotRepinAnEditedOrForeignCatalog(t *testing.T) {
	s, manifest := earlierCatalogRepo(t)
	framework := filepath.Join(s.repoPath, ".config", "archetypes", "framework.yaml")
	mustWrite(t, framework, mustRead(t, framework)+"# local note\n")
	s.opts.LockSourceRoot = currentCatalogSource(t, manifest)
	if err := runLockAndCatalog(t, s); !errors.Is(err, config.ErrLockDigestMismatch) {
		t.Errorf("an edited earlier catalog must keep failing, got %v", err)
	}

	foreign := lockAdoptSession(t)
	mustWrite(t, filepath.Join(foreign.repoPath, manifestFile), "version: 1\nprofiles: [framework]\nfacets: [\"security:high\"]\n")
	foreign.opts.LockSourceRoot = newCatalogLockSource(t, manifest, nil)
	if err := runLockAndCatalog(t, foreign); err != nil {
		t.Fatal(err)
	}
	foreign.opts.LockSourceRoot = currentCatalogSource(t, manifest)
	if err := runLockAndCatalog(t, foreign); !errors.Is(err, config.ErrLockDigestMismatch) {
		t.Errorf("a lock pinning texts Praetor never shipped must keep failing, got %v", err)
	}
}

// changedCatalogSource is a source bundle holding the shipped framework and security:high texts
// with the policy value old replaced by new in the file named by id.
func changedCatalogSource(t *testing.T, manifest *config.Manifest, id, old, replacement string) string {
	t.Helper()
	bodies := catalogBodies(t,
		filepath.Join(shippedCatalog, "framework.yaml"), filepath.Join(shippedCatalog, "facets", "security-high.yaml"))
	changed := strings.Replace(bodies[id], old, replacement, 1)
	if changed == bodies[id] {
		t.Fatalf("%s holds no %q to change", id, old)
	}
	bodies[id] = changed
	return newCatalogLockSource(t, manifest, bodies)
}

// Negative and boundary: a source that re-lays out an earlier catalog and also changes one
// policy value, in the profile or in only one facet, is not a layout-only successor. Plain
// re-adoption keeps refusing it and rewrites neither the lock nor any catalog file; a dry run
// refuses it the same way. Only --force takes up a changed value.
func TestAdoptDoesNotRepinAnEarlierCatalogToChangedValues(t *testing.T) {
	cases := map[string][3]string{
		"profile value": {"framework", "max_func_loc: 75", "max_func_loc: 400"},
		"facet value":   {"security:high", "required_approving_reviewers: 2", "required_approving_reviewers: 0"},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, manifest := earlierCatalogRepo(t)
			before := snapshotTree(t, s.repoPath)
			s.opts.LockSourceRoot = changedCatalogSource(t, manifest, change[0], change[1], change[2])
			if err := runLockAndCatalog(t, s); !errors.Is(err, config.ErrLockDigestMismatch) {
				t.Errorf("a changed %s must keep failing without --force, got %v", name, err)
			}
			assertTreeUnchanged(t, before, snapshotTree(t, s.repoPath))

			s.opts.DryRun = true
			if err := runLockAndCatalog(t, s); err == nil {
				t.Errorf("a dry run must refuse the changed %s too", name)
			}
			s.opts.DryRun, s.opts.Force = false, true
			if err := runLockAndCatalog(t, s); err != nil {
				t.Fatalf("--force must take up the changed %s: %v", name, err)
			}
		})
	}
}

// Negative and boundary: the catalog step on its own replaces an unmodified earlier text only
// with a text holding exactly its values. A changed value, or an earlier text a user edited, is
// refused without --force, and --force still replaces it.
func TestPrepareCatalogWritesReplacesEarlierTextsOnlyByLayout(t *testing.T) {
	prior := mustRead(t, filepath.Join(priorCatalogFixtures, "framework.yaml"))
	shipped := mustRead(t, filepath.Join(shippedCatalog, "framework.yaml"))
	changed := strings.Replace(shipped, "max_func_loc: 75", "max_func_loc: 400", 1)
	cases := []struct {
		name, onDisk, source string
		force, replaced      bool
	}{
		{"layout-only successor", prior, shipped, false, true},
		{"changed value", prior, changed, false, false},
		{"edited earlier text", prior + "# local note\n", shipped, false, false},
		{"forced value change", prior, changed, true, true},
	}
	for _, tc := range cases {
		s := lockAdoptSession(t)
		s.opts.Force = tc.force
		mustWrite(t, filepath.Join(s.repoPath, ".config", "archetypes", "framework.yaml"), tc.onDisk)
		artifact := config.PolicyArtifact{RelativePath: ".config/archetypes/framework.yaml",
			SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(tc.source))), Content: []byte(tc.source)}
		writes, err := prepareCatalogWrites(t.Context(), s, []config.PolicyArtifact{artifact})
		if tc.replaced && (err != nil || len(writes) != 1) {
			t.Errorf("%s: must be replaced, got %d writes, %v", tc.name, len(writes), err)
		}
		if !tc.replaced && err == nil {
			t.Errorf("%s: must be refused without --force", tc.name)
		}
	}
}
