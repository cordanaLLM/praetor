package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
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
		}, `Paperclip harness platform mismatch: got "other/repo"`},
		{"harness mismatch remedy", func(t *testing.T, f *auditFixture) {
			writeFixtureFile(t, f.dir, ".paperclip/harness.json", `{"version":1,"platform":"other/repo","operating_contract":["x"],"agit_push_format":"fixture push","invariants":["fixture invariant"]}`)
		}, "run '" + adopt.ForceCommand("") + "' to set platform (every other harness value kept); " +
			"with adoption.decline listing paperclip, adopt never writes the harness, so set platform by hand"},
		{"new violation", func(t *testing.T, f *auditFixture) {
			f.addViolation(t)
		}, "HISS invariant violations the baseline does not record"},
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
			// compile-context writes the targets, then fails on the lint the audit applies.
			out, err := runCompileContextCmd(t, f.dir)
			mustErrContain(t, err, "AGENTS.md fails the caveman lint")
			mustContain(t, out, "[COMPILED] CLAUDE.md")
		}, "AGENTS.md fails the caveman lint"},
		{"evidence directory not ignored", func(t *testing.T, f *auditFixture) {
			// The fixture enables no docs:* facet; the evidence check runs without one.
			writeFixtureFile(t, f.dir, ".gitignore", ".workingdir/*\n!.workingdir/evidence/\n")
		}, "Agent context evidence directory: git does not ignore .workingdir/evidence/"},
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

// auditReadmeFixture is the README block the audit fixture's clean baseline renders without
// the documentation gate, spelled out, with badge as its first line and agentsDefinition
// after the badge image's reference definition.
func auditReadmeFixture(badge, agentsDefinition string) string {
	return `# Widgets

<!-- praetor:readme-governance:start -->
` + badge + `

Praetor manages this repository's declared governance policy. This managed
block records adoption state; it is not a verification certificate.

**Verification**: ` + "`make verify-all`" + ` runs the repository's configured
verification cascade.

**HISS Audit**: ` + "`praetorctl audit`" + ` enforces policy, generated-surface
integrity, and the debt ratchet.

**Context Sync**: ` + "`praetorctl compile-context --verify`" + ` verifies every
generated agent context against ` + "`AGENTS.md`" + `.

**Debt Baseline**: ` + "`.standards-baseline.json`" + ` anchors the debt ratchet at
0 recorded infractions; audit forbids growth.

[praetor-hiss-badge]: https://img.shields.io/badge/Standards-HISS%20Adopted-blue
` + agentsDefinition + `<!-- praetor:readme-governance:end -->
`
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
		writeFixtureFile(t, f.dir, "README.md", auditReadmeFixture("![HISS Adopted][praetor-hiss-badge]", ""))
		out, err := f.audit(t)
		if err != nil {
			t.Fatalf("current README governance: %v\n%s", err, out)
		}
		mustContain(t, out, "[PASS] README governance block verified")
	})

	// #506: the block an earlier Praetor wrote linked AGENTS.md by a repository-relative
	// path; audit reports it stale and names plain adoption as the repair.
	t.Run("previous relative AGENTS.md link fails", func(t *testing.T) {
		f := newAuditFixture(t)
		writeFixtureFile(t, f.dir, "README.md", auditReadmeFixture("[![HISS Adopted][praetor-hiss-badge]](AGENTS.md)", ""))
		_, err := f.audit(t)
		mustErrContain(t, err, "README governance block is stale; run praetorctl adopt")
	})

	// The forge host is the one fact the block records that the manifest does not: audit
	// reads it back from the AGENTS.md link and accepts only the exact link a known forge
	// serves for the manifest's acme/widgets, so a checkout's remote never changes the verdict.
	linked := "[![HISS Adopted][praetor-hiss-badge]][praetor-hiss-agents]"
	t.Run("AGENTS.md link on a known forge passes", func(t *testing.T) {
		for _, link := range []string{
			"https://github.com/acme/widgets/blob/HEAD/AGENTS.md",
			"https://gitlab.com/acme/widgets/-/blob/HEAD/AGENTS.md",
		} {
			f := newAuditFixture(t)
			writeFixtureFile(t, f.dir, "README.md", auditReadmeFixture(linked, "[praetor-hiss-agents]: "+link+"\n"))
			out, err := f.audit(t)
			if err != nil {
				t.Fatalf("%s: %v\n%s", link, err, out)
			}
			mustContain(t, out, "[PASS] README governance block verified")
		}
	})

	t.Run("AGENTS.md link into another repository or onto an unknown forge fails", func(t *testing.T) {
		for _, link := range []string{
			"https://github.com/other/widgets/blob/HEAD/AGENTS.md",
			"https://git.example.org/acme/widgets/blob/HEAD/AGENTS.md",
			"https://gitlab.com/acme/widgets/blob/HEAD/AGENTS.md",
		} {
			f := newAuditFixture(t)
			writeFixtureFile(t, f.dir, "README.md", auditReadmeFixture(linked, "[praetor-hiss-agents]: "+link+"\n"))
			_, err := f.audit(t)
			mustErrContain(t, err, "README governance block is stale; run praetorctl adopt")
		}
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

// TestAudit_InvariantVerdictNamesItsLanguages is the #589 regression. Positive: the PASS line
// names the languages the scan examined, TypeScript and Svelte included. Negative: source in a
// language no scanner reads is reported as unscanned, never folded into the PASS, and a tree
// whose only source is unscanned gets no PASS line at all. Boundary: a tree with no source keeps
// the plain PASS, since nothing went unexamined.
func TestAudit_InvariantVerdictNamesItsLanguages(t *testing.T) {
	f := newAuditFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit without source: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] HISS invariant scan verified: 0 active violations")
	if strings.Contains(out, "[UNSCANNED]") {
		t.Fatalf("a tree without source reports unscanned source:\n%s", out)
	}

	writeFixtureFile(t, f.dir, "prompt.zsh", "#!/bin/zsh\necho deploy\n")
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit with only unscanned source: %v\n%s", err, out)
	}
	mustContain(t, out, "[UNSCANNED] HISS invariant scan read no source file: 0 active violations",
		"[UNSCANNED] No HISS rule examined zsh (1 file); the invariants are unverified there, not passed.")
	if strings.Contains(out, "[PASS] HISS invariant scan verified") {
		t.Fatalf("a PASS over source no scanner read:\n%s", out)
	}

	writeFixtureFile(t, f.dir, "ui/app.ts", "export const answer = (): number => {\n  return 42;\n};\n")
	writeFixtureFile(t, f.dir, "ui/App.svelte", "<script lang=\"ts\">\n  export let label = '';\n</script>\n<p>{label}</p>\n")
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit with scanned and unscanned source: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] HISS invariant scan verified for svelte, typescript: 0 active violations",
		"[UNSCANNED] No HISS rule examined zsh (1 file)")
}

