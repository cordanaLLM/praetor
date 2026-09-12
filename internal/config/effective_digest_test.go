package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func retainedPolicy(t *testing.T) *EffectivePolicy {
	t.Helper()
	policy, err := ResolvePolicy(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var retained EffectivePolicy
	if err := json.Unmarshal(data, &retained); err != nil {
		t.Fatal(err)
	}
	return &retained
}

func TestEffectivePolicyVerifyDigestRoundTripAndRelocation(t *testing.T) {
	policy := retainedPolicy(t)
	policy.Sources[0].Path = "/relocated/diagnostic/path"
	before, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.VerifyDigest(); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(policy)
	if err != nil || string(before) != string(after) {
		t.Fatalf("verification mutated snapshot: %v", err)
	}
}

func TestEffectivePolicyVerifyDigestRejectsChangedMetadata(t *testing.T) {
	for name, change := range map[string]func(*EffectivePolicy){
		"limit":       func(p *EffectivePolicy) { p.Policy.Complexity.MaxFuncLOC-- },
		"source":      func(p *EffectivePolicy) { p.Sources[0].SHA256 = strings.Repeat("a", 64) },
		"digest":      func(p *EffectivePolicy) { p.SHA256 = strings.Repeat("a", 64) },
		"contributor": func(p *EffectivePolicy) { p.Fields["max_func_loc"] = []string{"missing"} },
		"linters":     func(p *EffectivePolicy) { p.Policy.Linters = []string{"different"} },
		"missing":     func(p *EffectivePolicy) { p.Sources = nil },
		"unknown key": func(p *EffectivePolicy) { p.Fields["unknown"] = []string{"builtin:defaults-v1"} },
	} {
		t.Run(name, func(t *testing.T) {
			policy := retainedPolicy(t)
			change(policy)
			if err := policy.VerifyDigest(); err == nil {
				t.Fatal("changed retained metadata was accepted")
			}
		})
	}
	var absent *EffectivePolicy
	if err := absent.VerifyDigest(); err == nil {
		t.Fatal("nil policy was accepted")
	}
}

func TestEffectivePolicyVerifyDigestSourceBoundary(t *testing.T) {
	layers := make([]PolicyLayer, maxPolicyLayers)
	for i := range layers {
		id := fmt.Sprintf("source-%d", i)
		layers[i] = PolicyLayer{Source: PolicySource{ID: id, SHA256: policyDigest([]byte(id))}}
	}
	policy, err := ResolvePolicy(t.Context(), layers)
	if err != nil {
		t.Fatal(err)
	}
	policy.Sources[0].Path = strings.Repeat("p", 4096)
	if err := policy.VerifyDigest(); err != nil {
		t.Fatalf("exact metadata boundary rejected: %v", err)
	}
	policy.Sources[0].Path += "p"
	if err := policy.VerifyDigest(); err == nil {
		t.Fatal("oversize diagnostic path accepted")
	}
	policy.Sources[0].Path = ""
	policy.Sources = append(policy.Sources, policy.Sources[0])
	if err := policy.VerifyDigest(); err == nil {
		t.Fatal("source count overflow accepted")
	}
}
