package adopt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/editor"
)

const jetBrainsProfileFixtures = "testdata/jetbrains-profile"

func jetBrainsProfileFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(jetBrainsProfileFixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPriorJetBrainsProfileDigests_Positive_FixturesReproduceEveryDigest(t *testing.T) {
	assertPriorDigestsReproduced(t, jetBrainsProfileFixtures, priorJetBrainsProfileDigests)
}

func TestPriorJetBrainsProfileDigests_Boundary_CurrentGeneratorProfilesRecorded(t *testing.T) {
	for _, arch := range []string{"framework", "app-service", "native-gpu-systems"} {
		set, err := editor.SynthesizeContext(t.Context(), editor.Options{
			WorkspaceRoot: t.TempDir(), Archetype: arch,
			Editors: []string{editor.EditorJetBrains}, Languages: []string{"go"},
			Complexity: config.HISSComplexityCeiling(),
		})
		if err != nil {
			t.Fatalf("%s: synthesize current profile: %v", arch, err)
		}
		profile := generatedProfile(t, set)
		fixture := jetBrainsProfileFixture(t, "current-"+arch+".xml")
		if !bytes.Equal([]byte(profile.Content), fixture) || !isPriorRendering(fixture, priorJetBrainsProfileDigests) {
			t.Errorf("%s: current generator output is not the recorded fixture (%s)",
				arch, fixtureDigest(t, arch, []byte(profile.Content)))
		}
	}
}

func generatedProfile(t *testing.T, set *editor.EditorConfigSet) editor.GeneratedFile {
	t.Helper()
	for _, file := range set.Files {
		if file.Path == jetBrainsInspectionProfile {
			return file
		}
	}
	t.Fatal("JetBrains inspection profile missing from generated files")
	return editor.GeneratedFile{}
}

func TestReconcileJetBrainsProfile_Positive_Pre476ProfileRefreshedOnPlainRun(t *testing.T) {
	s := lockAdoptSession(t)
	prior := jetBrainsProfileFixture(t, "prior-60-framework.xml")
	current := jetBrainsProfileFixture(t, "current-framework.xml")
	path := filepath.Join(s.repoPath, jetBrainsInspectionProfile)
	mustWrite(t, path, string(prior))
	file := editor.GeneratedFile{Path: jetBrainsInspectionProfile, Content: string(current), Editor: editor.EditorJetBrains}
	if err := s.reconcileEditorFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != string(current) {
		t.Errorf("pre-#476 profile not refreshed to current generator output:\n%s", got)
	}
	if detail := findActionDetail(s.report.ActionDetails, jetBrainsInspectionProfile); !strings.Contains(detail, "Refreshed an unedited earlier") {
		t.Errorf("refresh not reported with checkpoint wording: %q", detail)
	}
}

func TestReconcileJetBrainsProfile_Negative_HandEditedPriorKeptUnverified(t *testing.T) {
	s := lockAdoptSession(t)
	prior := string(jetBrainsProfileFixture(t, "prior-60-framework.xml"))
	edited := strings.Replace(prior, `value="10"`, `value="9"`, 1)
	path := filepath.Join(s.repoPath, jetBrainsInspectionProfile)
	mustWrite(t, path, edited)
	file := editor.GeneratedFile{Path: jetBrainsInspectionProfile, Content: string(jetBrainsProfileFixture(t, "current-framework.xml")), Editor: editor.EditorJetBrains}
	if err := s.reconcileEditorFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != edited {
		t.Error("hand-edited profile was changed")
	}
	if detail := findActionDetail(s.report.ActionDetails, jetBrainsInspectionProfile); !strings.Contains(detail, "kept unchanged, not verified") {
		t.Errorf("hand edit was not kept unverified: %q", detail)
	}
}

func TestReconcileJetBrainsProfile_Boundary_CurrentProfileLeftUnchanged(t *testing.T) {
	s := lockAdoptSession(t)
	current := jetBrainsProfileFixture(t, "current-framework.xml")
	path := filepath.Join(s.repoPath, jetBrainsInspectionProfile)
	mustWrite(t, path, string(current))
	file := editor.GeneratedFile{Path: jetBrainsInspectionProfile, Content: string(current), Editor: editor.EditorJetBrains}
	if err := s.reconcileEditorFile(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != string(current) {
		t.Error("current profile changed")
	}
	if detail := findActionDetail(s.report.ActionDetails, jetBrainsInspectionProfile); strings.Contains(detail, "Refreshed an unedited earlier") {
		t.Errorf("current profile reported as refreshed: %q", detail)
	}
}
