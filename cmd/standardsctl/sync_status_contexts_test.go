// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestSyncDeclaredStatusContexts(t *testing.T) {
	f := newSyncValidationFixture(t)
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+
		"overrides:\n  branch_protection:\n    status_contexts: [evaluator/status]\n")
	writeFixtureFile(t, f.dir, ".github/workflows/evaluator.yml", "on: workflow_run\njobs:\n  undeclared: {}\n")
	if err := os.Remove(filepath.Join(f.dir, forge.RepositoryRulesetPath)); err != nil {
		t.Fatal(err)
	}
	out, err := runSyncCmd(t, "--config="+f.manifestPath)
	if err != nil {
		t.Fatalf("sync fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, forge.RepositoryRulesetPath))
	if err != nil {
		t.Fatal(err)
	}
	contexts, err := forge.RulesetStatusContexts(data)
	if err != nil || !slices.Contains(contexts, "evaluator/status") || slices.Contains(contexts, "undeclared") {
		t.Fatalf("sync contexts = %v, error %v", contexts, err)
	}
	t.Logf("sync fixture rc=0; declared context read back: %v", contexts)
	if out, err := runSyncCmd(t, "--config="+f.manifestPath); err != nil {
		t.Fatalf("sync readback: %v\n%s", err, out)
	}
}

func TestDeclaredStatusContextsLiveAndDocumentation(t *testing.T) {
	f := newSyncValidationFixture(t)
	manifest, err := config.LoadManifest(f.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	policy := config.DefaultPolicy().BranchProtection
	policy.StatusContexts = []string{"evaluator/status"}
	target, err := liveProtectionTarget(t.Context(), f.dir, manifest, policy)
	if err != nil || !slices.Equal(target.contexts, policy.StatusContexts) {
		t.Fatalf("live comparison contexts = %v, error %v", target.contexts, err)
	}
	option := forge.WithDeclaredStatusContexts(policy.StatusContexts)
	contexts, err := hostedDocumentationContexts(t.Context(), f.dir, nil, false, false, option)
	if err != nil || !slices.Equal(contexts, policy.StatusContexts) {
		t.Fatalf("documentation ruleset contexts = %v, error %v", contexts, err)
	}
	contexts, err = hostedDocumentationContexts(t.Context(), f.dir, nil, true, false, option)
	if err != nil || len(contexts) != 0 {
		t.Fatalf("declarations substituted for workflow evidence: %v, %v", contexts, err)
	}
}
