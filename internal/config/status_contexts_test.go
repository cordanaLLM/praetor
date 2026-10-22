// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestStatusContextsPositiveAndBoundary(t *testing.T) {
	counts := []int{0, 1, MaxDeclaredStatusContexts}
	for index := 0; index < len(counts) && index < 3; index++ {
		count := counts[index]
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			contexts := make([]string, count)
			for i := 0; i < count && i < 256; i++ {
				contexts[i] = fmt.Sprintf("%03d", i) + strings.Repeat("界", 252)
			}
			data, err := yaml.Marshal(map[string]any{"status_contexts": contexts})
			if err != nil {
				t.Fatal(err)
			}
			var policy BranchProtectionPolicy
			if err := yaml.Unmarshal(data, &policy); err != nil {
				t.Fatalf("valid declarations rejected: %v", err)
			}
			if !slices.Equal(policy.StatusContexts, contexts) {
				t.Fatalf("declarations lost: got %v, want %v", policy.StatusContexts, contexts)
			}
		})
	}
}

func TestStatusContextsNegativeNamedReasons(t *testing.T) {
	cases := []struct{ name, value, reason string }{
		{"empty", `[""]`, "empty"},
		{"blank", `["   "]`, "empty"},
		{"duplicate", `[gate, gate]`, "duplicate"},
		{"long", `["` + strings.Repeat("x", 256) + `"]`, "255 characters"},
		{"control", `["bad\ncontext"]`, "control"},
		{"unicode control", `["bad\u0085context"]`, "control"},
		{"overflow", "[" + strings.Repeat("gate,", 256) + "last]", "256 entries"},
		{"scalar", "gate", "list"},
		{"null", "null", "list"},
		{"number", "[42]", "string"},
		{"boolean", "[true]", "string"},
		{"null entry", "[null]", "string"},
		{"mapping", "[{gate: value}]", "string"},
	}
	for i := 0; i < len(cases) && i < 13; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			var policy BranchProtectionPolicy
			err := yaml.Unmarshal([]byte("status_contexts: "+tc.value+"\n"), &policy)
			if err == nil || !strings.Contains(err.Error(), "status_contexts") || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("want status_contexts reason %q, got %v", tc.reason, err)
			}
		})
	}
}

func TestStatusContextsRepositoryOnly(t *testing.T) {
	values := []string{"[]", "[gate]"}
	for i := 0; i < len(values) && i < 2; i++ {
		value := values[i]
		_, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{
			Root: policyFixture(t, "branch_protection:\n  status_contexts: "+value+"\n", "", ""),
		})
		if err == nil || !strings.Contains(err.Error(), "status_contexts is a repository-only setting") {
			t.Fatalf("catalog declaration accepted or wrong reason: %v", err)
		}
	}
}

func TestStatusContextsPolicyCopiesAndEquality(t *testing.T) {
	input := []string{"evaluator/status"}
	overrides := Overrides{BranchProtection: &BranchProtectionPolicy{StatusContexts: input}}
	policy := DefaultPolicy()
	policy.ApplyOverrides(overrides)
	input[0] = "changed"
	if !slices.Equal(policy.BranchProtection.StatusContexts, []string{"evaluator/status"}) {
		t.Fatal("policy aliases override declarations")
	}
	clone := clonePolicy(policy)
	clone.BranchProtection.StatusContexts[0] = "clone"
	if policy.BranchProtection.Equal(clone.BranchProtection) {
		t.Fatal("equality or cloning ignores declarations")
	}
	copy := overrides.DeclaredStatusContexts()
	copy[0] = "copy"
	if input[0] != "changed" || (Overrides{}).DeclaredStatusContexts() != nil {
		t.Fatal("override accessor aliases input or invents declarations")
	}
}
