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
	// Module identifies the framework; built-in catalog paths inside it are its packages.
	Module string
	// BuilderKits are the framework's repositories; the first routes demand requests.
	BuilderKits []string
	// Contract is a capability contract (version 1) declaring the framework's packages.
	Contract string
	// Checkout is a local checkout of the framework, observed from source (go only).
	Checkout string
}

// Targets maps a framework language (config.FrameworkLanguages) to its target. An empty
// map is resolved to legacyTargets until the built-in framework data is removed.
type Targets map[string]Target

// TRANSITION (ADR-0014 §6): defaultFrameworkModule and legacyTargets are the framework
// targets praetor shipped before targets became operator configuration. They apply only
// while no framework.targets entry is configured, so an unconfigured host keeps its
// reports unchanged; the built-in catalog paths in catalog.go and KnownDomainCapabilities
// describe these targets' packages. They are removed together once the operator exports
// them with `needs contract export` and configures the result.
const defaultFrameworkModule = "github.com/golusoris/golusoris"

// catalogDescribes reports whether the built-in catalog's claims that name no package, the
// retained foundations, describe the framework module: they are the legacy go target's.
func catalogDescribes(module string) bool {
	return module == defaultFrameworkModule
}

// legacyTargets returns a fresh copy of the built-in targets; see defaultFrameworkModule.
func legacyTargets() Targets {
	return Targets{
		"go":         {Module: defaultFrameworkModule, BuilderKits: []string{"golusoris/golusoris", "golusoris/goenvoy"}},
		"typescript": {Module: "github.com/golusoris/sveltesentio", BuilderKits: []string{"golusoris/sveltesentio"}},
		"python":     {Module: "github.com/golusoris/pykit", BuilderKits: []string{"golusoris/pykit"}},
		"rust":       {Module: "github.com/golusoris/rustkit", BuilderKits: []string{"golusoris/rustkit"}},
		"native":     {Module: "github.com/golusoris/template-native-gpu", BuilderKits: []string{"golusoris/template-native-gpu"}},
	}
}

// resolved returns a copy of t, or legacyTargets when t is empty.
func (t Targets) resolved() Targets {
	if len(t) == 0 {
		return legacyTargets()
	}
	out := make(Targets, len(t))
	for language, target := range t {
		target.BuilderKits = slices.Clone(target.BuilderKits)
		out[language] = target
	}
	return out
}

// For returns the resolved target of language; a language without one has the zero Target,
// which names no framework and routes nothing.
func (t Targets) For(language string) Target {
	return t.resolved()[language]
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
// (LoadRegistry). A nil policy has no targets and keeps the built-in ones (legacyTargets).
// The CLI and the MCP server both build their registry here.
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

// contains reports whether path is the target's module or a package inside it.
func (t Target) contains(path string) bool {
	return t.Module != "" && matchesModuleBoundary(path, t.Module)
}

// applyTo records the target on an analysed repository and scopes its demands to it
// (scopeDemand). Analyzers and the harvester call it before scoring readiness.
func (t Target) applyTo(repoNeeds *RepoNeeds) {
	repoNeeds.Framework = t.Module
	repoNeeds.BuilderKits = slices.Clone(t.BuilderKits)
	for i := range repoNeeds.Dependencies {
		t.scopeDemand(&repoNeeds.Dependencies[i])
	}
	for i := range repoNeeds.StandardLibraryImports {
		t.scopeDemand(&repoNeeds.StandardLibraryImports[i])
	}
}

// scopeDemand drops a built-in catalog path the target's module does not contain. The
// catalog names the legacy targets' packages (TRANSITION); another framework's package is
// never a candidate for this one, so such a demand keeps its capability and becomes a gap.
func (t Target) scopeDemand(dep *DependencyDemand) {
	if dep.FrameworkReplacement != "" && !t.contains(dep.FrameworkReplacement) {
		dep.FrameworkReplacement = ""
		if dep.Status == StatusCovered || dep.Status == StatusAdapterAvailable {
			dep.Status = StatusGap
		}
		dep.Notes = undeclaredNote(t.Module, "catalog replacement", dep.Capability)
	}
	if dep.Relationship != nil {
		t.scopeRelationship(dep)
	}
}

// scopeRelationship drops a built-in relationship claim of another framework: a foundation
// of a framework the catalog does not describe, or a related package outside the target.
func (t Target) scopeRelationship(dep *DependencyDemand) {
	relationship := dep.Relationship
	if relationship.Kind == RelationshipFoundation {
		if !catalogDescribes(t.Module) {
			dropFoundation(dep, t.Module)
		}
		return
	}
	if relationship.FrameworkPackage == "" || t.contains(relationship.FrameworkPackage) {
		return
	}
	dep.Relationship = cloneRelationship(relationship)
	dep.Relationship.FrameworkPackage = ""
	dep.Status = StatusGap
	dep.Notes = undeclaredNote(t.Module, "related package", dep.Capability)
}

// dropFoundation removes a built-in foundation claim from a demand of a framework the
// catalog does not describe: nothing declares the library retained, so it is a gap.
func dropFoundation(dep *DependencyDemand, module string) {
	dep.Relationship = nil
	dep.Status, dep.FrameworkReplacement = StatusGap, ""
	dep.Notes = undeclaredNote(module, "retained foundation", dep.Capability)
}

// FrameworkSource selects the framework index a report, migration or epic scores against.
type FrameworkSource struct {
	// Checkout is a local checkout observed from source; it wins over Contract. A value that
	// is module-path shaped and not a directory names the framework without evidence.
	Checkout string
	// Contract is a capability contract declaring the framework's packages.
	Contract string
	// Module is the go target's module: built-in catalog paths inside it resolve against
	// the selected framework, and it names the framework when neither source is selected.
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
