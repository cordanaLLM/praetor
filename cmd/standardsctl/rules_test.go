package main

import (
	"strings"
	"testing"
)

func TestRulesAuditCLI_ReportsProseOnlyWithoutFailing(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "AGENTS.md", "## Operational Rules\n\n1. Never bypass a check.\n\n## Text Register\n")
	writeFixtureFile(t, root, ".config/rules/enforcement.yaml", "version: 1\nrules: []\n")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("rules", []string{"audit", "--root", root})
	})
	if err != nil {
		t.Fatalf("rules audit error = %v", err)
	}
	mustContain(t, out, "HISS-01 guidance prose-only", "AGENTS-OP-1 guidance prose-only", "unenforced: 22")
	jsonOut, err := captureStdout(t, func() error {
		return dispatchCommand("rules", []string{"audit", "--root=" + root, "--json"})
	})
	if err != nil || !strings.Contains(jsonOut, "\"prose_only\": true") {
		t.Fatalf("rules audit JSON = (%q, %v)", jsonOut, err)
	}
	if err := dispatchCommand("rules", []string{"unknown"}); err == nil {
		t.Fatal("unknown rules subcommand succeeded")
	}
}

func TestRulesAuditCLI_FailsOnUnresolvedPointer(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "AGENTS.md", "## Operational Rules\n\n1. Check.\n\n## Text Register\n")
	writeFixtureFile(t, root, ".config/rules/enforcement.yaml", "version: 1\nrules:\n  - id: HISS-01\n    class: ci-gate\n    evidence: [make:absent]\n")
	_, err := captureStdout(t, func() error {
		return dispatchCommand("rules", []string{"audit", "--root", root})
	})
	if err == nil {
		t.Fatal("rules audit error = nil, want failure on unresolved evidence pointer")
	}
}
