package main

import (
	"context"
	"os"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/needs"
)

// needsSelection is what a needs subcommand resolves from the operator settings: the
// settings themselves, the resolved framework targets and the analyzer registry scoring
// against them, with each target's contract loaded (needs.RegistryFromPolicy).
type needsSelection struct {
	settings config.OperatorSettings
	targets  needs.Targets
	registry *needs.AnalyzerRegistry
}

// loadNeedsSelection loads the operator settings the flags select and prepares the needs
// engine for them (needs.SelectRegistry, shared with the MCP standards_needs_report).
// Praetor ships no framework: a language without a configured target is classified and
// reported as not configured (ADR-0014 §4).
func loadNeedsSelection(ctx context.Context, flags *operatorSettingsFlags) (*needsSelection, error) {
	policy, registry, err := needs.SelectRegistry(ctx, flags.request())
	if err != nil {
		return nil, err
	}
	return &needsSelection{settings: policy.OperatorSettings(), targets: registry.Targets(), registry: registry}, nil
}

// frameworkSource resolves the go framework of a subcommand's --framework flag
// (needs.SelectFrameworkSource): the flag when it was given, else $PRAETOR_FRAMEWORK_DIR,
// else framework.targets.go.checkout, else the declared contract, else the go target's
// module; with none of them the framework is not configured.
func (s *needsSelection) frameworkSource(value string, set bool) needs.FrameworkSource {
	return needs.SelectFrameworkSource(needs.FrameworkSelection{
		Explicit: value, ExplicitSet: set, Getenv: os.Getenv, Targets: s.targets,
	})
}
