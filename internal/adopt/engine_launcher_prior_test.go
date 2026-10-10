// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// pathEngineRenderings are the renderings under testdata/lefthook whose governance jobs ran the
// engine found on PATH, by language set, each with the markers of a repository detected as it.
var pathEngineRenderings = []struct {
	fixture   string
	markers   map[string]string
	languages hisscatalog.Language
}{
	{"path-engine-governance", map[string]string{"main.py": "print()\n"}, 0},
	{"path-engine-go", map[string]string{"go.mod": goModMarker}, hisscatalog.LanguageGo},
	{"path-engine-rust", map[string]string{"Cargo.toml": cargoTOMLBody}, hisscatalog.LanguageRust},
	{"path-engine-go-rust", map[string]string{"go.mod": goModMarker, "Cargo.toml": cargoTOMLBody}, lefthookJobLanguages},
}

// Negative: no PATH-resolved rendering is current. Unedited it is an earlier Praetor rendering to
// migrate, with and without the REUSE job and the checkpoint jobs; edited it is kept with a reason.
func TestClassifyLefthookConfig_Negative_PathResolvedRenderingsAreNotCurrent(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for _, rendering := range pathEngineRenderings {
		for _, suffix := range []string{"", "-checkpoint", "-reuse", "-reuse-checkpoint"} {
			name := rendering.fixture + suffix + ".lefthook.yml"
			data, ok := fixtures[name]
			if !ok {
				t.Fatalf("missing fixture %s", name)
			}
			shape := lefthookShape{languages: rendering.languages, reuse: strings.Contains(suffix, "reuse")}
			if !strings.Contains(string(data), "praetor_cli") || matchCurrentLefthook(data, shape).found {
				t.Errorf("%s: not a PATH-resolved rendering, or still current", name)
			}
			if got := classifyLefthookConfig(data, shape); got != (lefthookIdentity{prior: true}) {
				t.Errorf("%s: classified %+v, want an earlier rendering", name, got)
			}
			edited := append(append([]byte{}, data...), "# local edit\n"...)
			if got := classifyLefthookConfig(edited, shape); got.prior || got.reason == "" {
				t.Errorf("%s: an edited copy classified %+v, want kept with a reason", name, got)
			}
		}
	}
}

// Boundary: each PATH-resolved rendering migrates on a plain run in a repository of its own
// languages, with no --force, to the current rendering, and the launcher it runs is installed.
func TestAdopt_Boundary_PathResolvedRenderingMigratesWithoutForce(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	for _, rendering := range pathEngineRenderings {
		if rendering.languages == 0 {
			continue // the governance-only rendering is classified above; its markers detect another language
		}
		name := rendering.fixture + ".lefthook.yml"
		repoPath, rep := adoptMarkedRepo(t, rendering.fixture, rendering.markers, string(fixtures[name]))
		if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAMLFor(lefthookShape{languages: rendering.languages}, false) {
			t.Errorf("%s: not migrated to the current rendering:\n%s", name, got)
		}
		if !hasAction(rep, lefthookFile, actionReconcile) || hasAction(rep, lefthookFile, actionReplace) {
			t.Errorf("%s: want a reconcile and no replace: %+v", name, rep.ActionDetails)
		}
		if mustRead(t, filepath.Join(repoPath, filepath.FromSlash(engineLauncherFile))) != engineLauncherScript {
			t.Errorf("%s: the launcher the migrated jobs run was not installed", name)
		}
	}
}
