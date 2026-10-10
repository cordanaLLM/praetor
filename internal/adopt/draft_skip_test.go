// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// hostedGateShapes returns each hosted gate's workflow for the main branch in the draft shape
// skip selects.
func hostedGateShapes(t *testing.T, skip bool) map[string]string {
	t.Helper()
	shapes := map[string]string{}
	for _, family := range managedasset.Families() {
		if family.WorkflowFile == "" {
			continue
		}
		shaped, err := family.WithDraftShape(skip)
		if err != nil {
			t.Fatal(err)
		}
		shapes[family.WorkflowFile] = shaped.Workflow
	}
	return shapes
}

// declareDraftSkip sets hosted_gates.draft in the manifest of root, or removes the section.
func declareDraftSkip(t *testing.T, root string, skip bool) {
	t.Helper()
	path := filepath.Join(root, config.ManifestFileName)
	manifest := strings.SplitN(mustRead(t, path), "\nhosted_gates:", 2)[0]
	if skip {
		manifest = strings.TrimRight(manifest, "\n") + "\nhosted_gates:\n  draft: skip\n"
	}
	mustWrite(t, path, manifest)
}

// Positive (#857): by default adoption writes the fail-closed draft step; with hosted_gates.draft:
// skip it renders the job-level skip from the same definition and refreshes the earlier shape
// without --force, and removing the opt-in refreshes it back. A rerun changes nothing.
// Negative: a hand-edited skip rendering is kept without --force and replaced with it.
// Boundary: the skip shape names the default branch like the fail-closed one.
func TestAdoptRendersTheSelectedDraftShape(t *testing.T) {
	root, opts := adoptHostedGates(t, "gates-draft-skip", "")
	failClosed, skip := hostedGateShapes(t, false), hostedGateShapes(t, true)
	assertHostedGates(t, root, failClosed)
	for rel, text := range failClosed {
		if !strings.Contains(text, ghworkflow.HostedGateDraftStepName) || strings.Contains(skip[rel], ghworkflow.HostedGateDraftStepName) ||
			!strings.Contains(skip[rel], "github.event.pull_request.draft == false") {
			t.Fatalf("%s: the default must hold the draft step and the opt-in the job-level skip:\n%s", rel, skip[rel])
		}
	}
	declareDraftSkip(t, root, true)
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	assertHostedGates(t, root, skip)
	assertRefreshed(t, report, skip)
	before := snapshotTree(t, root)
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, root))
	edited := writeEditedBranches(t, root, skip)
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertHostedGates(t, root, edited)
	opts.Force = true
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	assertHostedGates(t, root, skip)
	opts.Force = false
	declareDraftSkip(t, root, false)
	report, err = Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	assertHostedGates(t, root, failClosed)
	assertRefreshed(t, report, failClosed)
}

// writeEditedBranches writes each workflow of texts with an extra push branch and returns what it
// wrote.
func writeEditedBranches(t *testing.T, root string, texts map[string]string) map[string]string {
	t.Helper()
	edited := map[string]string{}
	for rel, text := range texts {
		edited[rel] = strings.Replace(text, "branches: ['main']", "branches: ['main', 'release/**']", 1)
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), edited[rel])
	}
	return edited
}

// Positive (#857): the gates of an earlier Praetor refresh straight to the skip shape when the
// repository opts in, LF or CRLF, and the family claims either shape as Praetor's own text so a
// disabled facet removes it. Negative: a text that is neither shape is not claimed.
func TestFamilyClaimsBothDraftShapes(t *testing.T) {
	for _, family := range managedasset.Families() {
		if family.WorkflowFile == "" {
			continue
		}
		skip, err := family.WithDraftShape(true)
		if err != nil {
			t.Fatal(err)
		}
		assertEarlierShapesRefreshToSkip(t, family, skip)
		for _, claimed := range []string{skip.Workflow, family.Workflow} {
			if praetors, err := ManagedFileIsPraetors(family, family.WorkflowFile, []byte(claimed)); err != nil || !praetors {
				t.Fatalf("%s: the base family does not claim a shape it renders: %v", family.WorkflowFile, err)
			}
		}
		if known, _ := skip.PriorRendering(family.WorkflowFile, []byte(skip.Workflow+"# edit\n")); known {
			t.Fatalf("%s: an edited skip shape was claimed", family.WorkflowFile)
		}
	}
}

