package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func TestAudit_Positive_RootsFollowManifest(t *testing.T) {
	f := newAuditFixture(t)

	// Runs from the package directory: every companion must resolve next to the
	// manifest, never in the process working directory.
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed: %v\n%s", err, out)
	}
	mustContain(t, out,
		"=== acme/widgets Governance Audit ===",
		"[PASS] SemVer lockfile",
		"[PASS] Lockfile digests verified",
		"[PASS] Repository identity verified (acme/widgets",
		"[PASS] HISS invariant scan verified: 0 active violations within 0 baselined limit",
		"[PASS] Cross-agent context targets",
		"[PASS] Agent context caveman lint passed",
		"[PASS] Repository label taxonomy",
		"[PASS] Paperclip agent runtime harness verified (acme/widgets",
		"Audit Summary: configured governance gates passed",
	)
}

// auditGateCase mutates a passing fixture so that exactly one gate fails.
type auditGateCase struct {
	name   string
	mutate func(t *testing.T, f *auditFixture)
	want   string
}

func auditGateFailureCases() []auditGateCase {
	return []auditGateCase{
		{"lockfile missing", func(t *testing.T, f *auditFixture) {
			if err := os.Remove(filepath.Join(f.dir, ".standards.lock")); err != nil {
				t.Fatal(err)
			}
		}, ".standards.lock is missing"},
		{"legacy module path", func(t *testing.T, f *auditFixture) {
			writeFixtureFile(t, f.dir, "go.mod", "module "+legacyModulePath+"\n\ngo 1.27\n")
		}, "obsolete module path"},
		{"legacy needs reference", func(t *testing.T, f *auditFixture) {
			writeFixtureFile(t, f.dir, ".needs.yaml", "repository: "+legacyModulePath+"\n")
		}, ".needs.yaml contains obsolete repository reference"},
		{"harness mismatch", func(t *testing.T, f *auditFixture) {
			writeFixtureFile(t, f.dir, ".paperclip/harness.json", `{"version":1,"platform":"other/repo","operating_contract":["x"],"agit_push_format":"fixture push","invariants":["fixture invariant"]}`)
		}, "Paperclip harness platform mismatch"},
		{"new violation", func(t *testing.T, f *auditFixture) {
			f.addViolation(t)
		}, "HISS invariant violations introduced"},
		{"missing labels", func(t *testing.T, f *auditFixture) {
			if err := os.Remove(filepath.Join(f.dir, ".config", "labels.yaml")); err != nil {
				t.Fatal(err)
			}
		}, "label taxonomy .config/labels.yaml is missing"},
		{"persona projection drift", func(t *testing.T, f *auditFixture) {
			persona := "# Gatekeeper\n\nNever merge without a receipt.\n"
			writeFixtureFile(t, f.dir, ".agents/agents/gatekeeper.md", persona)
			for _, dir := range allPersonaDirs(t) {
				writeFixtureFile(t, f.dir, dir+"/gatekeeper.md", persona)
			}
			writeFixtureFile(t, f.dir, ".github/agents/gatekeeper.md", "# Gatekeeper\n\nReceipts are optional.\n")
		}, "Agent persona projections out of sync"},
		{"stale text register block", func(t *testing.T, f *auditFixture) {
			staleRegisterBlock(t, f.dir)
		}, "Agent context text register"},
		{"prose AGENTS.md", func(t *testing.T, f *auditFixture) {
			writeFixtureFile(t, f.dir, "AGENTS.md", proseAgentsMD)
			if out, err := runCompileContextCmd(t, f.dir); err != nil {
				t.Fatalf("recompile prose fixture: %v\n%s", err, out)
			}
		}, "AGENTS.md fails the caveman lint"},
		{"empty manifest identity", func(t *testing.T, f *auditFixture) {
			writeFixtureFile(t, f.dir, ".standards.yaml", "version: 1\nprofiles:\n  - \"framework\"\nfacets:\n  - \"security:high\"\n")
		}, "owner and name must not be empty"},
	}
}

func TestAudit_Negative_GateFailures(t *testing.T) {
	for _, tc := range auditGateFailureCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuditFixture(t)
			tc.mutate(t, f)
			_, err := f.audit(t)
			mustErrContain(t, err, tc.want)
		})
	}
}

