package adopt

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// priorCheckpointFixtures holds, per checkpoint bundle file, every text a Praetor release
// shipped at it, in a directory named after the file.
const priorCheckpointFixtures = "testdata/checkpoint"

// checkpointFixtureDir is the fixture directory of the bundle file at rel.
func checkpointFixtureDir(rel string) string {
	return filepath.Join(priorCheckpointFixtures, strings.TrimSuffix(path.Base(rel), ".py"))
}

// firstPriorCheckpointText returns the oldest recorded text of the bundle file at rel, which is
// never the current one.
func firstPriorCheckpointText(t *testing.T, rel string) string {
	t.Helper()
	return string(readFixtureDir(t, checkpointFixtureDir(rel))["promote-14."+path.Base(rel)])
}

// Each bundle file's digest set is replayable in both directions against its fixtures.
func TestPriorCheckpointDigests_Positive_ReproducedByFixtures(t *testing.T) {
	for _, rel := range []string{checkpointScript, checkpointCommon} {
		assertPriorDigestsReproduced(t, checkpointFixtureDir(rel), priorCheckpointDigests[rel])
	}
}

// Boundary: the bundle this repository ships is recorded, so the release that changes a script
// still refreshes the one adopters hold; --force no longer does. After changing one, copy it
// under testdata/checkpoint and add its digest to priorCheckpointDigests.
func TestPriorCheckpointDigests_Boundary_CurrentSourcesRecorded(t *testing.T) {
	if len(priorCheckpointDigests) != 2 {
		t.Fatalf("priorCheckpointDigests covers %d paths, want the 2 bundle files", len(priorCheckpointDigests))
	}
	for _, rel := range []string{checkpointScript, checkpointCommon} {
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !isPriorRendering(data, priorCheckpointDigests[rel]) {
			t.Errorf("the shipped %s (%s) is not in priorCheckpointDigests", rel, fixtureDigest(t, rel, data))
		}
	}
}

// Positive (#239): an unedited earlier checkpoint.py, and its CRLF checkout, is refreshed to the
// verified source on a plain run, in its own line-ending style, and the lifecycle is ready.
func TestReconcileCheckpointBundle_Positive_PriorScriptRefreshedOnPlainRun(t *testing.T) {
	source := checkpointSourceFixture(t, true)
	want := mustRead(t, filepath.Join(source, filepath.FromSlash(checkpointScript)))
	prior := firstPriorCheckpointText(t, checkpointScript)
	for style, existing := range map[string]string{"lf": prior, "crlf": crlfText(prior)} {
		session := checkpointSession(t, source)
		mustWrite(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript)), existing)
		ready, err := reconcileCheckpointBundle(context.Background(), session, false)
		if err != nil || !ready {
			t.Fatalf("%s: ready=%v err=%v", style, ready, err)
		}
		expected := want
		if style == "crlf" {
			expected = crlfText(want)
		}
		if got := mustRead(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript))); got != expected {
			t.Errorf("%s: earlier checkpoint.py not refreshed to the source:\n%q", style, got)
		}
		if !strings.Contains(findActionDetail(session.report.ActionDetails, checkpointScript), "Refreshed an unedited earlier") {
			t.Errorf("%s: refresh not reported: %+v", style, session.report.ActionDetails)
		}
	}
}

// Negative: an earlier checkpoint.py beside an edited common.py is not refreshed either: the
// bundle is one unit, so nothing of it is written beside a file adoption keeps.
func TestReconcileCheckpointBundle_Negative_NoHalfRefreshBesideAKeptFile(t *testing.T) {
	session := checkpointSession(t, checkpointSourceFixture(t, true))
	session.opts.Force = true
	prior := firstPriorCheckpointText(t, checkpointScript)
	edited := firstPriorCheckpointText(t, checkpointCommon) + "# repository note\n"
	mustWrite(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript)), prior)
	mustWrite(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointCommon)), edited)
	ready, err := reconcileCheckpointBundle(context.Background(), session, false)
	if err == nil || ready || !strings.Contains(err.Error(), checkpointCommon+" differs") {
		t.Fatalf("ready=%v err=%v, want the kept common.py named", ready, err)
	}
	if got := mustRead(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript))); got != prior {
		t.Error("checkpoint.py was refreshed beside a kept common.py")
	}
	if got := mustRead(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointCommon))); got != edited {
		t.Error("--force replaced an edited common.py")
	}
}

// Boundary: beside the canonical hook policy an unedited earlier script belongs to the vendored
// bundle and is kept, never refreshed.
func TestReconcileCheckpointBundle_Boundary_VendoredPriorScriptKept(t *testing.T) {
	session := checkpointSession(t, checkpointSourceFixture(t, true))
	prior := firstPriorCheckpointText(t, checkpointScript)
	mustWrite(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript)), prior)
	if ready, err := reconcileCheckpointBundle(context.Background(), session, true); err != nil || !ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	if got := mustRead(t, filepath.Join(session.repoPath, filepath.FromSlash(checkpointScript))); got != prior {
		t.Error("a vendored checkpoint.py was refreshed")
	}
}
