// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
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
func expectedEntry(workflow, state, reason string, expires ...string) string {
	exp := ""
	if len(expires) > 0 && expires[0] != "" {
		exp = fmt.Sprintf("      expires: %q\n", expires[0])
	} else if state == string(WorkflowRunFailing) {
		exp = fmt.Sprintf("      expires: %q\n", time.Now().AddDate(0, 0, 30).Format(ExceptionDateLayout))
	}
	return fmt.Sprintf("    - workflow: %q\n      state: %q\n      reason: %q\n%s", workflow, state, reason, exp)
}

// TestActionsPolicy_Positive_DeclarationsLoad: a manifest declaring both Actions permissions
// and workflow run expectations loads, and Expectation finds each declared workflow by name.
func TestActionsPolicy_Positive_DeclarationsLoad(t *testing.T) {
	future := time.Now().AddDate(0, 0, 30).Format(ExceptionDateLayout)
	body := actionsManifest("read", expectedEntry("build.yml", "failing", "the compile step refuses until its toolchain lands", future)+
		expectedEntry("release.yaml", "unexercised", "no tag has been pushed yet")+
		expectedEntry("nightly.yaml", "unexercised", "optional expiry", future))
	m, err := LoadManifest(writeManifest(t, body))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Overrides.Actions == nil || m.Overrides.Actions.DefaultWorkflowPermissions != "read" {
		t.Fatalf("overrides.actions = %+v", m.Overrides.Actions)
	}
	build := m.WorkflowRuns.Expectation("build.yml")
	if build == nil || build.State != WorkflowRunFailing || !strings.Contains(build.Reason, "toolchain") || build.Expires != future {
		t.Fatalf("build.yml expectation = %+v", build)
	}
	if release := m.WorkflowRuns.Expectation("release.yaml"); release == nil || release.State != WorkflowRunUnexercised || release.Expires != "" {
		t.Fatalf("release.yaml expectation = %+v", release)
	}
	if nightly := m.WorkflowRuns.Expectation("nightly.yaml"); nightly == nil || nightly.State != WorkflowRunUnexercised || nightly.Expires != future {
		t.Fatalf("nightly.yaml expectation = %+v", nightly)
	}
	if m.WorkflowRuns.Expectation("ci.yml") != nil {
		t.Fatal("an undeclared workflow has an expectation")
	}

	rendered, err := RenderManifest(m)
	if err != nil {
		t.Fatalf("render manifest: %v", err)
	}
	again, err := LoadManifest(writeManifest(t, string(rendered)))
	if err != nil {
		t.Fatalf("rendered manifest does not reload: %v\n%s", err, rendered)
	}
	if again.WorkflowRuns == nil || len(again.WorkflowRuns.Expected) != 3 || again.WorkflowRuns.Expectation("build.yml").Expires != future {
		t.Fatalf("workflow_runs lost in render round trip: %+v", again.WorkflowRuns)
	}
}

// TestActionsPolicy_Negative_MalformedDeclarationsFail: every malformed declaration is a
// load error naming the key, never a manifest that silently compares nonsense with the forge.
func TestActionsPolicy_Negative_MalformedDeclarationsFail(t *testing.T) {
	future := time.Now().AddDate(0, 0, 30).Format(ExceptionDateLayout)
	cases := map[string]string{
		"empty permissions":   actionsManifest(`""`, ""),
		"unknown permissions": actionsManifest("admin", ""),
		"nested workflow":     actionsManifest("read", expectedEntry("ci/build.yml", "failing", "r", future)),
		"not yaml":            actionsManifest("read", expectedEntry("build.txt", "failing", "r", future)),
		"bare extension":      actionsManifest("read", expectedEntry(".yml", "failing", "r", future)),
		"unknown state":       actionsManifest("read", expectedEntry("build.yml", "flaky", "r", future)),
		"empty reason":        actionsManifest("read", expectedEntry("build.yml", "failing", "  ", future)),
		"two-line reason":     actionsManifest("read", expectedEntry("build.yml", "failing", "one\ntwo", future)),
		"declared twice": actionsManifest("read", expectedEntry("build.yml", "failing", "r", future)+
			expectedEntry("build.yml", "unexercised", "r")),
		"unknown key":                 actionsManifest("read", fmt.Sprintf("    - workflow: build.yml\n      state: failing\n      reason: r\n      expires: %q\n      until: never\n", future)),
		"missing expires on failing":  actionsManifest("read", "    - workflow: build.yml\n      state: failing\n      reason: r\n"),
		"invalid expires date":        actionsManifest("read", "    - workflow: build.yml\n      state: failing\n      reason: r\n      expires: not-a-date\n"),
		"unpadded date":               actionsManifest("read", "    - workflow: build.yml\n      state: failing\n      reason: r\n      expires: 2026-5-1\n"),
		"expires too far ahead":       actionsManifest("read", "    - workflow: build.yml\n      state: failing\n      reason: r\n      expires: 2099-01-01\n"),
		"invalid expires unexercised": actionsManifest("read", "    - workflow: release.yml\n      state: unexercised\n      reason: r\n      expires: invalid\n"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadManifest(writeManifest(t, body)); err == nil {
				t.Fatalf("malformed declaration loaded:\n%s", body)
			}
		})
	}
}

// TestActionsPolicy_Negative_MissingExpiresOnFailingRefused: a missing expires on state failing
// is refused by manifest validation and names the field expires.
func TestActionsPolicy_Negative_MissingExpiresOnFailingRefused(t *testing.T) {
	manifest := actionsManifest("read", "    - workflow: build.yml\n      state: failing\n      reason: r\n")
	_, err := LoadManifest(writeManifest(t, manifest))
	if err == nil {
		t.Fatal("failing expectation without expires was accepted")
	}
	if !strings.Contains(err.Error(), "expires") {
		t.Fatalf("error %q does not name field 'expires'", err.Error())
	}
}

// TestActionsPolicy_Boundary_ExpiredMethod: Expired reports true after expires day, false on or
// before; unexercised without expires never expires; failing without expires counts as expired.
func TestActionsPolicy_Boundary_ExpiredMethod(t *testing.T) {
	today := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	failingOnDay := WorkflowRunExpectation{State: WorkflowRunFailing, Expires: "2026-10-14"}
	if failingOnDay.Expired(today) {
		t.Fatal("expectation on its expires day must not count as expired")
	}
	failingExpired := WorkflowRunExpectation{State: WorkflowRunFailing, Expires: "2026-10-13"}
	if !failingExpired.Expired(today) {
		t.Fatal("expectation before today must count as expired")
	}
	failingFuture := WorkflowRunExpectation{State: WorkflowRunFailing, Expires: "2026-10-15"}
	if failingFuture.Expired(today) {
		t.Fatal("future expectation must not count as expired")
	}
	unexercisedNoExpiry := WorkflowRunExpectation{State: WorkflowRunUnexercised}
	if unexercisedNoExpiry.Expired(today) {
		t.Fatal("unexercised expectation without expires must never expire")
	}
	failingNoExpiry := WorkflowRunExpectation{State: WorkflowRunFailing}
	if !failingNoExpiry.Expired(today) {
		t.Fatal("failing expectation without expires must count as expired")
	}
	badDate := WorkflowRunExpectation{State: WorkflowRunFailing, Expires: "bad-date"}
	if !badDate.Expired(today) {
		t.Fatal("expectation with bad date must count as expired")
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