func TestAuditReadmeGovernanceGate(t *testing.T) {
	t.Run("stale managed content fails", func(t *testing.T) {
		f := newAuditFixture(t)
		writeFixtureFile(t, f.dir, "README.md", "# Widgets\n\n<!-- praetor:readme-governance:start -->\nstale claim\n<!-- praetor:readme-governance:end -->\n")
		_, err := f.audit(t)
		mustErrContain(t, err, "README governance block is stale")
	})

	t.Run("current managed content passes", func(t *testing.T) {
		f := newAuditFixture(t)
		writeFixtureFile(t, f.dir, "README.md", `# Widgets

<!-- praetor:readme-governance:start -->
[![HISS Adopted](https://img.shields.io/badge/Standards-HISS%20Adopted-blue)](AGENTS.md)

Praetor manages this repository's declared governance policy. This managed block records adoption state; it is not a verification certificate.

| Gate | Command | Contract |
| :--- | :--- | :--- |
| **Verification** | `+"`make verify-all`"+` | Runs the repository's configured verification cascade |
| **HISS Audit** | `+"`praetorctl audit`"+` | Enforces policy, generated-surface integrity, and the debt ratchet |
| **Context Sync** | `+"`praetorctl compile-context --verify`"+` | Verifies every generated agent context against `+"`AGENTS.md`"+` |
| **Debt Baseline** | `+"`.standards-baseline.json`"+` | 0 recorded infractions; audit forbids growth |
<!-- praetor:readme-governance:end -->
`)
		out, err := f.audit(t)
		if err != nil {
			t.Fatalf("current README governance: %v\n%s", err, out)
		}
		mustContain(t, out, "[PASS] README governance block verified")
	})

	t.Run("explicit decline skips the managed surface", func(t *testing.T) {
		f := newAuditFixture(t)
		writeFixtureFile(t, f.dir, "README.md", "# Operator-owned README\n")
		writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+"adoption:\n  decline: [readme]\n")
		out, err := f.audit(t)
		if err != nil {
			t.Fatalf("declined README: %v\n%s", err, out)
		}
		mustContain(t, out, "[INFO] README governance block declined")
	})

	t.Run("invalid decline cannot bypass the managed surface", func(t *testing.T) {
		f := newAuditFixture(t)
		writeFixtureFile(t, f.dir, "README.md", "# Operator-owned README\n")
		writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+"adoption:\n  decline: [read-me]\n")
		_, err := f.audit(t)
		mustErrContain(t, err, "unknown artefact")
	})
}

func TestAudit_Negative_ReadErrorsAndArguments(t *testing.T) {
	t.Run("unreadable go.mod fails instead of passing", func(t *testing.T) {
		testsupport.SkipIfFileModeUnenforced(t)
		f := newAuditFixture(t)
		goMod := writeFixtureFile(t, f.dir, "go.mod", "module example.com/widgets\n\ngo 1.27\n")
		if err := os.Chmod(goMod, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(goMod, 0o600); err != nil {
				t.Log(err)
			}
		})
		_, err := f.audit(t)
		mustErrContain(t, err, "cannot read")
	})

	t.Run("positional argument", func(t *testing.T) {
		f := newAuditFixture(t)
		_, err := f.audit(t, "extra")
		mustErrContain(t, err, "no positional arguments")
	})

	t.Run("missing manifest", func(t *testing.T) {
		_, err := captureStdout(t, func() error {
			return dispatchCommand("audit", []string{"--config=" + filepath.Join(t.TempDir(), "nonexistent.yaml")})
		})
		mustErrContain(t, err, "Manifest audit failed")
	})
}

func TestAudit_TouchedFileCleanRule(t *testing.T) {
	f := newAuditFixture(t)
	inf := f.addViolation(t)

	// Baselined debt in an untouched file passes.
	f.writeBaseline(t, []baseline.Infraction{inf}, "")
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("baselined debt must pass: %v\n%s", err, out)
	}
	mustContain(t, out, "1 active violations within 1 baselined limit (0 touched files clean)")

	// The same file declared touched revokes the exemption.
	_, err = f.audit(t, "--touched=legacy.go")
	mustErrContain(t, err, "touched file must be clean")

	// Boundary: an empty list and a touched clean file both pass; the touched count is
	// reported.
	if _, err := f.audit(t, "--touched="); err != nil {
		t.Fatalf("empty --touched: %v", err)
	}
	out, err = f.audit(t, "--touched= ,AGENTS.md, ./.config/labels.yaml ,")
	if err != nil {
		t.Fatalf("clean touched files: %v", err)
	}
	mustContain(t, out, "(2 touched files clean)")
}

