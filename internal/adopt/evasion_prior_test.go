package adopt

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// priorEvasionFixtures holds every interceptor rendering a Praetor release wrote, one file per
// digest of priorEvasionHookDigests.
const priorEvasionFixtures = "testdata/evasion"

// currentEvasionFixture is the fixture holding the current rendering.
const currentEvasionFixture = "current-rendering.block_evasion.py"

// updateEvasionRenderingFixtures records the current interceptor and preserves its outgoing
// bytes under their digest. The emitted-fixture update flag also records the new digest in
// hooks.go, so refreshing emitted fixtures cannot silently drop an adopter's earlier text.
func updateEvasionRenderingFixtures(t *testing.T, current string) {
	t.Helper()
	path := filepath.Join(priorEvasionFixtures, currentEvasionFixture)
	previous := mustRead(t, path)
	if strings.ReplaceAll(previous, "\r\n", "\n") != current {
		digest := fixtureDigest(t, path, []byte(previous))
		archive := filepath.Join(priorEvasionFixtures, digest+".block_evasion.py")
		if existing, err := os.ReadFile(archive); err == nil {
			if strings.ReplaceAll(string(existing), "\r\n", "\n") != strings.ReplaceAll(previous, "\r\n", "\n") {
				t.Fatalf("outgoing rendering archive %s differs from its recorded bytes", archive)
			}
		} else if os.IsNotExist(err) {
			mustWrite(t, archive, previous)
		} else {
			t.Fatalf("read outgoing rendering archive %s: %v", archive, err)
		}
		mustWrite(t, path, current)
	}
	digest := fixtureDigest(t, "current rendering", []byte(current))
	if _, recorded := priorEvasionHookDigests[digest]; recorded {
		return
	}
	const declaration = "var priorEvasionHookDigests = map[string]string{\n"
	source := mustRead(t, "hooks.go")
	if strings.Count(source, declaration) != 1 {
		t.Fatal("hooks.go must contain exactly one priorEvasionHookDigests declaration")
	}
	const origin = "recorded Python hook rendering"
	entry := "\t\"" + digest + "\": \"" + origin + "\",\n"
	mustWrite(t, "hooks.go", strings.Replace(source, declaration, declaration+entry, 1))
	priorEvasionHookDigests[digest] = origin
}

// adoptEvasionFixture adopts a fresh repository whose interceptor holds existing.
func adoptEvasionFixture(t *testing.T, name, existing string, force bool) (string, *AdoptReport) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(evasionHookFile)), existing)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: force})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	return repoPath, rep
}

// The digest set is replayable in both directions: every fixture is a recognised digest and
// every digest has a fixture.
func TestPriorEvasionHookDigests_Positive_ReproducedByFixtures(t *testing.T) {
	assertPriorDigestsReproduced(t, priorEvasionFixtures, priorEvasionHookDigests)
}

// Boundary: the current rendering is recorded, so the release that changes it still refreshes
// it; --force no longer does. After a template or engine rule change, regenerate the emitted
// fixtures with PRAETOR_UPDATE_EMITTED_FIXTURES=1 to preserve the outgoing text and record the
// new rendering under testdata/evasion and its digest in priorEvasionHookDigests.
func TestPriorEvasionHookDigests_Boundary_CurrentRenderingRecorded(t *testing.T) {
	current := []byte(buildBlockEvasionPY())
	if !isPriorRendering(current, priorEvasionHookDigests) {
		t.Fatalf("the current interceptor rendering (%s) is not in priorEvasionHookDigests",
			fixtureDigest(t, "current rendering", current))
	}
	if got := readFixtureDir(t, priorEvasionFixtures)[currentEvasionFixture]; string(got) != string(current) {
		t.Fatalf("%s/%s is not the current rendering", priorEvasionFixtures, currentEvasionFixture)
	}
}

