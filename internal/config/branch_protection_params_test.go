package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBranchProtection_AllowedMergeMethods_Positive(t *testing.T) {
	var overrides Overrides
	yamlDoc := "branch_protection:\n  allowed_merge_methods:\n    - merge\n    - squash\n    - rebase\n  require_extra_approval_for_unattributed_changes: true\n"
	if err := yaml.Unmarshal([]byte(yamlDoc), &overrides); err != nil {
		t.Fatalf("valid branch protection parameters rejected: %v", err)
	}
	bp := overrides.BranchProtection
	if bp == nil {
		t.Fatal("expected non-nil BranchProtection")
	}
	wantMethods := []string{"merge", "squash", "rebase"}
	if !slices.Equal(bp.AllowedMergeMethods, wantMethods) {
		t.Fatalf("got %v, want %v", bp.AllowedMergeMethods, wantMethods)
	}
	if bp.RequireExtraApprovalForUnattributedChanges == nil || !*bp.RequireExtraApprovalForUnattributedChanges {
		t.Fatal("expected require_extra_approval_for_unattributed_changes to be true")
	}

	policy := DefaultPolicy()
	policy.ApplyOverrides(overrides)
	if !slices.Equal(policy.BranchProtection.AllowedMergeMethods, wantMethods) {
		t.Fatalf("policy allowed merge methods = %v", policy.BranchProtection.AllowedMergeMethods)
	}
	if policy.BranchProtection.RequireExtraApprovalForUnattributedChanges == nil || !*policy.BranchProtection.RequireExtraApprovalForUnattributedChanges {
		t.Fatal("policy require_extra_approval_for_unattributed_changes not applied")
	}
}

func TestBranchProtection_AllowedMergeMethods_Boundary_EmptyList(t *testing.T) {
	var overrides Overrides
	yamlDoc := "branch_protection:\n  allowed_merge_methods: []\n"
	err := yaml.Unmarshal([]byte(yamlDoc), &overrides)
	if err == nil || !strings.Contains(err.Error(), "allowed_merge_methods cannot be empty") {
		t.Fatalf("empty allowed_merge_methods accepted: %v", err)
	}
}

func TestBranchProtection_AllowedMergeMethods_Boundary_UnknownMethod(t *testing.T) {
	var overrides Overrides
	yamlDoc := "branch_protection:\n  allowed_merge_methods:\n    - fast-forward\n"
	err := yaml.Unmarshal([]byte(yamlDoc), &overrides)
	if err == nil || !strings.Contains(err.Error(), "unknown allowed_merge_method") {
		t.Fatalf("unknown merge method accepted: %v", err)
	}
}

func TestBranchProtection_AllowedMergeMethods_Boundary_Duplicates(t *testing.T) {
	var overrides Overrides
	yamlDoc := "branch_protection:\n  allowed_merge_methods:\n    - squash\n    - squash\n"
	err := yaml.Unmarshal([]byte(yamlDoc), &overrides)
	if err == nil || !strings.Contains(err.Error(), "duplicate allowed_merge_method") {
		t.Fatalf("duplicate merge method accepted: %v", err)
	}
}

func TestBranchProtection_RequireExtraApproval_DistinguishesUnsetFromFalse(t *testing.T) {
	var unset Overrides
	if err := yaml.Unmarshal([]byte("branch_protection:\n  require_signed_commits: true\n"), &unset); err != nil {
		t.Fatal(err)
	}
	if unset.BranchProtection.RequireExtraApprovalForUnattributedChanges != nil {
		t.Fatal("unset require_extra_approval must be nil")
	}

	var explicitFalse Overrides
	if err := yaml.Unmarshal([]byte("branch_protection:\n  require_extra_approval_for_unattributed_changes: false\n"), &explicitFalse); err != nil {
		t.Fatal(err)
	}
	ptr := explicitFalse.BranchProtection.RequireExtraApprovalForUnattributedChanges
	if ptr == nil || *ptr != false {
		t.Fatal("explicit false must decode to a pointer holding false")
	}
}

func TestBranchProtection_PolicyDigestUnchangedWhenUnset(t *testing.T) {
	legacy := ResolvedPolicy{Complexity: ComplexityPolicy{MaxFuncLOC: 60}, Linters: []string{"govet"}}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "AllowedMergeMethods") || strings.Contains(string(encoded), "allowed_merge_methods") {
		t.Fatalf("unset AllowedMergeMethods leaked into JSON: %s", encoded)
	}
	if strings.Contains(string(encoded), "RequireExtraApprovalForUnattributedChanges") || strings.Contains(string(encoded), "require_extra_approval") {
		t.Fatalf("unset RequireExtraApprovalForUnattributedChanges leaked into JSON: %s", encoded)
	}

	result, err := ResolvePolicy(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.VerifyDigest(); err != nil {
		t.Fatalf("default policy digest failed verification: %v", err)
	}
}
