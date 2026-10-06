// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"gopkg.in/yaml.v3"
)

// plainGovulncheckRenderings are the renderings under testdata/lefthook whose pre-push security job
// ran govulncheck ./..., one per language set holding Go, each with the markers of a repository
// detected as that set.
var plainGovulncheckRenderings = []struct {
	fixture   string
	markers   map[string]string
	languages hisscatalog.Language
}{
	{"plain-govulncheck-go", map[string]string{"go.mod": goModMarker}, hisscatalog.LanguageGo},
	{"plain-govulncheck-go-rust", map[string]string{"go.mod": goModMarker, "Cargo.toml": cargoTOMLBody}, lefthookJobLanguages},
}

// Positive (#778): every rendering holding Go jobs runs the Go vulnerability gate in its pre-push
// security job, spelled from govulnGateArgs, and no rendering without Go has a security job.
func TestLefthookSecurityJob_Positive_RunsTheVulnerabilityGate(t *testing.T) {
	for _, languages := range lefthookLanguageSets {
		for _, checkpoint := range []bool{false, true} {
			var decoded map[string]any
			if err := yaml.Unmarshal([]byte(buildLefthookYAMLFor(languages, checkpoint)), &decoded); err != nil {
				t.Fatalf("languages=%v checkpoint=%v: %v", languages, checkpoint, err)
			}
			if languages&hisscatalog.LanguageGo == 0 {
				if strings.Contains(buildLefthookYAMLFor(languages, checkpoint), "    security:\n") {
					t.Errorf("languages=%v: a security job without Go jobs", languages)
				}
				continue
			}
			run, isString := decodedJob(t, decoded, "pre-push", "security")["run"].(string)
			if !isString || !strings.Contains(run, `"$praetor_cli" `+govulnGateArgs+";") || strings.Contains(run, "govulncheck ./...") {
				t.Errorf("languages=%v checkpoint=%v: security job runs %q", languages, checkpoint, run)
			}
		}
	}
}

// Negative: a rendering whose security job ran govulncheck ./... is no current rendering for its
// languages. Unedited it is an earlier Praetor rendering to migrate; edited it is kept with a reason.
func TestClassifyLefthookConfig_Negative_PlainGovulncheckIsNotCurrent(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for _, rendering := range plainGovulncheckRenderings {
		for _, name := range []string{rendering.fixture + ".lefthook.yml", rendering.fixture + "-checkpoint.lefthook.yml"} {
			data, ok := fixtures[name]
			if !ok {
				t.Fatalf("missing fixture %s", name)
			}
			if !strings.Contains(string(data), "govulncheck ./...;") || matchCurrentLefthook(data, rendering.languages).found {
				t.Errorf("%s: not the plain govulncheck rendering, or still current", name)
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

// Boundary: each plain govulncheck rendering migrates on a plain run in a repository of its own
// languages, with no --force, to the current rendering, whose security job runs the gate.
func TestAdopt_Boundary_PlainGovulncheckMigratesWithoutForce(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for _, rendering := range plainGovulncheckRenderings {
		name := rendering.fixture + ".lefthook.yml"
		repoPath, rep := adoptMarkedRepo(t, rendering.fixture, rendering.markers, string(fixtures[name]))
		if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAMLFor(rendering.languages, false) {
			t.Errorf("%s: not migrated to the current rendering:\n%s", name, got)
		}
		if !hasAction(rep, lefthookFile, actionReconcile) || hasAction(rep, lefthookFile, actionReplace) {
			t.Errorf("%s: want a reconcile and no replace: %+v", name, rep.ActionDetails)
		}
	}
}