// assertEarlierShapesRefreshToSkip checks that the earlier Praetor gate text and the fail-closed
// text, LF or CRLF, are earlier texts of the family selecting the skip shape.
func assertEarlierShapesRefreshToSkip(t *testing.T, family, skip managedasset.Family) {
	t.Helper()
	for _, priorPath := range hostedGatePriors[family.WorkflowFile] {
		prior, err := os.ReadFile(priorPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{string(prior), strings.ReplaceAll(family.Workflow, "\n", "\r\n"), family.Workflow} {
			if known, _ := skip.PriorRendering(family.WorkflowFile, []byte(text)); !known {
				t.Fatalf("%s: an earlier shape is not refreshed to the skip shape:\n%s", family.WorkflowFile, text)
			}
		}
	}
}

// Negative (#857): an invalid hosted_gates.draft is an error where the family is rendered, not a
// silent fall back to the fail-closed default: FamilyForRepository names it for every family with
// a workflow, and a family without a workflow is returned unchanged without reading the manifest.
// Boundary: the two valid values render.
func TestInvalidDraftPolicySurfacesInFamilyForRepository(t *testing.T) {
	root := newTestRepo(t, "gates-draft-invalid")
	manifestPath := filepath.Join(root, config.ManifestFileName)
	valid := "version: 1\nrepository:\n  owner: acme\n  name: gates\n"
	mustWrite(t, manifestPath, valid+"hosted_gates:\n  draft: sometimes\n")
	for _, family := range managedasset.Families() {
		_, err := FamilyForRepository(t.Context(), root, family)
		switch {
		case family.WorkflowFile == "" && err != nil:
			t.Fatalf("%s: a family without a workflow read the manifest: %v", family.Name, err)
		case family.WorkflowFile != "" && (err == nil || !strings.Contains(err.Error(), "hosted_gates.draft")):
			t.Fatalf("%s: FamilyForRepository = %v, want the invalid hosted_gates.draft", family.Name, err)
		}
	}
	for _, draft := range []string{"fail", "skip"} {
		mustWrite(t, manifestPath, valid+"hosted_gates:\n  draft: "+draft+"\n")
		for _, family := range managedasset.Families() {
			if _, err := FamilyForRepository(t.Context(), root, family); err != nil {
				t.Fatalf("%s: draft %s: %v", family.Name, draft, err)
			}
		}
	}
}

// Negative (#857): adoption fails on an invalid hosted_gates.draft before it rewrites a hosted
// gate.
func TestAdoptRefusesAnInvalidDraftPolicy(t *testing.T) {
	root, opts := adoptHostedGates(t, "gates-draft-invalid-adopt", "")
	manifestPath := filepath.Join(root, config.ManifestFileName)
	mustWrite(t, manifestPath, strings.TrimRight(mustRead(t, manifestPath), "\n")+"\nhosted_gates:\n  draft: sometimes\n")
	if _, err := Adopt(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "hosted_gates.draft") {
		t.Fatalf("Adopt = %v, want the invalid hosted_gates.draft", err)
	}
	for rel, text := range hostedGateShapes(t, false) {
		if got := mustRead(t, filepath.Join(root, filepath.FromSlash(rel))); got != text {
			t.Fatalf("%s changed although adoption failed", rel)
		}
	}
}

// Boundary (#857): the draft skip and the manifest settings of a family compose. With
// hosted_gates.draft: skip, an unedited copy refreshes without --force when api.system_packages
// are declared, carrying the declared package in the skip shape.
func TestDraftSkipComposesWithManifestSettings(t *testing.T) {
	root, opts := adoptHostedGates(t, "gates-draft-skip-settings", "")
	declareDraftSkip(t, root, true)
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, config.ManifestFileName)
	manifest := strings.TrimRight(mustRead(t, manifestPath), "\n") + "\napi:\n  system_packages: [libfoo-dev]\n"
	mustWrite(t, manifestPath, manifest)
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	assertRefreshed(t, report, map[string]string{APICompatibilityWorkflowFile: ""})
	got := mustRead(t, filepath.Join(root, filepath.FromSlash(APICompatibilityWorkflowFile)))
	if !strings.Contains(got, "libfoo-dev") || !strings.Contains(got, "github.event.pull_request.draft == false") {
		t.Fatalf("workflow was not refreshed to skip shape with declared package:\n%s", got)
	}
}
