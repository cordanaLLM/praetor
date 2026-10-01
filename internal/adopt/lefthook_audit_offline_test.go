package adopt

import (
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// onlineAuditRenderings are the renderings under testdata/lefthook whose pre-commit audit still
// asked the forge, one per language set, each with the markers of a repository detected as
// that set.
var onlineAuditRenderings = []struct {
	fixture   string
	markers   map[string]string
	languages hisscatalog.Language
}{
	{"online-audit-governance", map[string]string{"pytest.ini": "[pytest]\n"}, 0},
	{"online-audit-go", map[string]string{"go.mod": goModMarker}, hisscatalog.LanguageGo},
	{"online-audit-rust", map[string]string{"Cargo.toml": cargoTOMLBody}, hisscatalog.LanguageRust},
	{"online-audit-go-rust", map[string]string{"go.mod": goModMarker, "Cargo.toml": cargoTOMLBody}, lefthookJobLanguages},
}

// onlineAuditFixtureNames are the file names of one online rendering, without and with the
// checkpoint jobs.
func onlineAuditFixtureNames(fixture string) []string {
	return []string{fixture + ".lefthook.yml", fixture + "-checkpoint.lefthook.yml"}
}

// Positive: every rendering runs the pre-commit audit with --offline and the pre-push audit
// without it, and so does the fallback hook before a commit. Run against a recording stub, the
// lines call exactly those arguments, so a commit never asks the forge while a push does.
func TestLefthookAudit_Positive_PreCommitOfflinePrePushOnline(t *testing.T) {
	for _, languages := range lefthookLanguageSets {
		for _, checkpoint := range []bool{false, true} {
			var decoded map[string]any
			if err := yaml.Unmarshal([]byte(buildLefthookYAMLFor(languages, checkpoint)), &decoded); err != nil {
				t.Fatalf("languages=%v checkpoint=%v: %v", languages, checkpoint, err)
			}
			if got := decodedJob(t, decoded, "pre-commit", "hiss-audit")["run"]; got != lefthookGovernedCommand("audit --offline") {
				t.Errorf("languages=%v checkpoint=%v: pre-commit audit = %v", languages, checkpoint, got)
			}
			if got := decodedJob(t, decoded, "pre-push", "audit")["run"]; got != lefthookGovernedCommand("audit") {
				t.Errorf("languages=%v checkpoint=%v: pre-push audit = %v", languages, checkpoint, got)
			}
		}
	}
	stubs, work := t.TempDir(), t.TempDir()
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, util.PraetorCLI, log, 0)
	for _, line := range []string{lefthookGovernedCommand(preCommitAuditArgs), lefthookGovernedCommand(prePushAuditArgs)} {
		if out, code := runHookLine(t, "sh", line, stubs, work); code != 0 {
			t.Fatalf("hook line exited %d: %s", code, out)
		}
	}
	if out, code := runHookLine(t, "bash", buildFallbackPreCommitScript(), stubs, work); code != 0 {
		t.Fatalf("fallback pre-commit hook exited %d: %s", code, out)
	}
	cli := util.PraetorCLI
	want := cli + " audit --offline\n" + cli + " audit\n" + cli + " compile-context --verify\n" + cli + " audit --offline\n"
	if got := mustRead(t, log); got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

// Negative: a rendering whose pre-commit audit asks the forge is no current rendering for any
// language set. Unedited, it is an earlier Praetor rendering to migrate; with a local edit it is
// neither and is kept with a reason, like any configuration Praetor did not write.
func TestClassifyLefthookConfig_Negative_OnlinePreCommitAuditIsNotCurrent(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for _, rendering := range onlineAuditRenderings {
		for _, name := range onlineAuditFixtureNames(rendering.fixture) {
			data, ok := fixtures[name]
			if !ok {
				t.Fatalf("missing fixture %s", name)
			}
			if matchCurrentLefthook(data, rendering.languages).found {
				t.Errorf("%s: an online pre-commit audit matched the current rendering", name)
			}
			if got := classifyLefthookConfig(data, rendering.languages); got != (lefthookIdentity{prior: true}) {
				t.Errorf("%s: classified %+v, want an earlier rendering", name, got)
			}
			edited := append(append([]byte{}, data...), "# local edit\n"...)
			if got := classifyLefthookConfig(edited, rendering.languages); got.prior || got.reason == "" {
				t.Errorf("%s: an edited copy classified %+v, want kept with a reason", name, got)
			}
		}
	}
}

// Boundary: each online rendering, with and without checkpoint jobs, migrates on a plain run in
// a repository of its own languages: a reconcile with no replace, so no --force and no backup,
// to the current rendering for those languages, whose pre-commit audit is offline.
func TestAdopt_Boundary_OnlinePreCommitAuditMigratesWithoutForce(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for _, rendering := range onlineAuditRenderings {
		for _, name := range onlineAuditFixtureNames(rendering.fixture) {
			repoPath, rep := adoptMarkedRepo(t, rendering.fixture, rendering.markers, string(fixtures[name]))
			if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAMLFor(rendering.languages, false) {
				t.Errorf("%s: not migrated to the current rendering:\n%s", name, got)
			}
			if !hasAction(rep, lefthookFile, actionReconcile) || hasAction(rep, lefthookFile, actionReplace) {
				t.Errorf("%s: want a reconcile and no replace: %+v", name, rep.ActionDetails)
			}
		}
	}
}
