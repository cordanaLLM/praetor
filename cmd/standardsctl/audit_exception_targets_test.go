// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// targetsToday is the fixed day the existence-check tests judge expiry against.
var targetsToday = time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)

// exceptionTargetRoot returns a fresh git repository holding files, untracked: the glob check
// lists the untracked files git does not ignore, as a checkout under work would hold them.
func exceptionTargetRoot(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range files {
		writeFixtureFile(t, dir, rel, "x\n")
	}
	if out, err := runFixtureGit(t, dir, nil, "init", "-q", "-b", "main"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

// liveEntry is an entry that holds on targetsToday.
func liveEntry(rule, path, glob string) config.Exception {
	return config.Exception{Rule: rule, Path: path, Glob: glob, Reason: "debt", Category: "A", Expires: "2026-11-30"}
}

// judgeTargets runs the existence check over entries in root and returns its output and error.
func judgeTargets(t *testing.T, root string, entries ...config.Exception) (string, error) {
	t.Helper()
	return captureStdout(t, func() error {
		return auditExceptionsExpiry(context.Background(), &config.Manifest{Exceptions: entries}, root, targetsToday)
	})
}

// Positive (#1259): an entry whose path exists, a glob that matches a file and a tool entry,
// which names no file, all pass without a [FAIL] line.
func TestAuditExceptionTargets_ExistingTargetsPass(t *testing.T) {
	root := exceptionTargetRoot(t, ".github/workflows/ci.yml", "src/gen/a.pb.go")
	out, err := judgeTargets(t, root,
		liveEntry(config.ExceptionRuleBuildWarnings, ".github/workflows/ci.yml", ""),
		liveEntry(config.ExceptionRuleClangTidyCoverage, "", "src/gen/*.pb.go"),
		config.Exception{Rule: config.ExceptionRuleDeclaredLinters, Tool: "gitleaks", Reason: "debt", Category: "A", Expires: "2026-11-30"},
	)
	if err != nil {
		t.Fatalf("existing targets failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "[FAIL]") {
		t.Fatalf("existing targets printed a failure:\n%s", out)
	}
}

// Negative (#1259): a path that names no file fails, naming the entry and the missing path, and
// a live entry says nothing about an expiry.
func TestAuditExceptionTargets_MissingPathFails(t *testing.T) {
	root := exceptionTargetRoot(t, ".github/workflows/ci.yml")
	_, err := judgeTargets(t, root, liveEntry(config.ExceptionRuleBuildWarnings, ".github/workflows/gone.yml", ""))
	mustErrContain(t, err, "[FAIL] Exceptions: the exceptions entry (rule HISS-10, .github/workflows/gone.yml) names the path "+
		".github/workflows/gone.yml, which matches no file in the repository, so it excuses nothing")
	if strings.Contains(err.Error(), "also expired") {
		t.Fatalf("a live missing entry was reported as expired: %v", err)
	}
}

// Boundary (#1259): an entry both expired and missing reports both; a glob matching nothing
// fails like a missing path; every stale entry is named, not only the first.
func TestAuditExceptionTargets_Boundaries(t *testing.T) {
	root := exceptionTargetRoot(t, "src/main.go")
	expired := liveEntry(config.ExceptionRuleBuildWarnings, ".github/workflows/old.yml", "")
	expired.Expires = "2026-10-01"
	_, err := judgeTargets(t, root, expired)
	mustErrContain(t, err, "(rule HISS-10, .github/workflows/old.yml) names the path .github/workflows/old.yml, which matches no file")
	mustErrContain(t, err, "The entry also expired on 2026-10-01.")

	_, err = judgeTargets(t, root, liveEntry(config.ExceptionRuleClangTidyCoverage, "", "src/gen/*.pb.go"))
	mustErrContain(t, err, "(rule clang-tidy-coverage, src/gen/*.pb.go) names the glob src/gen/*.pb.go, which matches no file")

	_, err = judgeTargets(t, root,
		liveEntry(config.ExceptionRuleDedupe, "gen/a.go", ""),
		liveEntry(config.ExceptionRuleDedupe, "gen/b.go", ""))
	mustErrContain(t, err, "(rule HISS-19, gen/a.go)")
	mustErrContain(t, err, "(rule HISS-19, gen/b.go)")
}

// Boundary (#1259, decision office-general-engine-1259-hiss11): the HISS-11 entry adoption
// records for the release workflow still to be added passes while no workflow writes
// provenance, saying it is planned and naming its expiry; expired, it is left to the HISS-11 gate
// (TestAdoptThenAuditDeclaresTheSupplyChainGap). Another missing HISS-11 path fails, and once a
// provenance workflow is planted the planned entry fails like any stale one.
func TestAuditExceptionTargets_PlannedReleaseWorkflow(t *testing.T) {
	root := exceptionTargetRoot(t)
	writeFixtureFile(t, root, ".github/workflows/ci.yml", fixtureNativeWorkflow(""))
	planned := liveEntry(config.ExceptionRuleSupplyChain, ".github/workflows/release.yml", "")
	out, err := judgeTargets(t, root, planned)
	if err != nil {
		t.Fatalf("the planned release workflow failed while no workflow writes provenance: %v\n%s", err, out)
	}
	mustContain(t, out, "[INFO] Exceptions: the exceptions entry (rule HISS-11, .github/workflows/release.yml) names a "+
		"planned file, not stale", "it holds until 2026-11-30.")

	_, err = judgeTargets(t, root, liveEntry(config.ExceptionRuleSupplyChain, ".github/workflows/other.yml", ""))
	mustErrContain(t, err, "(rule HISS-11, .github/workflows/other.yml) names the path .github/workflows/other.yml, which matches no file")

	expired := planned
	expired.Expires = "2026-10-01"
	out, err = judgeTargets(t, root, expired)
	if err != nil || strings.Contains(out, "planned file") {
		t.Fatalf("an expired planned entry is the HISS-11 gate's to fail by its expiry, got (%q, %v)", out, err)
	}

	writeFixtureFile(t, root, ".github/workflows/publish.yml", fixtureReleaseWorkflow)
	out, err = judgeTargets(t, root, planned)
	mustErrContain(t, err, "(rule HISS-11, .github/workflows/release.yml) names the path .github/workflows/release.yml, which matches no file")
	if strings.Contains(out, "planned file") {
		t.Fatalf("the entry stayed planned after a workflow began writing provenance:\n%s", out)
	}
}

// The full audit stops at the existence check: a HISS-10 entry for a workflow that is gone fails
// the audit with the line naming it (#1259).
func TestAudit_ExceptionMissingPath_Integration(t *testing.T) {
	f := newAuditFixture(t)
	expires := time.Now().AddDate(0, 0, 30).Format(config.ExceptionDateLayout)
	entry := fmt.Sprintf("exceptions:\n  - rule: HISS-10\n    path: .github/workflows/build.yml\n    reason: compiler upgrade pending\n    category: A\n    expires: %q\n", expires)
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+entry)
	out, err := f.audit(t)
	if err == nil {
		t.Fatalf("audit passed an exception for a missing workflow:\n%s", out)
	}
	mustErrContain(t, err, "[FAIL] Exceptions: the exceptions entry (rule HISS-10, .github/workflows/build.yml) names the path "+
		".github/workflows/build.yml, which matches no file in the repository")
}

// Positive and negative (order engine-secretscan-foreign-remote-refs-20261025): a HISS-12 entry
// names the file of a finding in history, which the tree may no longer hold, so the tree check
// passes it (the secret gate fails an entry no finding uses); the same missing path under
// another rule still fails.
func TestAuditExceptionTargets_SecretsHistoryPath(t *testing.T) {
	root := exceptionTargetRoot(t, "src/main.go")
	secret := liveEntry(config.ExceptionRuleSecrets, "logs/history.txt", "")
	secret.Tool = "gcp-api-key"
	out, err := judgeTargets(t, root, secret)
	if err != nil || strings.Contains(out, "[FAIL]") {
		t.Fatalf("a HISS-12 entry for a history-only path failed the tree check: (%q, %v)", out, err)
	}

	_, err = judgeTargets(t, root, liveEntry(config.ExceptionRuleDedupe, "logs/history.txt", ""))
	mustErrContain(t, err, "(rule HISS-19, logs/history.txt) names the path logs/history.txt, which matches no file")
}
