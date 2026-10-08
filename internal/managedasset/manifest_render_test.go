// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"maps"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
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
	assertEmptyManifestUnchanged(t, family)
	custom, err := family.ForManifest(packagesManifest("libudev-dev"))
	if err != nil || !strings.Contains(custom.Workflow, "apt-get install -y --no-install-recommends \\\n            libudev-dev\n") {
		t.Fatalf("the install step is missing (%v)", err)
	}
	assertCommutingBranchAndManifest(t, family, custom)
	assertUncustomizedFamiliesIgnoreManifest(t)
	if err := custom.Validate(); err != nil {
		t.Fatalf("a customized family is invalid: %v", err)
	}
}

func assertEmptyManifestUnchanged(t *testing.T, family Family) {
	t.Helper()
	for _, manifest := range []*config.Manifest{nil, {}, packagesManifest()} {
		same, err := family.ForManifest(manifest)
		if err != nil || same.Workflow != family.Workflow {
			t.Fatalf("manifest %+v changed the workflow (%v)", manifest, err)
		}
	}
}

func assertCommutingBranchAndManifest(t *testing.T, family, custom Family) {
	t.Helper()
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
}

func assertUncustomizedFamiliesIgnoreManifest(t *testing.T) {
	t.Helper()
	for _, other := range Families() {
		if other.Customize != nil {
			continue
		}
		if unchanged, err := other.ForManifest(packagesManifest("libudev-dev")); err != nil || unchanged.Workflow != other.Workflow {
			t.Fatalf("%s took manifest settings (%v)", other.Name, err)
		}
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
	assertOtherSettingsRecognized(t, rel, map[string]struct {
		family Family
		text   string
	}{
		"packages dropped":  {plain, withLibs.Workflow},
		"packages changed":  {other, withLibs.Workflow},
		"packages added":    {withLibs, plain.Workflow},
		"other branch":      {plain, develop.Workflow},
		"branch and change": {other, develop.Workflow},
	})
	assertEditedRenderingsRefused(t, plain, withLibs, rel)
}

func assertOtherSettingsRecognized(t *testing.T, rel string, cases map[string]struct {
	family Family
	text   string
}) {
	t.Helper()
	for name, tc := range cases {
		if known, _ := tc.family.PriorRendering(rel, []byte(tc.text)); !known {
			t.Errorf("%s: an unedited rendering is not a prior text", name)
		}
		crlf := strings.ReplaceAll(tc.text, "\n", "\r\n")
		if known, style := tc.family.PriorRendering(rel, []byte(crlf)); !known || !style {
			t.Errorf("%s: the CRLF checkout is not a prior text in its own style (%v, %v)", name, known, style)
		}
	}
}

func assertEditedRenderingsRefused(t *testing.T, plain, withLibs Family, rel string) {
	t.Helper()
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

func exceptionsManifest(path string) *config.Manifest {
	return &config.Manifest{
		Exceptions: []config.Exception{{
			Rule: config.ExceptionRuleAPICompatibility, Path: path, Reason: "vendored", Expires: "2027-01-01",
		}},
	}
}

// Positive (#857): under hosted_gates.draft: skip and the fail-closed default, an unedited copy
// refreshes without --force when api.system_packages are added, removed, or the package list or
// api-compatibility exceptions change.
func TestPriorRendering_ManifestSettingsRefreshBothShapes(t *testing.T) {
	plain := apiFamily(t)
	rel := plain.WorkflowFile
	withLibs, err := plain.ForManifest(packagesManifest("libudev-dev"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := plain.ForManifest(packagesManifest("libopenal-dev"))
	if err != nil {
		t.Fatal(err)
	}
	withExc1, err := plain.ForManifest(exceptionsManifest("hw/udev/go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	withExc2, err := plain.ForManifest(exceptionsManifest("hw/openal/go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, skip := range []bool{false, true} {
		assertShapeSettingsRefresh(t, rel, skip, plain, withLibs, other, withExc1, withExc2)
	}
}

func mustWithDraftShape(t *testing.T, family Family, skip bool) Family {
	t.Helper()
	shaped, err := family.WithDraftShape(skip)
	if err != nil {
		t.Fatal(err)
	}
	return shaped
}

func assertShapeSettingsRefresh(t *testing.T, rel string, skip bool, plain, withLibs, other, withExc1, withExc2 Family) {
	t.Helper()
	sPlain := mustWithDraftShape(t, plain, skip)
	sLibs := mustWithDraftShape(t, withLibs, skip)
	sOther := mustWithDraftShape(t, other, skip)
	sExc1 := mustWithDraftShape(t, withExc1, skip)
	sExc2 := mustWithDraftShape(t, withExc2, skip)

	cases := map[string]struct {
		family Family
		copy   string
	}{
		"packages added":     {sLibs, sPlain.Workflow},
		"packages removed":   {sPlain, sLibs.Workflow},
		"packages changed":   {sOther, sLibs.Workflow},
		"exceptions changed": {sExc2, sExc1.Workflow},
	}
	for name, tc := range cases {
		if known, _ := tc.family.PriorRendering(rel, []byte(tc.copy)); !known {
			t.Errorf("skip=%v, %s: unedited copy was not recognized as a prior text", skip, name)
		}
	}
}

// Positive (#857): when the gate text changes with settings declared, an unedited copy of the
// earlier Praetor text refreshes without --force under both draft shapes. Negative: a hand-edited
// skip copy is kept without --force.
func TestPriorRendering_GateTextChangeWithSettings(t *testing.T) {
	plain := apiFamily(t)
	rel := plain.WorkflowFile
	oldPlain := plain.Workflow
	oldPlainSkip, _, err := ghworkflow.RenderDraftSkip(oldPlain)
	if err != nil {
		t.Fatal(err)
	}
	oldWithLibs, err := plain.ForManifest(packagesManifest("libudev-dev"))
	if err != nil {
		t.Fatal(err)
	}
	oldWithLibsSkip, err := oldWithLibs.WithDraftShape(true)
	if err != nil {
		t.Fatal(err)
	}

	newFamily := plain
	newFamily.Prior = maps.Clone(plain.Prior)
	newFamily.Prior[sha256Hex(oldPlain)] = rel
	newFamily.Prior[sha256Hex(oldPlainSkip)] = rel
	newFamily.Workflow += "# pin moved\n"

	newWithLibs, err := newFamily.ForManifest(packagesManifest("libudev-dev"))
	if err != nil {
		t.Fatal(err)
	}
	for _, skip := range []bool{false, true} {
		target, err := newWithLibs.WithDraftShape(skip)
		if err != nil {
			t.Fatal(err)
		}
		text := oldWithLibs.Workflow
		if skip {
			text = oldWithLibsSkip.Workflow
		}
		if known, _ := target.PriorRendering(rel, []byte(text)); !known {
			t.Errorf("skip=%v: gate text change with settings declared was not recognized", skip)
		}
	}
	assertHandEditedSkipCopyRefused(t, newWithLibs, rel, oldWithLibsSkip.Workflow)
}

func assertHandEditedSkipCopyRefused(t *testing.T, family Family, rel, skipText string) {
	t.Helper()
	target, err := family.WithDraftShape(true)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(skipText, "timeout-minutes: 15", "timeout-minutes: 45", 1)
	if known, _ := target.PriorRendering(rel, []byte(edited)); known {
		t.Error("a hand-edited skip copy was recognized as a prior text")
	}
	if known, _ := target.PriorRendering(rel, []byte(skipText+"# edit\n")); known {
		t.Error("an appended skip copy was recognized as a prior text")
	}
}
