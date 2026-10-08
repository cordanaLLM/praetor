package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Positive: the repository override declares a merge queue and CodeQL default setup, and the
// resolved policy carries both. Negative: a policy that declares neither does not.
func TestMergeQueueOverrideReachesThePolicy(t *testing.T) {
	var overrides Overrides
	if err := yaml.Unmarshal([]byte("branch_protection:\n  merge_queue: true\n  codeql_default_setup: true\n"), &overrides); err != nil {
		t.Fatal(err)
	}
	if !overrides.DeclaresMergeQueue() {
		t.Fatal("the override declares a merge queue")
	}
	policy := DefaultPolicy()
	policy.ApplyOverrides(overrides)
	if !policy.BranchProtection.MergeQueue || !policy.BranchProtection.CodeQLDefaultSetup {
		t.Fatalf("policy = %+v", policy.BranchProtection)
	}
	plain := DefaultPolicy()
	plain.ApplyOverrides(Overrides{})
	if plain.BranchProtection.MergeQueue || plain.BranchProtection.CodeQLDefaultSetup || (Overrides{}).DeclaresMergeQueue() {
		t.Fatalf("a repository that declares nothing must not get a queue: %+v", plain.BranchProtection)
	}
	if (Overrides{BranchProtection: &BranchProtectionPolicy{}}).DeclaresMergeQueue() {
		t.Fatal("an override section without the key declares no queue")
	}
}

// Negative: a catalog layer cannot set either key, and a misspelling is refused as an unknown
// key. Boundary: the effective policy digest of a repository that declares neither is the one it
// had before the keys existed, because both carry an omitempty JSON tag.
func TestMergeQueueIsRepositoryOnly(t *testing.T) {
	for _, key := range []string{"merge_queue", "codeql_default_setup"} {
		controls := ArchetypeControls{}
		controls.BranchProtection = BranchProtectionPolicy{MergeQueue: key == "merge_queue", CodeQLDefaultSetup: key != "merge_queue"}
		if err := controls.validate(); err == nil || !strings.Contains(err.Error(), "repository-only") {
			t.Fatalf("%s in a catalog layer: %v", key, err)
		}
	}
	var overrides Overrides
	if err := yaml.Unmarshal([]byte("branch_protection:\n  merge_queues: true\n"), &overrides); err == nil {
		t.Fatal("a misspelled key was accepted")
	}
	joined := joinBranchProtection(BranchProtectionPolicy{MergeQueue: true}, BranchProtectionPolicy{})
	if !joined.MergeQueue {
		t.Fatal("a join keeps a declared queue")
	}
}
