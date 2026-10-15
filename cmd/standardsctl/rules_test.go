package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestRulesAuditCLI_ReportsProseOnlyWithoutFailing(t *testing.T) {
	root := t.TempDir()
	agents := "## Operational Rules\n\n1. The agent is told not to bypass a check.\n\n## Text Register\n"
	writeFixtureFile(t, root, "AGENTS.md", agents)
	writeFixtureFile(t, root, ".config/rules/enforcement.yaml", "version: 1\nrules: []\n")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("rules", []string{"audit", "--root", root})
	})
	if err != nil {
		t.Fatalf("rules audit error = %v", err)
	}
	mustContain(t, out, "HISS-01 guidance prose-only", "AGENTS-OP-1 guidance prose-only",
		"instruction-only prohibitions:", "  AGENTS-OP-1", "unenforced: 22")
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

func TestRulesAuditCLI_ForgeReadbackPositiveAndOffline(t *testing.T) {
	f := rulesAuditForgeFixture(t)
	reader := &rulesProtectionStub{live: rulesLiveProtection("CI")}
	offline, err := captureStdout(t, func() error {
		return runRulesAuditWithReader([]string{"--root", f.dir}, reader)
	})
	if err != nil || reader.calls != 0 {
		t.Fatalf("offline rules audit = (%v, calls=%d)\n%s", err, reader.calls, offline)
	}
	mustContain(t, offline, "HISS-01 ci-gate asserted, not read back",
		"asserted, not read back:", "HISS-01 forge:required_checks asserted 1", "unenforced: 21")

	remote, err := captureStdout(t, func() error {
		return runRulesAuditWithReader([]string{"--root", f.dir, "--remote", "--json"}, reader)
	})
	if err != nil || reader.calls != 1 {
		t.Fatalf("remote rules audit = (%v, calls=%d)\n%s", err, reader.calls, remote)
	}
	mustContain(t, remote, `"asserted": "1"`, `"observed": "1 of 1 required"`,
		`"match": true`, `"read_back": true`, `"enforced": true`)
}

func TestRulesAuditCLI_ForgeReadbackNegativeAndError(t *testing.T) {
	f := rulesAuditForgeFixture(t)
	drift := &rulesProtectionStub{live: rulesLiveProtection()}
	out, err := captureStdout(t, func() error {
		return runRulesAuditWithReader([]string{"--root", f.dir, "--remote"}, drift)
	})
	if err == nil || drift.calls != 1 {
		t.Fatalf("drift rules audit = (%v, calls=%d)\n%s", err, drift.calls, out)
	}
	mustContain(t, out, "HISS-01 forge:required_checks asserted 1, observed 0 of 1 required; missing: CI, match=false",
		"invalid: HISS-01: forge setting required_checks mismatch: asserted 1, observed 0 of 1 required; missing: CI")

	unread := &rulesProtectionStub{err: errors.New("read refused")}
	out, err = captureStdout(t, func() error {
		return runRulesAuditWithReader([]string{"--root", f.dir, "--remote"}, unread)
	})
	if err == nil || unread.calls != 1 {
		t.Fatalf("unread rules audit = (%v, calls=%d)\n%s", err, unread.calls, out)
	}
	mustContain(t, out, "invalid: forge readback failed: read the live branch protection of main: read refused")
}

func rulesAuditForgeFixture(t *testing.T) *auditFixture {
	t.Helper()
	f := protectedFixture(t, false, "", &forgeStub{})
	agents := "## Operational Rules\n\n1. The agent is told not to bypass checks.\n\n## Text Register\n"
	writeFixtureFile(t, f.dir, "AGENTS.md", agents)
	writeFixtureFile(t, f.dir, ".config/rules/enforcement.yaml", "version: 1\nrules:\n"+
		"  - id: HISS-01\n    class: ci-gate\n    evidence: [forge:required_checks]\n"+
		"  - id: AGENTS-OP-1\n    class: ci-gate\n    evidence: [workflow:ci.yml#ci]\n")
	return f
}

type rulesProtectionStub struct {
	live  *forge.LiveBranchProtection
	err   error
	calls int
}

func (s *rulesProtectionStub) ReadBranchProtection(_ context.Context, _ string) (*forge.LiveBranchProtection, error) {
	s.calls++
	return s.live, s.err
}

func rulesLiveProtection(checks ...string) *forge.LiveBranchProtection {
	return &forge.LiveBranchProtection{Branch: "main", Legacy: &forge.LegacyBranchProtection{
		RequiredStatusChecks: &forge.LegacyStatusChecks{Contexts: checks},
	}}
}
