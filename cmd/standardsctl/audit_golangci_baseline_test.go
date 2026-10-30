// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

func fixtureGolangciRules(rules ...string) string {
	var b strings.Builder
	b.WriteString("version: \"2\"\nlinters:\n  exclusions:\n    rules:\n")
	b.WriteString("      " + adopt.GeneratedExceptionsBegin + "\n")
	for i := 0; i < len(rules) && i < 100; i++ {
		b.WriteString(rules[i])
	}
	b.WriteString("      " + adopt.GeneratedExceptionsEnd + "\n")
	return b.String()
}

func fixtureBaselineRule(path, linter, fn string) string {
	return fmt.Sprintf("      # baseline #1030\n      - path: ^%s$\n        linters:\n          - %s\n        text: func `%s`\n",
		regexp.QuoteMeta(path), linter, fn)
}

func TestAudit_GolangciBaselineGrowth_Positive_Shrink(t *testing.T) {
	t.Setenv("CI", "true")
	f := newAuditFixture(t)
	ruleA := fixtureBaselineRule("pkg/a.go", "gocyclo", "TestA")
	ruleB := fixtureBaselineRule("pkg/b.go", "gocognit", "TestB")
	writeFixtureFile(t, f.dir, ".golangci.yml", fixtureGolangciRules(ruleA, ruleB))
	gitCommitAll(t, f.dir, f.gitEnv, "add baseline rules on main")

	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "checkout", "-q", "-b", "feature"); err != nil {
		t.Fatalf("checkout: %v (%s)", err, out)
	}
	writeFixtureFile(t, f.dir, ".golangci.yml", fixtureGolangciRules(ruleA))
	out, err := f.audit(t, "--base=main")
	if err != nil {
		t.Fatalf("shrink failed: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] .golangci.yml baseline: does not grow versus main (2 -> 1)")
}

func TestAudit_GolangciBaselineGrowth_Negative_HandAddedRuleRefused(t *testing.T) {
	t.Setenv("CI", "true")
	f := newAuditFixture(t)
	ruleA := fixtureBaselineRule("pkg/a.go", "gocyclo", "TestA")
	writeFixtureFile(t, f.dir, ".golangci.yml", fixtureGolangciRules(ruleA))
	gitCommitAll(t, f.dir, f.gitEnv, "add baseline rule on main")

	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "checkout", "-q", "-b", "feature"); err != nil {
		t.Fatalf("checkout: %v (%s)", err, out)
	}
	rulePlanted := fixtureBaselineRule("pkg/planted.go", "gocyclo", "TestPlanted")
	writeFixtureFile(t, f.dir, ".golangci.yml", fixtureGolangciRules(ruleA, rulePlanted))
	_, err := f.audit(t, "--base=main")
	for _, needle := range []string{"[FAIL]", ".golangci.yml", "pkg/planted.go", "gocyclo", "TestPlanted", "fix it, a baseline never grows"} {
		mustErrContain(t, err, needle)
	}
}

func TestAudit_GolangciBaselineGrowth_Boundary_BaseWithoutBaselineRules(t *testing.T) {
	t.Setenv("CI", "true")
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".golangci.yml", "version: \"2\"\nlinters:\n  default: none\n")
	gitCommitAll(t, f.dir, f.gitEnv, "add golangci without baseline on main")

	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "checkout", "-q", "-b", "feature"); err != nil {
		t.Fatalf("checkout: %v (%s)", err, out)
	}
	ruleA := fixtureBaselineRule("pkg/a.go", "gocyclo", "TestA")
	writeFixtureFile(t, f.dir, ".golangci.yml", fixtureGolangciRules(ruleA))
	out, err := f.audit(t, "--base=main")
	if err != nil {
		t.Fatalf("boundary without base baseline failed: %v\n%s", err, out)
	}
	mustContain(t, out, "nothing to compare")
}

func TestAudit_GolangciBaselineGrowth_Negative_UnreadableBase(t *testing.T) {
	t.Setenv("CI", "true")
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".golangci.yml", "version: \"2\"\nlinters: [unclosed\n")
	gitCommitAll(t, f.dir, f.gitEnv, "add unreadable golangci on main")

	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "checkout", "-q", "-b", "feature"); err != nil {
		t.Fatalf("checkout: %v (%s)", err, out)
	}
	ruleA := fixtureBaselineRule("pkg/a.go", "gocyclo", "TestA")
	writeFixtureFile(t, f.dir, ".golangci.yml", fixtureGolangciRules(ruleA))
	_, err := f.audit(t, "--base=main")
	mustErrContain(t, err, "[FAIL]")
	mustErrContain(t, err, ".golangci.yml")
	mustErrContain(t, err, "is unreadable")
}

func TestAudit_GolangciBaselineGrowth_Boundary_NoBaseRef(t *testing.T) {
	t.Setenv("CI", "true")
	f := newAuditFixture(t)
	ruleA := fixtureBaselineRule("pkg/a.go", "gocyclo", "TestA")
	writeFixtureFile(t, f.dir, ".golangci.yml", fixtureGolangciRules(ruleA))
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit without base failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "baseline never grows") || strings.Contains(out, "baseline: does not grow") {
		t.Fatalf("unexpected baseline growth message without --base: %s", out)
	}
}
