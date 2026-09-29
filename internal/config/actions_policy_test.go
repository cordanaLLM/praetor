// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"
	"testing"
)

// actionsManifest is a manifest declaring overrides.actions with permissions and the
// workflow_runs block body, when it is not empty.
func actionsManifest(permissions, workflowRuns string) string {
	body := "version: 1\noverrides:\n  actions:\n    default_workflow_permissions: " + permissions +
		"\n    allow_create_and_approve_pull_requests: false\n"
	if workflowRuns != "" {
		body += "workflow_runs:\n  expected:\n" + workflowRuns
	}
	return body
}

// expectedEntry renders one workflow_runs.expected entry.
func expectedEntry(workflow, state, reason string) string {
	return fmt.Sprintf("    - workflow: %q\n      state: %q\n      reason: %q\n", workflow, state, reason)
}

// TestActionsPolicy_Positive_DeclarationsLoad: a manifest declaring both Actions permissions
// and workflow run expectations loads, and Expectation finds each declared workflow by name.
func TestActionsPolicy_Positive_DeclarationsLoad(t *testing.T) {
	body := actionsManifest("read", expectedEntry("build.yml", "failing", "the compile step refuses until its toolchain lands")+
		expectedEntry("release.yaml", "unexercised", "no tag has been pushed yet"))
	m, err := LoadManifest(writeManifest(t, body))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Overrides.Actions == nil || m.Overrides.Actions.DefaultWorkflowPermissions != "read" {
		t.Fatalf("overrides.actions = %+v", m.Overrides.Actions)
	}
	build := m.WorkflowRuns.Expectation("build.yml")
	if build == nil || build.State != WorkflowRunFailing || !strings.Contains(build.Reason, "toolchain") {
		t.Fatalf("build.yml expectation = %+v", build)
	}
	if release := m.WorkflowRuns.Expectation("release.yaml"); release == nil || release.State != WorkflowRunUnexercised {
		t.Fatalf("release.yaml expectation = %+v", release)
	}
	if m.WorkflowRuns.Expectation("ci.yml") != nil {
		t.Fatal("an undeclared workflow has an expectation")
	}
}

// TestActionsPolicy_Negative_MalformedDeclarationsFail: every malformed declaration is a
// load error naming the key, never a manifest that silently compares nonsense with the forge.
func TestActionsPolicy_Negative_MalformedDeclarationsFail(t *testing.T) {
	cases := map[string]string{
		"empty permissions":   actionsManifest(`""`, ""),
		"unknown permissions": actionsManifest("admin", ""),
		"nested workflow":     actionsManifest("read", expectedEntry("ci/build.yml", "failing", "r")),
		"not yaml":            actionsManifest("read", expectedEntry("build.txt", "failing", "r")),
		"bare extension":      actionsManifest("read", expectedEntry(".yml", "failing", "r")),
		"unknown state":       actionsManifest("read", expectedEntry("build.yml", "flaky", "r")),
		"empty reason":        actionsManifest("read", expectedEntry("build.yml", "failing", "  ")),
		"two-line reason":     actionsManifest("read", expectedEntry("build.yml", "failing", "one\ntwo")),
		"declared twice": actionsManifest("read", expectedEntry("build.yml", "failing", "r")+
			expectedEntry("build.yml", "unexercised", "r")),
		"unknown key": actionsManifest("read", "    - workflow: build.yml\n      state: failing\n      reason: r\n      until: never\n"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadManifest(writeManifest(t, body)); err == nil {
				t.Fatalf("malformed declaration loaded:\n%s", body)
			}
		})
	}
}

// TestActionsPolicy_Boundary_BoundsAndAbsence: 64 declarations and a 500-byte reason load, one
// more of either fails; a manifest without either section loads, and Expectation is safe on it.
func TestActionsPolicy_Boundary_BoundsAndAbsence(t *testing.T) {
	var entries strings.Builder
	for i := 0; i < MaxWorkflowRunExpectations; i++ {
		entries.WriteString(expectedEntry(fmt.Sprintf("w%02d.yml", i), "unexercised", "not yet"))
	}
	if _, err := LoadManifest(writeManifest(t, actionsManifest("write", entries.String()))); err != nil {
		t.Fatalf("%d declarations refused: %v", MaxWorkflowRunExpectations, err)
	}
	entries.WriteString(expectedEntry("extra.yml", "unexercised", "not yet"))
	if _, err := LoadManifest(writeManifest(t, actionsManifest("write", entries.String()))); err == nil {
		t.Fatalf("%d declarations accepted", MaxWorkflowRunExpectations+1)
	}
	longest := strings.Repeat("r", maxWorkflowRunReason)
	if _, err := LoadManifest(writeManifest(t, actionsManifest("read", expectedEntry("a.yml", "failing", longest)))); err != nil {
		t.Fatalf("a %d-byte reason was refused: %v", maxWorkflowRunReason, err)
	}
	if _, err := LoadManifest(writeManifest(t, actionsManifest("read", expectedEntry("a.yml", "failing", longest+"r")))); err == nil {
		t.Fatalf("a %d-byte reason was accepted", maxWorkflowRunReason+1)
	}
	m, err := LoadManifest(writeManifest(t, "version: 1\n"))
	if err != nil {
		t.Fatalf("a manifest without Actions declarations failed: %v", err)
	}
	if m.Overrides.Actions != nil || m.WorkflowRuns != nil || m.WorkflowRuns.Expectation("ci.yml") != nil {
		t.Fatalf("absent sections decoded as %+v / %+v", m.Overrides.Actions, m.WorkflowRuns)
	}
}

// TestActionsPolicy_Negative_EffectiveLoaderValidates: the audit resolves its manifest through
// the effective policy loader, which refuses the malformed declaration LoadManifest refuses.
func TestActionsPolicy_Negative_EffectiveLoaderValidates(t *testing.T) {
	catalog := policyFixture(t, "", "", "")
	manifest := readPolicyFixture(t, catalog, ".standards.yaml")
	lock := readPolicyFixture(t, catalog, ".standards.lock")
	opts := EffectiveOptions{Root: t.TempDir(), CatalogRoot: catalog}
	if _, err := LoadEffectivePolicyInputsContext(t.Context(), opts, manifest, lock); err != nil {
		t.Fatalf("fixture manifest refused: %v", err)
	}
	bad := append(append([]byte(nil), manifest...), []byte("workflow_runs:\n  expected:\n"+expectedEntry("ci/x.yml", "failing", "r"))...)
	if _, err := LoadEffectivePolicyInputsContext(t.Context(), opts, bad, lock); err == nil ||
		!strings.Contains(err.Error(), "workflow_runs.expected") {
		t.Fatalf("effective loader accepted a malformed workflow_runs declaration: %v", err)
	}
}