// Positive: every earlier rendering, and its CRLF checkout, is refreshed to the current one on a
// plain run, in the file's own line-ending style, and reported as a refresh, not a create or a
// replace. The current rendering is verified, never refreshed.
func TestAdopt_Positive_PriorEvasionHookRefreshedOnPlainRun(t *testing.T) {
	current := buildBlockEvasionPY()
	fixtures := readFixtureDir(t, priorEvasionFixtures)
	names := slices.Sorted(maps.Keys(fixtures))
	for _, name := range names {
		data := fixtures[name]
		for _, style := range []string{"lf", "crlf"} {
			existing := string(data)
			if style == "crlf" {
				existing = crlfText(existing)
			}
			// The first fixture is refreshed by a whole adoption, every other one by the
			// interceptor step alone (adoptOrStep, office-w19a-c1-adopt).
			repoPath := newTestRepo(t, "evasion-"+style)
			mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(evasionHookFile)), existing)
			rep, err := adoptOrStep(t, AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath},
				name == names[0] && style == "lf", func(ctx context.Context, s *adoptSession) error { return reconcileEvasionHook(ctx, s, false) })
			if err != nil {
				t.Fatalf("%s (%s): %v", name, style, err)
			}
			assertNoIssues(t, rep)
			want, detail := current, "Refreshed an unedited earlier"
			if style == "crlf" {
				want = crlfText(current)
			}
			if name == currentEvasionFixture {
				want, detail = existing, "verified present"
			}
			if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(evasionHookFile))); got != want {
				t.Errorf("%s (%s): interceptor not refreshed to the current rendering", name, style)
			}
			if got := findActionDetail(rep.ActionDetails, evasionHookFile); !strings.Contains(got, detail) {
				t.Errorf("%s (%s): action detail %q, want %q", name, style, got, detail)
			}
		}
	}
}

// Negative: an edited interceptor is kept byte for byte under --force, reported with the
// not-audit-verified note and a warning, and neither created nor replaced.
func TestAdopt_Negative_EditedEvasionHookKeptUnderForce(t *testing.T) {
	edited := strings.Replace(buildBlockEvasionPY(), "BLOCK_EXIT = 2", "BLOCK_EXIT = 2  # repository note", 1)
	repoPath, rep := adoptEvasionFixture(t, "evasion-edited", edited, true)
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(evasionHookFile))); got != edited {
		t.Fatal("--force replaced an edited interceptor")
	}
	if contains(rep.CreatedFiles, evasionHookFile) || hasAction(rep, evasionHookFile, actionReplace) ||
		!hasAction(rep, evasionHookFile, actionReconcile) {
		t.Fatalf("want a kept reconcile entry, got created=%v actions=%+v", rep.CreatedFiles, rep.ActionDetails)
	}
	detail := findActionDetail(rep.ActionDetails, evasionHookFile)
	if !strings.Contains(detail, "(-1/+1 lines); not audit-verified; kept") {
		t.Errorf("drift note %q", detail)
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), evasionHookFile+": existing file differs") {
		t.Errorf("no drift warning: %v", rep.Warnings)
	}
}

// Boundary: beside the canonical hook policy the interceptor belongs to that vendored bundle, so
// even an unedited earlier rendering is kept. An interceptor the snapshot reader refuses, here a
// symlink to an earlier rendering inside the repository, is reported unverified and adoption goes
// on, as it did before the earlier-text set existed; the text it points to stays.
func TestReconcileEvasionHook_Boundary_VendoredPriorKeptAndUnreadableUnverified(t *testing.T) {
	prior := string(readFixtureDir(t, priorEvasionFixtures)["engine-rules.block_evasion.py"])
	s := &adoptSession{repoPath: t.TempDir(), report: &AdoptReport{}}
	path := filepath.Join(s.repoPath, filepath.FromSlash(evasionHookFile))
	mustWrite(t, path, prior)
	if err := reconcileEvasionHook(t.Context(), s, true); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != prior {
		t.Fatal("a vendored interceptor was refreshed")
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs developer mode on Windows; the vendored half ran")
	}
	target := filepath.Join(filepath.Dir(path), "interceptor.py")
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("interceptor.py", path); err != nil {
		t.Fatal(err)
	}
	s.report = &AdoptReport{}
	if err := reconcileEvasionHook(t.Context(), s, false); err != nil {
		t.Fatalf("an unreadable interceptor failed adoption: %v", err)
	}
	if got := mustRead(t, target); got != prior || !strings.Contains(findActionDetail(s.report.ActionDetails, evasionHookFile), "unverified") {
		t.Fatalf("symlinked interceptor: target changed or not reported unverified: %+v", s.report.ActionDetails)
	}
}
