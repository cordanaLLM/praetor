package agenthook

import (
	"reflect"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestBuildPolicy(t *testing.T) {
	policy, err := BuildPolicy(config.HookSettings{})
	if err != nil || policy.Command("git status").Outcome != Allow {
		t.Fatalf("zero settings: %v %+v", err, policy)
	}
	withDeny, err := BuildPolicy(config.HookSettings{CommandPolicy: config.CommandPolicySettings{Deny: []string{`\bshutdown\b`}}})
	if err != nil || withDeny.Command("shutdown -h now").Outcome != Deny {
		t.Fatalf("deny list not wired: %v %+v", err, withDeny)
	}
	if _, err := BuildPolicy(config.HookSettings{CommandPolicy: config.CommandPolicySettings{Deny: []string{"("}}}); err == nil {
		t.Error("an unparsable deny pattern compiled")
	}
}

func TestAppliesEverywhere(t *testing.T) {
	if AppliesEverywhere(config.HookSettings{}) {
		t.Error("the zero value applies everywhere")
	}
	if AppliesEverywhere(config.HookSettings{Scope: config.HookScopeGoverned}) {
		t.Error("the governed scope applies everywhere")
	}
	if !AppliesEverywhere(config.HookSettings{Scope: config.HookScopeAll}) {
		t.Error("hooks.scope: all did not apply everywhere")
	}
}

func TestInterpreterCandidates(t *testing.T) {
	if got := InterpreterCandidates(config.HookSettings{}); !reflect.DeepEqual(got, config.DefaultOperatorSettings().Hooks.Python) {
		t.Errorf("zero settings did not fall back to the built-in candidates: %v", got)
	}
	configured := [][]string{{"/opt/python/python3", "-X", "utf8"}}
	if got := InterpreterCandidates(config.HookSettings{Python: configured}); !reflect.DeepEqual(got, configured) {
		t.Errorf("configured candidates lost: %v", got)
	}
	if got := InterpreterCandidates(config.HookSettings{Python: [][]string{}}); !reflect.DeepEqual(got, config.DefaultOperatorSettings().Hooks.Python) {
		t.Errorf("an explicitly empty list did not fall back to the built-in candidates: %v", got)
	}
}
