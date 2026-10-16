// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestAuditMutationExclusions_3D(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".cargo/mutants.toml", "exclude_re = [\"legacy\"]\nexclude_globs = [\"src/generated/**\"]\n")
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "add", ".cargo/mutants.toml"); err != nil {
		t.Fatalf("track mutation configuration: %v\n%s", err, out)
	}
	today := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	manifest := &config.Manifest{}
	if err := auditMutationExclusions(t.Context(), manifest, f.dir, today); err == nil ||
		!strings.Contains(err.Error(), ".cargo/mutants.toml has 2 mutation exclusion(s)") || !strings.Contains(err.Error(), "add rule mutants") {
		t.Fatalf("missing exception result = %v; want file, count and fix", err)
	}
	manifest.Exceptions = []config.Exception{{Rule: config.ExceptionRuleMutants, Path: ".cargo/mutants.toml", Reason: "legacy generated code", Expires: "2027-02-13"}}
	if err := auditMutationExclusions(t.Context(), manifest, f.dir, today); err == nil || !strings.Contains(err.Error(), "has 2 mutation exclusion(s)") {
		t.Fatalf("uncategorized exception result = %v; want file and count", err)
	}
	manifest.Exceptions[0].Category, manifest.Exceptions[0].Entered = "D", "2026-10-16"
	if err := auditMutationExclusions(t.Context(), manifest, f.dir, today); err != nil {
		t.Fatalf("live category D exception refused: %v", err)
	}
	if err := auditMutationExclusions(t.Context(), manifest, f.dir, time.Date(2027, 2, 14, 0, 0, 0, 0, time.UTC)); err == nil ||
		!strings.Contains(err.Error(), ".cargo/mutants.toml has 2 mutation exclusion(s)") {
		t.Fatalf("expired exception result = %v; want file and count", err)
	}
	writeFixtureFile(t, f.dir, ".cargo/mutants.toml", "exclude_re = []\nexclude_globs = []\n")
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "add", ".cargo/mutants.toml"); err != nil {
		t.Fatalf("track empty mutation configuration: %v\n%s", err, out)
	}
	if err := auditMutationExclusions(t.Context(), nil, f.dir, today); err != nil {
		t.Fatalf("empty exclusions need no entry: %v", err)
	}
}

func TestAuditMutationExclusionsIntegration(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".cargo/mutants.toml", "exclude_re = [\"legacy\"]\n")
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "add", ".cargo/mutants.toml"); err != nil {
		t.Fatalf("track mutation config: %v\n%s", err, out)
	}
	out, err := f.audit(t)
	if err == nil || !strings.Contains(err.Error(), ".cargo/mutants.toml has 1 mutation exclusion(s)") {
		t.Fatalf("full audit without an exception = %v\n%s", err, out)
	}
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+
		"exceptions:\n  - rule: mutants\n    path: .cargo/mutants.toml\n    reason: legacy generated code\n    expires: 2027-02-13\n")
	out, err = f.audit(t)
	if err == nil || !strings.Contains(err.Error(), ".cargo/mutants.toml has 1 mutation exclusion(s)") {
		t.Fatalf("full audit with an uncategorized exception = %v\n%s", err, out)
	}
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+
		"exceptions:\n  - rule: mutants\n    path: .cargo/mutants.toml\n    category: D\n    entered: 2026-10-16\n    reason: legacy generated code\n    expires: 2027-02-13\n")
	if out, err = f.audit(t); err != nil {
		t.Fatalf("full audit with categorized exception failed: %v\n%s", err, out)
	}
}

func TestAuditMutationExclusionCountMalformed(t *testing.T) {
	for index, text := range []string{"exclude_re = [\"open\"\n", "exclude_globs = [1]\n"} {
		if _, err := countCargoMutantsKeys(strings.Split(text, "\n")); err == nil {
			t.Errorf("case %d malformed exclusion array was accepted", index)
		}
	}
	count, err := countCargoMutantsKeys([]string{`"exclude_re" = [`, `"first",`, `# between values`, `"second"`, `]`, `[other]`, `exclude_globs = ["ignored"]`})
	if err != nil || count != 2 {
		t.Fatalf("quoted, multiline cargo-mutants keys = %d, %v; want 2", count, err)
	}
	if _, err := countCargoMutantsKeys([]string{`exclude_re = ["first"]`, `exclude_re = ["second"]`}); err == nil {
		t.Fatal("duplicate cargo-mutants exclusions were accepted")
	}
	if _, err := countStrykerExclusions([]byte(`{"mutate":["!a"],"mutate":["!b"]}`)); err == nil {
		t.Fatal("duplicate Stryker JSON exclusion key was accepted")
	}
	if _, err := countYAMLExclusions([]byte("unleash:\n  exclude-files: []\n  exclude-files: [x]\n"), "exclude-files"); err == nil {
		t.Fatal("duplicate Gremlins YAML exclusion key was accepted")
	}
}

func TestMutationToolConfigExclusionParsers(t *testing.T) {
	cases := []struct {
		name  string
		count int
		call  func() (int, error)
	}{
		{"mutmut pyproject", 1, func() (int, error) {
			return countMutmutExclusions("pyproject.toml", "[tool.mutmut]\ndo_not_mutate_patterns = [\"logger\\\\.info\"]\n")
		}},
		{"mutmut setup.cfg", 2, func() (int, error) {
			return countMutmutExclusions("setup.cfg", "[mutmut]\ndo_not_mutate =\n  *__tests.py\n  generated/*\n")
		}},
		{"Stryker JSON", 3, func() (int, error) {
			return countStrykerExclusions([]byte(`{"mutate":["!src/*.spec.ts"],"ignorePatterns":["dist"],"mutator":{"excludedMutations":["EqualityOperator"]}}`))
		}},
		{"PIT Maven", 3, func() (int, error) {
			return countPITExclusions([]byte(`<plugin><configuration><excludedClasses><param>a.*</param><param>b.*</param></excludedClasses><excludedMethods><param>x</param></excludedMethods></configuration></plugin>`))
		}},
		{"Gremlins YAML", 1, func() (int, error) {
			return countYAMLExclusions([]byte("unleash:\n  exclude-files:\n    - '.*_gen.go'\n"), "exclude-files")
		}},
		{"go-mutesting YAML", 2, func() (int, error) {
			return countGoMutestingExclusions([]byte("exclude_dirs: [vendor, testdata]\ndisable_mutators: []\n"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.call()
			if err != nil || got != tc.count {
				t.Fatalf("exclusion count = %d, %v; want %d", got, err, tc.count)
			}
		})
	}
}

func TestAuditMutationExceptionRuleRequiresPath(t *testing.T) {
	err := config.ValidateExceptions([]config.Exception{{Rule: config.ExceptionRuleMutants, Glob: "**/*.toml", Reason: "temporary", Expires: "2026-11-01"}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "one mutation-tool configuration file by path") {
		t.Fatalf("globbed mutants exception = %v", err)
	}
}
