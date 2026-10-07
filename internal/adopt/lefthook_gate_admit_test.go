package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// mesonMarker is the root file of a Meson project: the gate runs no toolchain for it.
const mesonMarker = "project('demo', 'c')\n"

// strictGateRenderings are the renderings under testdata/lefthook whose pre-push gate ran without
// --admit-unsupported, one per language set, each with the markers of a repository detected as
// that set. The governance-only one is a Meson repository, the repository #648 reports.
var strictGateRenderings = []struct {
	fixture   string
	markers   map[string]string
	languages hisscatalog.Language
}{
	{"strict-gate-governance", map[string]string{"meson.build": mesonMarker}, 0},
	{"strict-gate-go", map[string]string{"go.mod": goModMarker}, hisscatalog.LanguageGo},
	{"strict-gate-rust", map[string]string{"Cargo.toml": cargoTOMLBody}, hisscatalog.LanguageRust},
	{"strict-gate-go-rust", map[string]string{"go.mod": goModMarker, "Cargo.toml": cargoTOMLBody}, lefthookJobLanguages},
}

// strictGateFixtureNames are the file names of one strict rendering, without and with the
// checkpoint jobs.
func strictGateFixtureNames(fixture string) []string {
	return []string{fixture + ".lefthook.yml", fixture + "-checkpoint.lefthook.yml"}
}

// Positive (#648): every rendering's pre-push gate job passes --admit-unsupported, spelled from the
// flag's own constant, and its header says a root with neither go.mod nor Cargo.lock is admitted
// without a receipt. Run against a recording stub, the line calls exactly those arguments.
func TestLefthookGate_Positive_PrePushGateAdmitsUnsupportedLanguages(t *testing.T) {
	want := "gate run --path=. --" + gating.AdmitUnsupportedFlag
	for _, languages := range lefthookLanguageSets {
		for _, checkpoint := range []bool{false, true} {
			rendered := buildLefthookYAMLFor(lefthookShape{languages: languages}, checkpoint)
			var decoded map[string]any
			if err := yaml.Unmarshal([]byte(rendered), &decoded); err != nil {
				t.Fatalf("languages=%v checkpoint=%v: %v", languages, checkpoint, err)
			}
			if got := decodedJob(t, decoded, "pre-push", "gate")["run"]; got != lefthookGovernedCommand(want) {
				t.Errorf("languages=%v checkpoint=%v: pre-push gate = %v", languages, checkpoint, got)
			}
			if !strings.Contains(rendered, "# gets no receipt: the job names the languages it could not verify and\n") {
				t.Errorf("languages=%v checkpoint=%v: the header does not state the admission", languages, checkpoint)
			}
		}
	}
	stubs, work := t.TempDir(), t.TempDir()
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, util.PraetorCLI, log, 0)
	if out, code := runHookLine(t, "sh", lefthookGovernedCommand(prePushGateArgs), stubs, work); code != 0 {
		t.Fatalf("gate hook line exited %d: %s", code, out)
	}
	if got := mustRead(t, log); got != util.PraetorCLI+" "+want+"\n" {
		t.Fatalf("calls = %q", got)
	}
}

// Negative: a rendering whose pre-push gate refused every unsupported root is no current rendering
// for any language set. Unedited, it is an earlier Praetor rendering to migrate; with a local edit
// it is neither and is kept with a reason, like any configuration Praetor did not write. A failing
// gate still blocks the push through the rendered line.
func TestClassifyLefthookConfig_Negative_StrictPrePushGateIsNotCurrent(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for _, rendering := range strictGateRenderings {
		for _, name := range strictGateFixtureNames(rendering.fixture) {
			data, ok := fixtures[name]
			if !ok {
				t.Fatalf("missing fixture %s", name)
			}
			if matchCurrentLefthook(data, lefthookShape{languages: rendering.languages}).found {
				t.Errorf("%s: a strict pre-push gate matched the current rendering", name)
			}
			if got := classifyLefthookConfig(data, lefthookShape{languages: rendering.languages}); got != (lefthookIdentity{prior: true}) {
				t.Errorf("%s: classified %+v, want an earlier rendering", name, got)
			}
			edited := append(append([]byte{}, data...), "# local edit\n"...)
			if got := classifyLefthookConfig(edited, lefthookShape{languages: rendering.languages}); got.prior || got.reason == "" {
				t.Errorf("%s: an edited copy classified %+v, want kept with a reason", name, got)
			}
		}
	}
	stubs, work := t.TempDir(), t.TempDir()
	recordingStub(t, stubs, util.PraetorCLI, filepath.Join(work, "calls.log"), 1)
	if _, code := runHookLine(t, "sh", lefthookGovernedCommand(prePushGateArgs), stubs, work); code == 0 {
		t.Fatal("a failing gate did not block the push")
	}
}

// Boundary: each strict rendering, with and without checkpoint jobs, migrates on a plain run in a
// repository of its own languages, a Meson repository included: a reconcile with no replace, so no
// --force and no backup, to the current rendering for those languages, which is then activated.
func TestAdopt_Boundary_StrictPrePushGateMigratesWithoutForce(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for _, rendering := range strictGateRenderings {
		for _, name := range strictGateFixtureNames(rendering.fixture) {
			repoPath, rep := adoptMarkedRepo(t, rendering.fixture, rendering.markers, string(fixtures[name]))
			if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAMLFor(lefthookShape{languages: rendering.languages}, false) {
				t.Errorf("%s: not migrated to the current rendering:\n%s", name, got)
			}
			if !hasAction(rep, lefthookFile, actionReconcile) || hasAction(rep, lefthookFile, actionReplace) {
				t.Errorf("%s: want a reconcile and no replace: %+v", name, rep.ActionDetails)
			}
			if !fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
				t.Errorf("%s: the migrated configuration was not activated", name)
			}
		}
	}
}
