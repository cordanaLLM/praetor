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
	edited := map[string]string{}
	for rel, text := range skip {
		edited[rel] = strings.Replace(text, "branches: ['main']", "branches: ['main', 'release/**']", 1)
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), edited[rel])
	}
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
		prior, err := os.ReadFile(hostedGatePriors[family.WorkflowFile])
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{string(prior), strings.ReplaceAll(family.Workflow, "\n", "\r\n"), family.Workflow} {
			if known, _ := skip.PriorRendering(family.WorkflowFile, []byte(text)); !known {
				t.Fatalf("%s: an earlier shape is not refreshed to the skip shape:\n%s", family.WorkflowFile, text)
			}
		}
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