// TestAudit_ShellSystemdAnsibleAreScanned: Positive (#182): a strict shell script, a bounded unit
// and a playbook whose task states its change are read and named on the PASS line. Negative: debt
// in any one of them is a new infraction the ratchet refuses, where it used to pass unscanned.
func TestAudit_ShellSystemdAnsibleAreScanned(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "tools/build.sh", "#!/bin/sh\nset -eu\necho build\n")
	writeFixtureFile(t, f.dir, "units/app.service", "[Service]\nExecStart=/usr/bin/app\n")
	writeFixtureFile(t, f.dir, "site.yml", "- hosts: all\n  tasks:\n    - ansible.builtin.command: uptime\n      changed_when: false\n")
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit over clean shell, unit and playbook: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] HISS invariant scan verified for ansible, shell, systemd: 0 active violations")
	for path, body := range map[string]string{
		"tools/build.sh":    "#!/bin/sh\nset -eu\neval \"$1\"\n",
		"units/app.service": "[Service]\nType=oneshot\nExecStart=/usr/bin/app\n",
		"site.yml":          "- hosts: all\n  tasks:\n    - ansible.builtin.command: uptime\n",
	} {
		g := newAuditFixture(t)
		writeFixtureFile(t, g.dir, path, body)
		_, err := g.audit(t)
		mustErrContain(t, err, "HISS invariant violations the baseline does not record")
	}
}

// TestAudit_ScriptViolationFailsTheRatchet: Positive (#589): an eval in a Svelte component is
// now a HISS-08 infraction the ratchet refuses, where it used to pass unscanned.
func TestAudit_ScriptViolationFailsTheRatchet(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "ui/Probe.svelte", "<script>\n  eval(code);\n</script>\n")
	_, err := f.audit(t)
	mustErrContain(t, err, "HISS invariant violations the baseline does not record")
}

