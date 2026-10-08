// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

// Positive: audit locks the API workflow to the rendering for the repository's own manifest, the
// install step and the module exception included. Negative: the plain workflow of a repository
// that declares system packages fails naming plain adoption, the rendering of packages the
// manifest no longer declares fails the same way, and a hand-edited rendering fails naming
// --force. Boundary: no api settings keep the canonical bytes.
func TestAuditLocksTheAPIWorkflowToTheManifestRendering(t *testing.T) {
	var family managedasset.Family
	for _, candidate := range managedasset.Families() {
		if candidate.Facet == managedasset.APIContractFacet {
			family = candidate
		}
	}
	expires := time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02")
	root := t.TempDir()
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\napi:\n  system_packages:\n    - libudev-dev\n"+
		"exceptions:\n  - rule: api-compatibility\n    path: hw/go.mod\n    reason: needs a vendor SDK\n    expires: \""+expires+"\"\n")
	writeFamilyFixture(t, root, family, func(text string) string { return text })
	earlier := "holds an earlier Praetor text; run 'praetorctl adopt'"
	if _, err := auditManagedFamily(t.Context(), root, family); err == nil || !strings.Contains(err.Error(), earlier) {
		t.Fatalf("the plain workflow passed for a manifest with system packages: %v", err)
	}
	custom, err := adopt.FamilyForRepository(t.Context(), root, family)
	if err != nil {
		t.Fatal(err)
	}
	rendered := custom.Workflow
	writeFixtureFile(t, root, family.WorkflowFile, rendered)
	if _, err := auditManagedFamily(t.Context(), root, family); err != nil {
		t.Fatalf("the manifest's rendering failed audit: %v", err)
	}
	if !strings.Contains(rendered, apiassets.InstallStepName) || !strings.Contains(rendered, "hw/go.mod") {
		t.Fatalf("the rendering carries neither the install step nor the exception:\n%s", rendered)
	}
	edited := strings.Replace(rendered, "timeout-minutes: 15", "timeout-minutes: 90", 1)
	writeFixtureFile(t, root, family.WorkflowFile, edited)
	if _, err := auditManagedFamily(t.Context(), root, family); err == nil || !strings.Contains(err.Error(), "differs from the locked Praetor asset") {
		t.Fatalf("a hand-edited rendering passed or failed unnamed: %v", err)
	}
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\n")
	writeFixtureFile(t, root, family.WorkflowFile, rendered)
	if _, err := auditManagedFamily(t.Context(), root, family); err == nil || !strings.Contains(err.Error(), earlier) {
		t.Fatalf("a rendering of settings the manifest dropped passed: %v", err)
	}
	writeFixtureFile(t, root, family.WorkflowFile, family.Workflow)
	if _, err := auditManagedFamily(t.Context(), root, family); err != nil {
		t.Fatalf("no api settings must keep the canonical bytes: %v", err)
	}
}
