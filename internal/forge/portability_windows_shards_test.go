// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// portabilityWindowsShardsProblem checks the Windows shard split and aggregator contract on raw
// portability.yml bytes, returning "" when satisfied or a diagnostic naming the violation.
func portabilityWindowsShardsProblem(raw []byte, identity string) string {
	var strict map[string]any
	if err := yaml.Unmarshal(raw, &strict); err != nil {
		return fmt.Sprintf("yaml decode: %v", err)
	}
	spec, err := ghworkflow.Parse(raw)
	if err != nil {
		return fmt.Sprintf("parse workflow: %v", err)
	}
	contexts, err := workflowContextsIn(raw, identity)
	if err != nil {
		return fmt.Sprintf("workflowContextsIn: %v", err)
	}
	wantContexts := []string{
		"Platform Neutrality (Linux)",
		"Platform Neutrality (macOS)",
		"Platform Neutrality (Windows)",
	}
	if !slices.Equal(contexts, wantContexts) {
		return fmt.Sprintf("contexts = %v, want %v", contexts, wantContexts)
	}
	winJob, ok := spec.Jobs["windows"]
	if !ok {
		return "missing windows aggregator job"
	}
	if covered := provenAggregateNeeds(&spec, &winJob); !slices.Equal(covered, []string{"windows-shard"}) {
		return fmt.Sprintf("provenAggregateNeeds(windows) = %v, want [windows-shard]", covered)
	}
	return ""
}

// runWindowsShardSelection executes the awk shard filter extracted from windows-shard's Test Every
// Package step for the given shard ("1" or "2") against lines.
func runWindowsShardSelection(t *testing.T, stepRun, shard string, packages []string) []string {
	t.Helper()
	const wantAwk = `LC_ALL=C sort | awk -v shard="$SHARD" 'NR % 2 == (shard % 2)'`
	if !strings.Contains(stepRun, wantAwk) {
		t.Fatalf("windows-shard Test Every Package step missing %q", wantAwk)
	}
	// The shard number goes into the filter text, not the environment: util.RunCommandStream runs
	// with the shared command environment and its deadline.
	filter := strings.Replace(wantAwk, `"$SHARD"`, shard, 1)
	var out bytes.Buffer
	stdin := strings.NewReader(strings.Join(packages, "\n") + "\n")
	if _, err := util.RunCommandStream(t.Context(), "", "bash", stdin, &out, 1<<20, "-c", filter); err != nil {
		t.Fatalf("shard %s selection failed: %v", shard, err)
	}
	trimmed := strings.TrimSpace(out.String())
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// Positive: portability.yml reports the three required contexts, windows proves coverage of
// windows-shard, and the two shards partition the run-1 package list with empty intersection.
func TestPortabilityWindowsShards_Positive(t *testing.T) {
	workflows, identity := engineWorkflows(t)
	raw := workflows["portability.yml"]
	if problem := portabilityWindowsShardsProblem(raw, identity); problem != "" {
		t.Fatal(problem)
	}
	spec, err := ghworkflow.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	step, found := findJobStep(spec.Jobs["windows-shard"], "Test Every Package")
	if !found {
		t.Fatal("windows-shard missing Test Every Package step")
	}
	run1 := []string{
		"github.com/cordanaLLM/praetor/cmd/standards-mcp",
		"github.com/cordanaLLM/praetor/internal/agent",
		"github.com/cordanaLLM/praetor/internal/config",
		"github.com/cordanaLLM/praetor/internal/forge",
		"github.com/cordanaLLM/praetor/internal/ghworkflow",
		"github.com/cordanaLLM/praetor/internal/hiss",
		"github.com/cordanaLLM/praetor/internal/util",
	}
	shard1 := runWindowsShardSelection(t, step.Run, "1", run1)
	shard2 := runWindowsShardSelection(t, step.Run, "2", run1)
	if len(shard1) == 0 || len(shard2) == 0 {
		t.Fatalf("empty shard: shard1=%v shard2=%v", shard1, shard2)
	}
	for i := 0; i < len(shard1) && i < 64; i++ {
		if slices.Contains(shard2, shard1[i]) {
			t.Fatalf("package %q selected by both shards", shard1[i])
		}
	}
	union := append(slices.Clone(shard1), shard2...)
	slices.Sort(union)
	want := slices.Clone(run1)
	slices.Sort(want)
	if !slices.Equal(union, want) {
		t.Fatalf("shard union = %v, want %v", union, want)
	}
}

// Negative: dropping the failure condition, dropping always(), or changing the aggregator's
// repository guard by one character makes provenAggregateNeeds return nil and fails the check, and
// aggregateFails reports fails=true on failure for the real workflow.
func TestPortabilityWindowsShards_Negative(t *testing.T) {
	workflows, identity := engineWorkflows(t)
	raw := string(workflows["portability.yml"])
	for name, mutated := range map[string]string{
		"dropped failure condition": strings.Replace(raw,
			"contains(needs.*.result, 'failure') || ",
			"", 1),
		"dropped cancelled condition": strings.Replace(raw,
			" || contains(needs.*.result, 'cancelled')", "", 1),
		"dropped always()": strings.Replace(raw, " && always()", "", 1),
		"guard differs by one character": strings.Replace(raw,
			"PRAETOR_FORK_PORTABILITY == 'enabled') && always()",
			"PRAETOR_FORK_PORTABILITY == 'enablex') && always()", 1),
	} {
		if mutated == raw {
			t.Fatalf("%s: mutation did not apply", name)
		}
		spec, err := ghworkflow.Parse([]byte(mutated))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		winJob := spec.Jobs["windows"]
		if got := provenAggregateNeeds(&spec, &winJob); got != nil {
			t.Fatalf("%s: provenAggregateNeeds = %v, want nil", name, got)
		}
		if problem := portabilityWindowsShardsProblem([]byte(mutated), identity); problem == "" {
			t.Fatalf("%s: broken aggregator passed", name)
		}
	}
	spec, err := ghworkflow.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	winJob := spec.Jobs["windows"]
	steps, _, shaped := aggregateShape(&spec, &winJob)
	if !shaped {
		t.Fatal("windows job is not a shaped aggregate")
	}
	if fails, known := aggregateFails(steps, map[string]string{"windows-shard": needsResultFailure}); !known || !fails {
		t.Fatalf("result %q: fails = %t, known = %t; want true, true", needsResultFailure, fails, known)
	}
}

// Boundary: cancelled and skipped fail the aggregate while success passes it.
func TestPortabilityWindowsShards_Boundary(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	spec, err := ghworkflow.Parse(workflows["portability.yml"])
	if err != nil {
		t.Fatal(err)
	}
	winJob := spec.Jobs["windows"]
	steps, _, shaped := aggregateShape(&spec, &winJob)
	if !shaped {
		t.Fatal("windows job is not a shaped aggregate")
	}
	if fails, known := aggregateFails(steps, map[string]string{"windows-shard": needsResultCancelled}); !known || !fails {
		t.Fatalf("cancelled: fails = %t, known = %t; want true, true", fails, known)
	}
	if fails, known := aggregateFails(steps, map[string]string{"windows-shard": needsResultSuccess}); !known || fails {
		t.Fatalf("success: fails = %t, known = %t; want false, true", fails, known)
	}
	if fails, known := aggregateFails(steps, map[string]string{"windows-shard": needsResultSkipped}); !known || !fails {
		t.Fatalf("skipped: fails = %t, known = %t; want true, true", fails, known)
	}
}