func TestAudit_GitChangeSetAndGrowthGuard(t *testing.T) {
	t.Setenv("CI", "true")
	f := newAuditFixture(t)
	inf := f.addViolation(t)
	f.writeBaseline(t, []baseline.Infraction{inf}, "")
	env := initGitFixture(t, f.dir)

	// Clean tree: nothing is touched and the committed baseline did not grow.
	out, err := f.audit(t, "--base=main")
	if err != nil {
		t.Fatalf("clean tree: %v\n%s", err, out)
	}
	mustContain(t, out, "(0 touched files clean)", "[PASS] HISS-13 debt ratchet")

	// Editing the legacy file (violation kept) makes it touched: the exemption is revoked
	// even without --base, because git reports it as changed versus HEAD.
	writeFixtureFile(t, f.dir, "legacy.go", legacyGoSource+"\n// touched\n")
	_, err = f.audit(t)
	mustErrContain(t, err, "touched file must be clean")

	// Committed on a branch, the same edit is caught through --base.
	if out, gerr := runFixtureGit(t, f.dir, env, "checkout", "-q", "-b", "feature"); gerr != nil {
		t.Fatalf("checkout: %v (%s)", gerr, out)
	}
	gitCommitAll(t, f.dir, env, "touch legacy")
	if _, err := f.audit(t); err != nil {
		t.Fatalf("clean tree without --base must pass: %v", err)
	}
	_, err = f.audit(t, "--base=main")
	mustErrContain(t, err, "touched file must be clean")

	// Fix the file, then grow the baseline for an untouched file: without a rationale the
	// growth guard fails, with one it warns and passes.
	writeFixtureFile(t, f.dir, "legacy.go", "package legacy\n")
	phantom := baseline.Infraction{RuleID: "HISS-07", FilePath: "other.go", LineNumber: 1, Fingerprint: "other.go:1:HISS-07"}
	f.writeBaseline(t, []baseline.Infraction{inf, phantom}, "")
	gitCommitAll(t, f.dir, env, "grow baseline")
	_, err = f.audit(t, "--base=main")
	mustErrContain(t, err, "HISS-13 debt ratchet")
	f.writeBaseline(t, []baseline.Infraction{inf, phantom}, "scanner rule added upstream")
	gitCommitAll(t, f.dir, env, "grow baseline with rationale")
	out, err = f.audit(t, "--base=main")
	if err != nil {
		t.Fatalf("recorded increase must pass: %v\n%s", err, out)
	}
	mustContain(t, out, "[WARN] HISS-13 debt ratchet", "scanner rule added upstream")

	// Negative: unresolvable or unsafe base refs, and --base outside a repository.
	_, err = f.audit(t, "--base=no-such-ref")
	mustErrContain(t, err, "does not resolve")
	_, err = f.audit(t, "--base=main;rm")
	mustErrContain(t, err, "invalid --base")
	plain := newAuditFixture(t)
	_, err = plain.audit(t, "--base=main")
	mustErrContain(t, err, "requires a git repository")
}

func TestAudit_Boundary_HooksGate(t *testing.T) {
	f := newAuditFixture(t)
	initGitFixture(t, f.dir)

	// CI skips local hook verification.
	t.Setenv("CI", "true")
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit in CI: %v\n%s", err, out)
	}
	mustContain(t, out, "CI environment detected")

	// Locally a missing pre-commit hook fails the gate (unless the developer routes
	// hooks elsewhere, which this fixture cannot control).
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	if hp, hpErr := runFixtureGit(t, f.dir, os.Environ(), "config", "--get", "core.hooksPath"); hpErr == nil && strings.TrimSpace(hp) != "" {
		t.Skipf("core.hooksPath=%s overrides the fixture hooks directory", strings.TrimSpace(hp))
	}
	_, err = f.audit(t)
	mustErrContain(t, err, "Pre-commit hook")

	// An installed hook passes; a missing lefthook.yml fails.
	writeFixtureFile(t, f.dir, ".git/hooks/pre-commit", "#!/bin/sh\nexit 0\n")
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("installed hook: %v\n%s", err, out)
	}
	mustContain(t, out, "Local Git hooks")
	if err := os.Remove(filepath.Join(f.dir, "lefthook.yml")); err != nil {
		t.Fatal(err)
	}
	_, err = f.audit(t)
	mustErrContain(t, err, "lefthook.yml configuration is missing")
}
