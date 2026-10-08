// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func apiFamily(t *testing.T) Family {
	t.Helper()
	for _, family := range Families() {
		if family.Facet == APIContractFacet {
			return family
		}
	}
	t.Fatal("no API compatibility family")
	return Family{}
}

func packagesManifest(packages ...string) *config.Manifest {
	return &config.Manifest{API: &config.APIPolicy{SystemPackages: packages}}
}

// Positive: a manifest with system packages renders the install step into the API workflow, on
// top of the default-branch rendering in either order; a manifest that declares nothing, and no
// manifest, keep the canonical bytes. Negative: a family without Customize ignores the manifest.
func TestForManifest_RendersSettingsOverTheBranchRendering(t *testing.T) {
	family := apiFamily(t)
	for _, manifest := range []*config.Manifest{nil, {}, packagesManifest()} {
		same, err := family.ForManifest(manifest)
		if err != nil || same.Workflow != family.Workflow {
			t.Fatalf("manifest %+v changed the workflow (%v)", manifest, err)
		}
	}
	custom, err := family.ForManifest(packagesManifest("libudev-dev"))
	if err != nil || !strings.Contains(custom.Workflow, "apt-get install -y --no-install-recommends \\\n            libudev-dev\n") {
		t.Fatalf("the install step is missing (%v)", err)
	}
	branchFirst, err := family.ForBranch("develop")
	if err != nil {
		t.Fatal(err)
	}
	branchFirst, err = branchFirst.ForManifest(packagesManifest("libudev-dev"))
	if err != nil {
		t.Fatal(err)
	}
	manifestFirst, err := custom.ForBranch("develop")
	if err != nil || branchFirst.Workflow != manifestFirst.Workflow || !strings.Contains(branchFirst.Workflow, "branches: ['develop']") {
		t.Fatalf("branch and manifest renderings do not commute (%v)", err)
	}
	for _, other := range Families() {
		if other.Customize != nil {
			continue
		}
		if unchanged, err := other.ForManifest(packagesManifest("libudev-dev")); err != nil || unchanged.Workflow != other.Workflow {
			t.Fatalf("%s took manifest settings (%v)", other.Name, err)
		}
	}
	if err := custom.Validate(); err != nil {
		t.Fatalf("a customized family is invalid: %v", err)
	}
}

// Positive: an unedited rendering for other settings is an earlier text, so adoption refreshes it
// without --force: a rendering when the manifest dropped its packages, one for other packages,
// one rendered for another default branch, in a CRLF checkout too. Negative: the current
// rendering is no earlier text of itself, and an edited rendering is none at all.
func TestPriorRendering_RecognisesRenderingsOfOtherSettings(t *testing.T) {
	plain := apiFamily(t)
	withLibs, err := plain.ForManifest(packagesManifest("libudev-dev"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := plain.ForManifest(packagesManifest("libudev-dev", "libopenal-dev"))
	if err != nil {
		t.Fatal(err)
	}
	develop, err := withLibs.ForBranch("develop")
	if err != nil {
		t.Fatal(err)
	}
	rel := plain.WorkflowFile
	for name, tc := range map[string]struct {
		family Family
		text   string
	}{
		"packages dropped":  {plain, withLibs.Workflow},
		"packages changed":  {other, withLibs.Workflow},
		"packages added":    {withLibs, plain.Workflow},
		"other branch":      {plain, develop.Workflow},
		"branch and change": {other, develop.Workflow},
	} {
		if known, _ := tc.family.PriorRendering(rel, []byte(tc.text)); !known {
			t.Errorf("%s: an unedited rendering is not a prior text", name)
		}
		crlf := strings.ReplaceAll(tc.text, "\n", "\r\n")
		if known, style := tc.family.PriorRendering(rel, []byte(crlf)); !known || !style {
			t.Errorf("%s: the CRLF checkout is not a prior text in its own style (%v, %v)", name, known, style)
		}
	}
	if known, _ := withLibs.PriorRendering(rel, []byte(withLibs.Workflow)); known {
		t.Error("the current rendering counts as an earlier text of itself")
	}
	edited := strings.Replace(withLibs.Workflow, "timeout-minutes: 15", "timeout-minutes: 45", 1)
	appended := withLibs.Workflow + "# edit\n"
	for name, text := range map[string]string{"edited step": edited, "appended": appended} {
		if known, _ := plain.PriorRendering(rel, []byte(text)); known {
			t.Errorf("%s: an edited rendering counts as an earlier text", name)
		}
	}
	if known, _ := plain.PriorRendering("README.md", []byte(withLibs.Workflow)); known {
		t.Error("a rendering counts as an earlier text at another path")
	}
}
