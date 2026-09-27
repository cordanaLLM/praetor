package needs

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// FrameworkDirEnv names a local checkout of the go target framework. It sits between an
// explicit selection and framework.targets.go.checkout in SelectFrameworkSource.
const FrameworkDirEnv = "PRAETOR_FRAMEWORK_DIR"

// Target is one language's framework, as the operator configures it under
// framework.targets.<language> (config.FrameworkTarget, ADR-0014).
type Target struct {
	// Module identifies the framework; its contract must declare the same module.
	Module string
	// BuilderKits are the framework's repositories; the first routes demand requests.
	BuilderKits []string
	// Contract is a capability contract (version 1) declaring the framework's packages.
	Contract string
	// Checkout is a local checkout of the framework, observed from source (go only).
	Checkout string
}

// Targets maps a framework language (config.FrameworkLanguages) to its target. A language
// without an entry has no framework: its demands are classified, never mapped, and its
// demand requests are unrouted (ADR-0014 §4).
type Targets map[string]Target

// clone returns a copy of t whose builder kit lists are not shared with t.
func (t Targets) clone() Targets {
	out := make(Targets, len(t))
	for language, target := range t {
		target.BuilderKits = slices.Clone(target.BuilderKits)
		out[language] = target
	}
	return out
}

// For returns the target of language; a language without one has the zero Target, which
// names no framework and routes nothing.
func (t Targets) For(language string) Target {
	target := t[language]
	target.BuilderKits = slices.Clone(target.BuilderKits)
	return target
}

// Languages returns the configured languages in sorted order.
func (t Targets) Languages() []string {
	return slices.Sorted(maps.Keys(t))
}

// TargetsFromPolicy converts the operator's framework.targets into needs targets, resolving
// each relative contract path against the layer file that set it. A nil policy (no settings
// document selected) has no targets.
func TargetsFromPolicy(policy *config.EffectivePolicy) (Targets, error) {
	configured := policy.OperatorSettings().Framework.Targets
	targets := make(Targets, len(configured))
	for _, language := range slices.Sorted(maps.Keys(configured)) {
		target := configured[language]
		contract, err := policy.ResolveOperatorPath("framework.targets."+language+".contract", target.Contract)
		if err != nil {
			return nil, fmt.Errorf("framework.targets.%s.contract: %w", language, err)
		}
		targets[language] = Target{Module: target.Module, BuilderKits: slices.Clone(target.BuilderKits),
			Contract: contract, Checkout: target.Checkout}
	}
	return targets, nil
}

// RegistryFromPolicy prepares the needs engine for the operator settings policy selects:
// its framework targets (TargetsFromPolicy) with each target's contract loaded
// (LoadRegistry). A nil policy has no targets, so every language is unconfigured. The CLI
// and the MCP server both build their registry here.
func RegistryFromPolicy(ctx context.Context, policy *config.EffectivePolicy) (*AnalyzerRegistry, error) {
	targets, err := TargetsFromPolicy(policy)
	if err != nil {
		return nil, fmt.Errorf("resolve framework targets: %w", err)
	}
	registry, err := LoadRegistry(ctx, targets)
	if err != nil {
		return nil, fmt.Errorf("load framework targets: %w", err)
	}
	return registry, nil
}

// languageEcosystem is the package ecosystem of a framework language's dependencies.
func languageEcosystem(language string) string {
	switch language {
	case "typescript":
		return "npm"
	case "python":
		return "pypi"
	case "rust":
		return "cargo"
	case "native":
		return "system"
	default:
		return contractEcosystemGo
	}
}

// RoutingKit is the builder kit demand requests for this target go to, or "" when the target
// lists none.
func (t Target) RoutingKit() string {
	if len(t.BuilderKits) == 0 {
		return ""
	}
	return t.BuilderKits[0]
}

// undeclaredNote explains a demand the framework named by module declares nothing for.
func undeclaredNote(module, what string, capability CapabilityKey) string {
	if module == "" {
		return fmt.Sprintf("No target framework is configured for capability %s.", capability)
	}
	return fmt.Sprintf("%s declares no %s for capability %s.", module, what, capability)
}

// applyTo records the target on an analysed repository: the framework it is scored against
// and the builder kits its demands route to. Analyzers and the harvester call it before
// scoring readiness.
func (t Target) applyTo(repoNeeds *RepoNeeds) {
	repoNeeds.Framework = t.Module
	repoNeeds.BuilderKits = slices.Clone(t.BuilderKits)
}

// FrameworkSource selects the framework index a report, migration or epic scores against.
type FrameworkSource struct {
	// Checkout is a local checkout observed from source; it wins over Contract. A value that
	// is module-path shaped and not a directory names the framework without evidence.
	Checkout string
	// Contract is a capability contract declaring the framework's packages. With a checkout
	// that publishes no capabilities.yaml of its own, its packages are the ones observed.
	Contract string
	// Module is the go target's module: a contract must declare it, and it names the
	// framework when neither source is selected.
	Module string
}

// FrameworkSelection is what a caller knows about the framework the operator selected.
type FrameworkSelection struct {
	// Explicit is the --framework flag or tool argument; ExplicitSet reports it was given,
	// so an explicit "" selects the declaration instead of the checkout fallbacks.
	Explicit    string
	ExplicitSet bool
	// Getenv reads FrameworkDirEnv; nil means no environment.
	Getenv func(string) string
	// Targets are the configured framework targets.
	Targets Targets
}

// SelectFrameworkSource resolves the go framework a needs run scores against (ADR-0014 §3):
// the explicit selection, then $PRAETOR_FRAMEWORK_DIR, then framework.targets.go.checkout;
// without a checkout, framework.targets.go.contract declares the framework, and without
// either the go target's module names it.
func SelectFrameworkSource(selection FrameworkSelection) FrameworkSource {
	target := selection.Targets.For("go")
	source := FrameworkSource{Contract: target.Contract, Module: target.Module}
	switch {
	case selection.ExplicitSet:
		source.Checkout = strings.TrimSpace(selection.Explicit)
	case selection.Getenv != nil && selection.Getenv(FrameworkDirEnv) != "":
		source.Checkout = selection.Getenv(FrameworkDirEnv)
	default:
		source.Checkout = target.Checkout
	}
	return source
}
