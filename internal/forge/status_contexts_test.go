// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"go.yaml.in/yaml/v3"
)

func TestDeclaredStatusContextsRuleset(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", prWorkflow("CI"))
	writeWorkflowFixture(t, root, "evaluator.yml", "on: workflow_run\njobs:\n  undeclared: {}\n")
	policy := config.DefaultPolicy().BranchProtection
	if err := yaml.Unmarshal([]byte("status_contexts: [evaluator/status, CI]\n"), &policy); err != nil {
		t.Fatal(err)
	}
	data, contexts, err := RenderRulesetForRepository(t.Context(), root, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"CI", "evaluator/status"}
	if !slices.Equal(contexts, want) {
		t.Fatalf("derived contexts = %v, want %v", contexts, want)
	}
	stored, err := RulesetStatusContexts(data)
	if err != nil || !slices.Equal(stored, want) {
		t.Fatalf("ruleset contexts = %v, error %v", stored, err)
	}
}

func TestDeclaredStatusContextsVariants(t *testing.T) {
	root, commit := committedWorkflowRepo(t, map[string]string{
		".github/workflows/ci.yml":        prWorkflow("CI"),
		".github/workflows/evaluator.yml": "on: workflow_run\njobs:\n  undeclared: {}\n",
	})
	option := WithDeclaredStatusContexts([]string{"evaluator/status", "CI"})
	cases := []struct {
		name     string
		discover func(context.Context) ([]string, error)
	}{
		{"default", func(ctx context.Context) ([]string, error) { return RequiredStatusContexts(ctx, root, option) }},
		{"planned", func(ctx context.Context) ([]string, error) {
			return RequiredStatusContextsPlanned(ctx, root, map[string][]byte{".github/workflows/ci.yml": []byte(prWorkflow("CI"))}, option)
		}},
		{"of", func(ctx context.Context) ([]string, error) {
			return RequiredStatusContextsOf(ctx, root, []string{".github/workflows/ci.yml"}, option)
		}},
		{"in", func(ctx context.Context) ([]string, error) {
			return RequiredStatusContextsIn(ctx, root, "acme/widgets", option)
		}},
		{"at", func(ctx context.Context) ([]string, error) {
			return RequiredStatusContextsAt(ctx, root, commit, "acme/widgets", option)
		}},
	}
	for i := 0; i < len(cases) && i < 5; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			contexts, err := tc.discover(t.Context())
			if err != nil || !slices.Equal(contexts, []string{"CI", "evaluator/status"}) {
				t.Fatalf("contexts = %v, error %v", contexts, err)
			}
		})
	}
}

func TestDeclaredStatusContextsQueueAndInvalid(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", prWorkflow("CI"))
	contexts, err := RequiredStatusContexts(t.Context(), root, ForMergeQueue(true), WithDeclaredStatusContexts([]string{"evaluator/status"}))
	if err != nil || !slices.Equal(contexts, []string{"evaluator/status"}) {
		t.Fatalf("declared context lost to workflow filtering: %v, %v", contexts, err)
	}
	invalid := [][]string{{""}, {"gate", "gate"}, {strings.Repeat("x", 256)}}
	for i := 0; i < len(invalid) && i < 3; i++ {
		contexts, err := RequiredStatusContexts(t.Context(), root, WithDeclaredStatusContexts(invalid[i]))
		if err == nil || contexts != nil {
			t.Fatalf("invalid declarations accepted: %v, %v", contexts, err)
		}
	}
}

func TestDeclaredStatusContextsBaseline(t *testing.T) {
	for i := 0; i < 2; i++ {
		root := t.TempDir()
		queue := []string{"false", "true"}[i]
		writeManifestFixture(t, root, "version: 1\noverrides:\n  branch_protection:\n    merge_queue: "+queue+"\n    status_contexts: [evaluator/status]\n")
		baseline, err := ReadRulesetBaseline(t.Context(), root)
		if err != nil || !slices.Equal(baseline.Contexts, []string{"evaluator/status"}) {
			t.Fatalf("baseline contexts = %v, error %v", baseline.Contexts, err)
		}
		other := baseline.QueuedContexts
		if i == 1 {
			other = baseline.UnqueuedContexts
		}
		if !slices.Equal(other, []string{"evaluator/status"}) {
			t.Fatalf("alternate queue contexts = %v", other)
		}
	}
}
