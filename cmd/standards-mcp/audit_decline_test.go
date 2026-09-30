package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureDeclineManifest is the fixture manifest declining steps.
func fixtureDeclineManifest(steps ...string) string {
	return "version: 1\nrepository:\n  owner: \"fixture\"\n  name: \"repo\"\nprofiles:\n  - \"framework\"\nfacets: []\n" +
		"adoption:\n  decline:\n    - " + strings.Join(steps, "\n    - ") + "\n"
}

// TestServerAuditDecline_Positive (#600): standards_audit honours the labels and git-hooks
// declines as the CLI audit does: the missing artefact passes with the decline named.
func TestServerAuditDecline_Positive(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, ".standards.yaml", fixtureDeclineManifest("labels", "git-hooks"))
	for _, rel := range []string{".config/labels.yaml", "lefthook.yml"} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	result := callTool(t, srv, "standards_audit", nil)
	expectText(t, "declined labels", result, "[PASS] Label taxonomy .config/labels.yaml declined by adoption.decline.")
	expectText(t, "declined git-hooks", result, "[PASS] Git hooks (lefthook.yml and its activation) declined by adoption.decline.")
}

// TestServerAuditDecline_Negative (#600): without the decline the missing label taxonomy still
// fails, and an agent-harness decline does not cover the register block: the failure says so.
func TestServerAuditDecline_Negative(t *testing.T) {
	srv, root := newFixtureServer(t)
	if err := os.Remove(filepath.Join(root, ".config", "labels.yaml")); err != nil {
		t.Fatal(err)
	}
	expectError(t, "undeclined labels", callTool(t, srv, "standards_audit", nil),
		"Required label taxonomy .config/labels.yaml is missing")

	harnessSrv, harnessRoot := newFixtureServer(t)
	writeFixtureFile(t, harnessRoot, ".standards.yaml", fixtureDeclineManifest("agent-harness"))
	writeFixtureFile(t, harnessRoot, "AGENTS.md", "# Repo\n\n## Rules\n\n- Keep functions small.\n")
	expectError(t, "declined agent-harness", callTool(t, harnessSrv, "standards_audit", nil),
		"adoption.decline lists agent-harness, which does not cover this check: audit still requires the text register block")
}

// TestServerAuditDecline_Boundary (#600): with agent-harness declined and the context in sync,
// the context gate passes and names the decline with what audit still requires; outside a Git
// checkout the git-hooks decline is not consulted.
func TestServerAuditDecline_Boundary(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, ".standards.yaml", fixtureDeclineManifest("agent-harness"))
	expectText(t, "synced agent-harness", callTool(t, srv, "standards_audit", nil),
		"[INFO] Agent harness declined by adoption.decline; audit still requires the text register block")
	bare := t.TempDir()
	if line, err := auditHookConfig(nil, bare); err != nil || !strings.Contains(line, "Not a git checkout") {
		t.Fatalf("hook gate outside a checkout: %q, %v", line, err)
	}
}
