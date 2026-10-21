// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/lockdown"
)

func TestRacePackagesStageAndSignedOutput(t *testing.T) {
	cases := []struct {
		name     string
		packages []string
		want     []string
	}{
		{"omitted full suite", nil, []string{"./..."}},
		{"no affected package", []string{}, nil},
		{"package and reverse dependent", []string{"example.test/a", "example.test/b"}, []string{"example.test/a", "example.test/b"}},
		{"testdata package", []string{"example.test/p"}, []string{"example.test/p"}},
		{"embedded markdown package", []string{"example.test/q"}, []string{"example.test/q"}},
		{"module configuration", []string{"./..."}, []string{"./..."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newHermeticGitRepo(t)
			seedGoModule(t, dir)
			cfg, recorded := newTestConfig(t, dir, false)
			cfg.racePackages, cfg.rep.RacePackages = tc.packages, tc.packages
			if err := executeStage(t.Context(), stage{stageTests, runTestStage}, cfg); err != nil {
				t.Fatal(err)
			}
			assertRacePackageEvidence(t, cfg.rep, *recorded, tc.want)
		})
	}
}

func assertRacePackageEvidence(t *testing.T, rep *PipelineReport, recorded []recordedCommand, want []string) {
	t.Helper()
	invocations := testInvocations(recorded)
	stage := rep.Stages[0]
	output := string(rep.StageOutput())
	if want == nil {
		if len(invocations) != 0 || stage.Status != StageNotApplicable {
			t.Fatalf("empty scope must be not_applicable with reason: stage=%+v invocations=%v output=%s", stage, invocations, output)
		}
		if !strings.Contains(output, lockdown.NoAffectedGoPackage) {
			t.Fatalf("signed output omitted empty-scope reason: %s", output)
		}
		return
	}
	if stage.Status != StagePassed || len(invocations) != 1 {
		t.Fatalf("race stage did not run exactly once: stage=%+v commands=%v", stage, invocations)
	}
	assertRaceInvocation(t, invocations[0].args, want)
	if rep.RacePackages != nil && !strings.Contains(output, lockdown.RacePackagesReason(want)) {
		t.Fatalf("signed output omitted scoped packages and reason: %s", output)
	}
}

func assertRaceInvocation(t *testing.T, args, want []string) {
	t.Helper()
	if len(args) < 4 || !reflect.DeepEqual(args[len(args)-len(want):], want) {
		t.Fatalf("race packages: args=%v want=%v", args, want)
	}
}

func TestRacePackagesMintSignedReceipt(t *testing.T) {
	for _, packages := range [][]string{{}, {"example.test/a", "example.test/b"}} {
		t.Run(lockdown.RacePackagesLine(packages), func(t *testing.T) {
			key := sandboxReceiptKey(t)
			cfg := pinnedReceiptConfig(t, pinManifest(key))
			cfg.rep.RacePackages = packages
			result := StageResult{Name: stageTests, Status: StagePassed, Message: lockdown.RacePackagesReason(packages)}
			if len(packages) == 0 {
				result.Status, result.Message = StageNotApplicable, lockdown.NoAffectedGoPackage
			}
			cfg.rep.Stages = append(cfg.rep.Stages, result)
			if _, err := runReceiptStage(t.Context(), cfg); err != nil {
				t.Fatalf("scoped receipt not minted: %v", err)
			}
			requireReceiptSignedBy(t, cfg, key)
		})
	}
}

func TestRacePackagesNonePreservesNodeTestsAndReceipt(t *testing.T) {
	dir := newHermeticGitRepo(t)
	seedNodeWorkspace(t, dir, "package-lock.json", map[string]string{"test": "vitest"})
	cfg, recorded := newTestConfig(t, dir, false)
	cfg.racePackages, cfg.rep.RacePackages = []string{}, []string{}
	cfg.rep.Repository, cfg.rep.CommitSHA, cfg.rep.WorktreeClean = "example/test", "abc", true
	if err := executeStage(t.Context(), stage{stageTests, runTestStage}, cfg); err != nil {
		t.Fatal(err)
	}
	result := cfg.rep.Stages[0]
	if result.Status != StageSkipped || !strings.Contains(result.Message, lockdown.NoAffectedGoPackage) {
		t.Fatalf("empty Go scope must remain explicit beside Node tests: %+v", result)
	}
	if len(testInvocations(*recorded)) != 0 || !strings.Contains(strings.Join(commandLines(*recorded), "\n"), "npm run test") {
		t.Fatalf("empty Go scope suppressed Node or ran Go: %+v", *recorded)
	}
	key := sandboxReceiptKey(t)
	if _, err := runReceiptStage(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	requireReceiptSignedBy(t, cfg, key)
}