func TestAudit_TouchedFileCleanRule(t *testing.T) {
	f := newAuditFixture(t)
	inf := f.addViolation(t)

	// Baselined debt in an untouched file passes.
	f.writeBaseline(t, []baseline.Infraction{inf}, "")
	gitCommitAll(t, f.dir, f.gitEnv, "baseline legacy fixture")
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
	gitCommitAll(t, f.dir, f.gitEnv, "baseline growth fixture")
	env := f.gitEnv

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
	if err := os.RemoveAll(filepath.Join(plain.dir, ".git")); err != nil {
		t.Fatal(err)
	}
	_, err = plain.audit(t, "--base=main")
	mustErrContain(t, err, "requires a git repository")
}

func TestAudit_Boundary_HooksGate(t *testing.T) {
	f := newAuditFixture(t)
	if err := os.Remove(filepath.Join(f.dir, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}

	// CI skips local hook verification and names the runner configuration it verified.
	t.Setenv("CI", "true")
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit in CI: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] CI environment detected: lefthook configuration lefthook.yml verified")

	// Locally a missing pre-commit hook fails the gate (unless the developer routes
	// hooks elsewhere, which this fixture cannot control).
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	if hp, hpErr := runFixtureGit(t, f.dir, os.Environ(), "config", "--get", "core.hooksPath"); hpErr == nil && strings.TrimSpace(hp) != "" {
		t.Skipf("core.hooksPath=%s overrides the fixture hooks directory", strings.TrimSpace(hp))
	}
	_, err = f.audit(t)
	mustErrContain(t, err, "Pre-commit hook")
	mustErrContain(t, err, "is missing")

	// A placeholder no runner wrote fails, naming what was found (#175); lefthook's hook passes
	// and the line names lefthook; a missing lefthook.yml fails.
	writeFixtureHook(t, f.dir, ".git/hooks/pre-commit", "#!/bin/sh\nexit 0\n")
	_, err = f.audit(t)
	mustErrContain(t, err, "was not written by a known hook runner")
	mustErrContain(t, err, `whose first line is "#!/bin/sh"`)
	writeFixtureHook(t, f.dir, ".git/hooks/pre-commit", fixtureLefthookHook)
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("installed hook: %v\n%s", err, out)
	}
	mustContain(t, out, "Local Git hooks", "via lefthook, lefthook.yml) verified active")
	if err := os.Remove(filepath.Join(f.dir, "lefthook.yml")); err != nil {
		t.Fatal(err)
	}
	_, err = f.audit(t)
	mustErrContain(t, err, "lefthook.yml configuration is missing")
}

// fixturePreCommitFrameworkHook is the part of the hook pre-commit 4.6.2 installs that the audit
// recognises: the ID line of resources/hook-tmpl and the templated --config argument.
const fixturePreCommitFrameworkHook = "#!/usr/bin/env bash\n# File generated by pre-commit: https://pre-commit.com\n" +
	"# ID: 138fd403232d2ddd5efb44317e38bf03\n\n# start templated\n" +
	"ARGS=(hook-impl --config=.pre-commit-config.yaml --hook-type=pre-commit)\n# end templated\n"

// TestAudit_Positive_PreCommitFrameworkRunner (#175): without lefthook.yml, the pre-commit
// framework's hook and a configuration that runs both praetor commands pass the CLI audit and
// the line names the framework; the same hook beside a configuration without the audit fails.
func TestAudit_Positive_PreCommitFrameworkRunner(t *testing.T) {
	f := newAuditFixture(t)
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	if hp, hpErr := runFixtureGit(t, f.dir, os.Environ(), "config", "--get", "core.hooksPath"); hpErr == nil && strings.TrimSpace(hp) != "" {
		t.Skipf("core.hooksPath=%s overrides the fixture hooks directory", strings.TrimSpace(hp))
	}
	if err := os.Remove(filepath.Join(f.dir, "lefthook.yml")); err != nil {
		t.Fatal(err)
	}
	local := "repos:\n  - repo: local\n    hooks:\n" +
		"      - {id: context, name: context, entry: praetorctl compile-context --verify, language: system, pass_filenames: false}\n"
	writeFixtureFile(t, f.dir, ".pre-commit-config.yaml", local+
		"      - {id: audit, name: audit, entry: praetorctl audit --offline, language: system, pass_filenames: false}\n")
	writeFixtureHook(t, f.dir, ".git/hooks/pre-commit", fixturePreCommitFrameworkHook)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("pre-commit framework runner: %v\n%s", err, out)
	}
	mustContain(t, out, "via the pre-commit framework, .pre-commit-config.yaml) verified active")

	writeFixtureFile(t, f.dir, ".pre-commit-config.yaml", local)
	_, err = f.audit(t)
	mustErrContain(t, err, "does not run 'praetorctl audit'")
}
