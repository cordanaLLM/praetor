// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// draftSkipManifest declares the opt-in draft skip.
const draftSkipManifest = "version: 1\nrepository:\n  owner: acme\n  name: gates\nhosted_gates:\n  draft: skip\n"

// Positive (#857): with hosted_gates.draft: skip the audit locks each hosted gate to the skip
// rendering, whose result job is a proven aggregate that fails on a draft. Negative: the
// fail-closed text, a skip rendering without that aggregate, one whose aggregate passes while the
// gate job is skipped and a hand-edited copy are each refused, and the guard names the missing
// condition. Boundary: without the opt-in the skip rendering is an earlier text, not accepted.
func TestAuditLocksTheDraftSkipRendering(t *testing.T) {
	for _, family := range managedasset.Families() {
		if family.WorkflowFile == "" {
			continue
		}
		skip, err := family.WithDraftShape(true)
		if err != nil {
			t.Fatal(err)
		}
		gate, result, _ := skip.DraftSkipJobs()
		root := t.TempDir()
		writeFixtureFile(t, root, ".standards.yaml", draftSkipManifest)
		writeFamilyFixture(t, root, skip, func(text string) string { return text })
		if _, err := auditManagedFamily(t.Context(), root, family); err != nil {
			t.Fatalf("%s: the skip rendering is refused: %v", family.Name, err)
		}
		failedOnly := "needs." + gate + ".result == 'failure' || needs." + gate + ".result == 'cancelled'"
		withoutResult := skip.Workflow[:strings.Index(skip.Workflow, "  "+result+":")]
		cases := map[string]struct{ text, want string }{
			"the fail-closed text": {family.Workflow, "holds an earlier Praetor text; run 'praetorctl adopt'"},
			"an edited fail-closed text": {
				family.Workflow + "# edit\n",
				"does not carry the draft skip condition",
			},
			"no aggregate": {
				strings.Replace(withoutResult, "name: "+family.StatusContext+ghworkflow.HostedGateSkipGateSuffix, "name: "+family.StatusContext, 1),
				"is the gate job " + gate + " itself, whose draft skip reports success",
			},
			"an aggregate passing on a skip": {
				strings.NewReplacer(
					"if: needs."+gate+".result == 'skipped'", "if: "+failedOnly,
					"if: needs."+gate+".result != 'success'", "if: "+failedOnly,
				).Replace(skip.Workflow),
				"passes while " + gate + " is skipped",
			},
			"a hand edit": {
				strings.Replace(skip.Workflow, "timeout-minutes: 5", "timeout-minutes: 50", 1),
				"differs from the locked Praetor asset",
			},
		}
		for name, tc := range cases {
			writeFixtureFile(t, root, family.WorkflowFile, tc.text)
			_, err := auditManagedFamily(t.Context(), root, family)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s: %s: audit = %v, want %q", family.Name, name, err, tc.want)
			}
		}
		undeclared := t.TempDir()
		writeFixtureFile(t, undeclared, ".standards.yaml", "version: 1\nrepository:\n  owner: acme\n  name: gates\n")
		writeFamilyFixture(t, undeclared, skip, func(text string) string { return text })
		if _, err := auditManagedFamily(t.Context(), undeclared, family); err == nil ||
			!strings.Contains(err.Error(), "holds an earlier Praetor text; run 'praetorctl adopt'") {
			t.Fatalf("%s: the skip rendering without the opt-in: %v", family.Name, err)
		}
	}
}

// Negative (#857): an invalid hosted_gates.draft fails the audit of every family with a hosted
// workflow naming the key, instead of auditing the fail-closed default.
func TestAuditRefusesAnInvalidDraftPolicy(t *testing.T) {
	for _, family := range managedasset.Families() {
		if family.WorkflowFile == "" {
			continue
		}
		root := t.TempDir()
		writeFixtureFile(t, root, ".standards.yaml", strings.Replace(draftSkipManifest, "draft: skip", "draft: sometimes", 1))
		writeFamilyFixture(t, root, family, func(text string) string { return text })
		if _, err := auditManagedFamily(t.Context(), root, family); err == nil || !strings.Contains(err.Error(), "hosted_gates.draft") {
			t.Fatalf("%s: audit = %v, want the invalid hosted_gates.draft", family.Name, err)
		}
	}
}
