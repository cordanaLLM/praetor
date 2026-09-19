package agenthook

import "github.com/cordanaLLM/praetor/internal/config"

// This file wires `hooks.*` operator settings (S1, internal/config/operator_sections.go)
// into the evaluator: the command-policy deny list, the checkpoint governance scope and the
// Python interpreter candidates. It holds no filesystem access of its own; a caller resolves
// and loads config.OperatorSettings (config.SelectOperatorSettings, config.LoadOperatorSettings)
// and hands the Hooks section in.

// BuildPolicy compiles the command policy for one hooks section: the built-in rules plus
// hooks.command_policy.deny. A zero HookSettings compiles the built-in rules alone, so a
// caller that has not loaded any settings document still fails closed rather than nil.
func BuildPolicy(hooks config.HookSettings) (*Policy, error) {
	return NewPolicy(hooks.CommandPolicy.Deny)
}

// AppliesEverywhere reports hooks.scope: all, where the command policy still runs in an
// ungoverned workspace (3.2 step 4 of the rollout spec). Any other value, including the zero
// value of an unloaded settings section, is the governed default: an ungoverned or missing
// workspace answers a neutral skip instead.
func AppliesEverywhere(hooks config.HookSettings) bool {
	return hooks.Scope == config.HookScopeAll
}

// interpreterCandidates is the built-in interpreter search order, identical to
// config.DefaultOperatorSettings().Hooks.Python. It is used whenever a caller has not loaded
// an operator settings document, so a hook running without configuration still resolves an
// interpreter on a host that has one.
var interpreterCandidates = config.DefaultOperatorSettings().Hooks.Python

// InterpreterCandidates returns hooks.python in table order, or the built-in candidates
// (python3, python, py -3) when the section carries none. config.ValidateCommandPolicyDeny's
// sibling, the pythonCandidates schema check, has already bounded and shaped every candidate
// by the time it reaches here (internal/config/operator_schema.go); this function trusts that
// and only chooses between "configured" and "built-in".
func InterpreterCandidates(hooks config.HookSettings) [][]string {
	if len(hooks.Python) == 0 {
		return interpreterCandidates
	}
	return hooks.Python
}
