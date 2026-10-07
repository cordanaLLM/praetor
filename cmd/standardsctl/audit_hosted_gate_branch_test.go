// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// hostedGatePriorTexts maps each hosted gate to the committed text the earlier Praetor shipped
// there, which ran on every branch, tag and draft (#815).
var hostedGatePriorTexts = map[string]string{
	".github/workflows/praetor-api.yml":  "../../tools/apicompat/testdata/prior/praetor-api.every-branch-and-draft.yml",
	".github/workflows/praetor-docs.yml": "../../tools/markdownlint/testdata/prior/praetor-docs.every-branch-and-draft.yml",
}

// developRepository writes a manifest declaring develop as the default branch and family's files
// rendered for develop, and returns the root and that rendering.
func developRepository(t *testing.T, family managedasset.Family) (string, managedasset.Family) {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nrepository:\n  owner: acme\n  name: gates\n  default_branch: develop\n")
	develop, err := family.ForBranch("develop")
	if err != nil {
		t.Fatal(err)
	}
	writeFamilyFixture(t, root, develop, func(text string) string { return text })
	return root, develop
}

// Positive: a hosted gate rendered for the declared default branch passes, and without a
// declaration the main rendering does. Negative: the rendering for another branch fails naming
// both branches and the declaration, the earlier gate fails naming plain adoption, and a
// hand-edited gate fails naming --force. Boundary: a disabled facet still claims a rendering for
// any branch, never a hand-edited copy.
func TestAuditHostedGatesLockTheDefaultBranchRendering(t *testing.T) {
	for _, family := range managedasset.Families() {
		prior, hosted := hostedGatePriorTexts[family.WorkflowFile]
		if !hosted {
			continue
		}
		root, develop := developRepository(t, family)
		if _, err := auditManagedFamily(t.Context(), root, family); err != nil {
			t.Fatalf("%s: develop rendering: %v", family.Name, err)
		}
		priorText, err := os.ReadFile(prior)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(develop.Workflow, "branches: ['develop']", "branches: ['develop', 'feature/**']", 1)
		for text, want := range map[string]string{
			family.Workflow: "is Praetor's rendering for default branch main, but this checkout resolves develop " +
				"(repository.default_branch in .standards.yaml, else the origin remote's HEAD, else main): if main is the " +
				"default branch, declare repository.default_branch: main in .standards.yaml; otherwise run 'praetorctl adopt' " +
				"to render it for develop",
			string(priorText): "holds an earlier Praetor text; run 'praetorctl adopt'",
			edited:            "differs from the locked Praetor asset",
		} {
			writeFixtureFile(t, root, family.WorkflowFile, text)
			if _, err := auditManagedFamily(t.Context(), root, family); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: audit = %v, want %q", family.Name, err, want)
			}
		}
		disabled := t.TempDir()
		writeFixtureFile(t, disabled, family.WorkflowFile, edited)
		if err := auditDisabledManagedFamily(t.Context(), disabled, family); err != nil {
			t.Fatalf("%s: a disabled facet claimed a hand-edited gate: %v", family.Name, err)
		}
		writeFixtureFile(t, disabled, family.WorkflowFile, develop.Workflow)
		if err := auditDisabledManagedFamily(t.Context(), disabled, family); err == nil || !strings.Contains(err.Error(), family.WorkflowFile) {
			t.Fatalf("%s: a disabled facet passed over its develop rendering: %v", family.Name, err)
		}
		undeclared := t.TempDir()
		writeFamilyFixture(t, undeclared, family, func(text string) string { return text })
		if _, err := auditManagedFamily(t.Context(), undeclared, family); err != nil {
			t.Fatalf("%s: main rendering without a declaration: %v", family.Name, err)
		}
	}
}
