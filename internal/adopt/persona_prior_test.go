package adopt

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// priorPersonaFixtures holds, per canonical persona, every text a Praetor release wrote at it,
// in a directory named after the persona file. The fixtures carry a .prior suffix: the earlier
// texts fail the markdownlint defaults the Markdown gate applies to every tracked .md file.
const priorPersonaFixtures = "testdata/personas"

// personaFixtureDir is the fixture directory of the persona at rel.
func personaFixtureDir(rel string) string {
	return filepath.Join(priorPersonaFixtures, strings.TrimSuffix(path.Base(rel), ".md"))
}

// adoptPersonaFixture adopts a fresh repository whose persona at rel holds existing.
func adoptPersonaFixture(t *testing.T, rel, existing string, force bool) (string, *AdoptReport) {
	t.Helper()
	repoPath := newTestRepo(t, "persona")
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(rel)), existing)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: force})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	return repoPath, rep
}

// Each persona's digest set is replayable in both directions against its fixture directory.
func TestPriorPersonaDigests_Positive_ReproducedByFixtures(t *testing.T) {
	for _, persona := range generatedPersonas() {
		assertPriorDigestsReproduced(t, personaFixtureDir(persona.rel), priorPersonaDigests[persona.rel])
	}
}

// Boundary: every current persona text is recorded, so the release that changes it still
// refreshes it; --force no longer does. After changing a persona, add its new text under
// testdata/personas and its digest to priorPersonaDigests. Only the two generated personas have
// a set.
func TestPriorPersonaDigests_Boundary_CurrentTextsRecorded(t *testing.T) {
	personas := generatedPersonas()
	if len(priorPersonaDigests) != len(personas) {
		t.Fatalf("priorPersonaDigests covers %d paths, want the %d generated personas", len(priorPersonaDigests), len(personas))
	}
	for _, persona := range personas {
		if !isPriorRendering(persona.content, persona.prior) {
			t.Errorf("the current %s (%s) is not in priorPersonaDigests", persona.rel,
				fixtureDigest(t, persona.rel, persona.content))
		}
	}
}

// Positive: every earlier persona text is refreshed on a plain run through the confined writer
// and reported as a refresh; the current text is verified, never refreshed.
func TestAdopt_Positive_PriorPersonaRefreshedOnPlainRun(t *testing.T) {
	for _, persona := range generatedPersonas() {
		for name, data := range readFixtureDir(t, personaFixtureDir(persona.rel)) {
			repoPath, rep := adoptPersonaFixture(t, persona.rel, string(data), false)
			want, detail := string(persona.content), "Refreshed the unedited earlier"
			if string(data) == want {
				detail = "verified present"
			}
			if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(persona.rel))); got != want {
				t.Errorf("%s: %s not refreshed to the current text", persona.rel, name)
			}
			if got := findActionDetail(rep.ActionDetails, persona.rel); !strings.Contains(got, detail) {
				t.Errorf("%s: %s reported as %q, want %q", persona.rel, name, got, detail)
			}
		}
	}
}

// Negative: an edited persona is kept byte for byte under --force, with the not-audit-verified
// note and a warning, and neither created nor replaced.
func TestAdopt_Negative_EditedPersonaKeptUnderForce(t *testing.T) {
	edited := defaultAuditorAgentMD + "\nRepository note: audit the vendored tree too.\n"
	repoPath, rep := adoptPersonaFixture(t, auditorAgentFile, edited, true)
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(auditorAgentFile))); got != edited {
		t.Fatal("--force replaced an edited persona")
	}
	if contains(rep.CreatedFiles, auditorAgentFile) || hasAction(rep, auditorAgentFile, actionReplace) {
		t.Fatalf("an edited persona must be kept, got created=%v actions=%+v", rep.CreatedFiles, rep.ActionDetails)
	}
	if detail := findActionDetail(rep.ActionDetails, auditorAgentFile); !strings.Contains(detail, "not audit-verified; kept") {
		t.Errorf("drift note %q", detail)
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), auditorAgentFile+": existing file differs") {
		t.Errorf("no drift warning: %v", rep.Warnings)
	}
}

// Boundary: a CRLF checkout of an earlier persona is refreshed and keeps CRLF. A persona that is
// a symlink to an earlier text inside the repository is never refreshed through: it is reported
// unverified, and the text it points to stays.
func TestReconcileAgentDefinitions_Boundary_CRLFPriorKeepsCRLFAndSymlinkNotWrittenThrough(t *testing.T) {
	prior := string(readFixtureDir(t, personaFixtureDir(gatekeeperFile))["caveman-dry-run.repo-gatekeeper.md.prior"])
	repoPath, _ := adoptPersonaFixture(t, gatekeeperFile, crlfText(prior), false)
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(gatekeeperFile))); got != crlfText(defaultGatekeeperAgentMD) {
		t.Fatalf("CRLF prior persona not refreshed in CRLF:\n%q", got)
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs developer mode on Windows; the CRLF half ran")
	}
	s := &adoptSession{repoPath: t.TempDir(), report: &AdoptReport{}}
	link := filepath.Join(s.repoPath, filepath.FromSlash(gatekeeperFile))
	target := filepath.Join(filepath.Dir(link), "kept.md")
	mustWrite(t, target, prior)
	if err := os.Symlink("kept.md", link); err != nil {
		t.Fatal(err)
	}
	for _, persona := range generatedPersonas() {
		if persona.rel != gatekeeperFile {
			continue
		}
		if state, err := s.scaffoldFile(t.Context(), persona); err != nil || state != scaffoldUnverified {
			t.Fatalf("symlinked persona: state=%v err=%v, want unverified", state, err)
		}
	}
	if got := mustRead(t, target); got != prior {
		t.Fatal("the refresh wrote through a symlinked persona")
	}
}

// Positive, negative and boundary: a planned removal of a stale skill licence copy (Remove) is
// reported as a removal, never as a hand edit that needs a backup, and it records no prior digest.
func TestRecordProjections_RemovalOfAStaleSkillLicence(t *testing.T) {
	rel := ".claude/skills/caveman/LICENSE"
	removal := compiler.TargetFile{RelativePath: rel, Remove: true}
	target := vendorTarget{file: removal, before: []byte("MIT License\n"), exists: true}
	prior := agentSurfaceDigests([]compiler.TargetFile{removal})
	if len(prior) != 0 {
		t.Fatalf("a removal recorded a prior digest: %v", prior)
	}
	if target.replacesEdit(prior) {
		t.Fatal("a removal counted as a hand edit that needs a backup")
	}
	if got := projectionReplacements([]vendorTarget{target}, prior, agentSurfaceLabels); len(got) != 0 {
		t.Fatalf("a removal planned a replacement: %v", got)
	}
	var report AdoptReport
	recordProjections(&report, []vendorTarget{target}, prior, agentSurfaceLabels)
	if !hasAction(&report, rel, actionRemove) {
		t.Fatalf("the removal is not reported: %+v", report.ActionDetails)
	}
	if len(report.CreatedFiles) != 0 {
		t.Fatalf("a removal was reported created: %v", report.CreatedFiles)
	}
}
