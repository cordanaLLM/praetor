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
// engine for them. No framework target configured keeps the built-in targets until they
// are removed (ADR-0014 §6).
func loadNeedsSelection(ctx context.Context, flags *operatorSettingsFlags) (*needsSelection, error) {
	policy, err := flags.loadPolicy(ctx)
	if err != nil {
		return nil, err
	}
	registry, err := needs.RegistryFromPolicy(ctx, policy)
	if err != nil {
		return nil, err
	}
	return &needsSelection{settings: policy.OperatorSettings(), targets: registry.Targets(), registry: registry}, nil
}

// frameworkSource resolves the go framework of a subcommand's --framework flag
// (needs.SelectFrameworkSource): the flag when it was given, else $PRAETOR_FRAMEWORK_DIR,
// else framework.targets.go.checkout, else the declared contract or catalog.
func (s *needsSelection) frameworkSource(value string, set bool) needs.FrameworkSource {
	return needs.SelectFrameworkSource(needs.FrameworkSelection{
		Explicit: value, ExplicitSet: set, Getenv: os.Getenv, Targets: s.targets,
	})
}
