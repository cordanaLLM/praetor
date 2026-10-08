// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

func apiManagedFamily(t *testing.T) managedasset.Family {
	t.Helper()
	return lintPriorFamily(t, "API compatibility")
}

// declareAPIPackages writes a manifest declaring packages and, when expires is not empty, one
// api-compatibility exception for lib/go.mod.
func declareAPIPackages(t *testing.T, s *adoptSession, expires string, packages ...string) {
	t.Helper()
	body := "version: 1\n"
	if len(packages) > 0 {
		body += "api:\n  system_packages:\n"
		for _, name := range packages {
			body += "    - " + name + "\n"
		}
	}
	if expires != "" {
		body += "exceptions:\n  - rule: api-compatibility\n    path: lib/go.mod\n    reason: needs a header the runner lacks\n    expires: \"" + expires + "\"\n"
	}
	mustWrite(t, filepath.Join(s.repoPath, config.ManifestFileName), body)
}

func workflowOf(t *testing.T, s *adoptSession) string {
	t.Helper()
	text, exists := familyFile(t, s, apiassets.WorkflowFile)
	if !exists {
		t.Fatal("adoption wrote no API workflow")
	}
	return text
}

// Positive: adoption renders the locked API workflow from api.system_packages, with the install
// step before the checker and the module exception in the gate's environment, and a second run
// leaves it alone. A repository declaring neither gets the canonical bytes.
func TestAdoptRendersTheAPIWorkflowFromTheManifest(t *testing.T) {
	family := apiManagedFamily(t)
	plain := familySession(t, false)
	if err := reconcileManagedFamily(t.Context(), plain, family); err != nil {
		t.Fatal(err)
	}
	if got := workflowOf(t, plain); got != family.Workflow {
		t.Fatal("a repository without api settings did not get the canonical workflow")
	}
	s := familySession(t, false)
	declareAPIPackages(t, s, time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02"), "libudev-dev", "libopenal-dev")
	for run := 0; run < 2; run++ {
		if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
			t.Fatal(err)
		}
	}
	text := workflowOf(t, s)
	install := strings.Index(text, apiassets.InstallStepName)
	gate := strings.Index(text, "- name: Compare the API")
	if install < 0 || gate < install || !strings.Contains(text, "--no-install-recommends \\\n            libudev-dev \\\n            libopenal-dev\n") ||
		!strings.Contains(text, apiassets.ExceptionsEnv+": ") {
		t.Fatalf("the workflow is not rendered from the manifest:\n%s", text)
	}
	rendered, err := FamilyForRepository(t.Context(), s.repoPath, family)
	if err != nil || rendered.Workflow != text {
		t.Fatalf("FamilyForRepository disagrees with what adoption wrote (%v)", err)
	}
}

// Positive: an unedited earlier rendering refreshes without --force when the manifest changes:
// the plain workflow once packages are declared, the rendering with packages once they are
// dropped, one for other packages. Negative: a hand-edited copy is kept without --force.
func TestAdoptRefreshesUneditedRenderingsWhenTheManifestChanges(t *testing.T) {
	family := apiManagedFamily(t)
	s := familySession(t, false)
	steps := []struct {
		name     string
		packages []string
	}{
		{"declared", []string{"libudev-dev"}}, {"changed", []string{"libvorbis-dev", "libgl-dev"}}, {"dropped", nil},
	}
	declareAPIPackages(t, s, "")
	if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		before := workflowOf(t, s)
		declareAPIPackages(t, s, "", step.packages...)
		if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		after := workflowOf(t, s)
		if after == before || strings.Contains(after, apiassets.InstallStepName) != (len(step.packages) > 0) {
			t.Fatalf("%s: the workflow was not refreshed to the manifest's packages", step.name)
		}
	}
	declareAPIPackages(t, s, "", "libudev-dev")
	if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(workflowOf(t, s), "timeout-minutes: 15", "timeout-minutes: 90", 1)
	mustWrite(t, filepath.Join(s.repoPath, apiassets.WorkflowFile), edited)
	declareAPIPackages(t, s, "")
	if err := reconcileManagedFamily(t.Context(), s, family); err != nil {
		t.Logf("an edited copy is refused with: %v", err)
	}
	if got := workflowOf(t, s); got != edited {
		t.Fatal("a hand-edited rendering was overwritten without --force")
	}
}

// Negative: a manifest with an invalid package name stops adoption naming api.system_packages
// before the workflow is written.
func TestAdoptRefusesAnInvalidSystemPackage(t *testing.T) {
	family := apiManagedFamily(t)
	s := familySession(t, false)
	declareAPIPackages(t, s, "", "libudev-dev", "Bad_Name")
	err := reconcileManagedFamily(t.Context(), s, family)
	if err == nil || !strings.Contains(err.Error(), "api.system_packages") {
		t.Fatalf("err = %v, want api.system_packages refused", err)
	}
	if _, exists := familyFile(t, s, apiassets.WorkflowFile); exists {
		t.Fatal("the workflow was written for an invalid manifest")
	}
}
