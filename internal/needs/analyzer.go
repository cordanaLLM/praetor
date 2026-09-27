package needs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// LanguageAnalyzer defines the contract for analyzing a repository's language-specific needs.
// Analyze scores the project against target, the framework configured for Language().
type LanguageAnalyzer interface {
	Language() string
	Detect(repoPath string) bool
	Analyze(ctx context.Context, repoPath string, target Target) (*RepoNeeds, error)
}

// ErrNoAnalyzer is returned by AnalyzePolyglot when no registered analyzer recognises a
// repository. Callers must surface it rather than substituting a default manifest.
var ErrNoAnalyzer = errors.New("needs: no matching language analyzer found for repository")

// AnalyzerRegistry maintains registered language analyzers for polyglot discovery and the
// framework targets they score against.
type AnalyzerRegistry struct {
	mu        sync.RWMutex
	analyzers []LanguageAnalyzer
	targets   Targets
	// contracts holds, per language, the framework index a configured target's capability
	// contract declares (LoadRegistry).
	contracts map[string]*FrameworkIndex
}

var (
	defaultRegistryOnce sync.Once
	defaultRegistry     *AnalyzerRegistry
)

// NewAnalyzerRegistry initializes an empty analyzer registry with no configured targets.
func NewAnalyzerRegistry() *AnalyzerRegistry {
	return &AnalyzerRegistry{
		analyzers: make([]LanguageAnalyzer, 0),
		targets:   Targets{},
	}
}

// NewRegistry returns a registry of every built-in language analyzer scoring against the
// operator's framework targets (TargetsFromPolicy). A language without a target is
// classified only: its demands are gaps and name no framework (ADR-0014 §4).
func NewRegistry(targets Targets) *AnalyzerRegistry {
	registry := NewAnalyzerRegistry()
	registry.targets = targets.clone()
	registry.Register(NewGoAnalyzer())
	registry.Register(NewNodeAnalyzer())
	registry.Register(NewPythonAnalyzer())
	registry.Register(NewRustAnalyzer())
	registry.Register(NewNativeAnalyzer())
	return registry
}

// LoadRegistry is NewRegistry with every configured target's capability contract loaded as
// a declared framework index: a scan maps each language's demands to the packages its
// contract declares, and a report does so for every language but go, whose framework the
// report selects itself (SelectFrameworkSource). A contract must name its target's module
// when one is configured and declare its language's ecosystem.
func LoadRegistry(ctx context.Context, targets Targets) (*AnalyzerRegistry, error) {
	registry := NewRegistry(targets)
	registry.contracts = make(map[string]*FrameworkIndex)
	for _, language := range targets.Languages() {
		target := targets[language]
		if target.Contract == "" {
			continue
		}
		index, err := InspectFramework(ctx, FrameworkSource{Contract: target.Contract, Module: target.Module})
		if err != nil {
			return nil, fmt.Errorf("framework.targets.%s.contract: %w", language, err)
		}
		if want := languageEcosystem(language); index.Ecosystem != want {
			return nil, fmt.Errorf("framework.targets.%s.contract declares ecosystem %s; a %s target's contract declares %s",
				language, index.Ecosystem, language, want)
		}
		registry.contracts[language] = index
	}
	return registry, nil
}

// frameworkFor returns the framework a demand of language is reconciled against in a
// report: the language's declared contract for every language but go, else fallback.
func (r *AnalyzerRegistry) frameworkFor(language string, fallback *FrameworkIndex) *FrameworkIndex {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if index := r.contracts[language]; index != nil && language != "go" {
		return index
	}
	return fallback
}

// RowFramework returns the framework a row scored against selected is reported against
// (ADR-0014 §4): the framework its own language's demands are reconciled against
// (frameworkFor) when that one is configured, else the first configured framework the
// demands of another of its languages are reconciled against, the way a scan takes the
// first framework a merged result names (mergeRepoNeeds), else selected. A host that
// configures only a non-go target thus reports that target's rows against its contract, not
// as not configured. A nil registry selects DefaultRegistry.
func RowFramework(registry *AnalyzerRegistry, row *RepoNeeds, selected *FrameworkIndex) *FrameworkIndex {
	if row == nil {
		return selected
	}
	registry = registryOrDefault(registry)
	languages := append([]string{row.Language}, row.Languages...)
	for i := range row.Dependencies {
		languages = append(languages, row.Dependencies[i].Language)
	}
	for _, language := range languages {
		if index := registry.frameworkFor(language, selected); index != nil && index.Basis != FrameworkNotConfigured {
			return index
		}
	}
	return selected
}

// reconcileDeclared maps the demands of one analysed project onto the packages its
// language's contract declares, when one is loaded.
func (r *AnalyzerRegistry) reconcileDeclared(language string, repoNeeds *RepoNeeds) {
	r.mu.RLock()
	index := r.contracts[language]
	r.mu.RUnlock()
	if index == nil {
		return
	}
	for i := range repoNeeds.Dependencies {
		reconcileDependency(index, &repoNeeds.Dependencies[i])
	}
	if language == "go" {
		reconcileStandardImports(index, repoNeeds)
	}
	calculateReadiness(repoNeeds)
}

// Targets returns a copy of the targets the registry scores against.
func (r *AnalyzerRegistry) Targets() Targets {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.targets.clone()
}

// registryOrDefault returns r, or DefaultRegistry when r is nil.
func registryOrDefault(r *AnalyzerRegistry) *AnalyzerRegistry {
	if r == nil {
		return DefaultRegistry()
	}
	return r
}

// Register adds a language analyzer to the registry.
func (r *AnalyzerRegistry) Register(a LanguageAnalyzer) {
	if a == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.analyzers = append(r.analyzers, a)
}

// DetectAll returns all analyzers matching the given repository.
func (r *AnalyzerRegistry) DetectAll(repoPath string) []LanguageAnalyzer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	matched := make([]LanguageAnalyzer, 0)
	for _, a := range r.analyzers {
		if a.Detect(repoPath) {
			matched = append(matched, a)
		}
	}
	return matched
}

// DefaultRegistry returns the singleton registry with all built-in language analyzers and
// no configured targets. Detection-only callers (bump, dogfood, editor) use it; a needs run
// builds its registry from the operator's targets with RegistryFromPolicy.
func DefaultRegistry() *AnalyzerRegistry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewRegistry(nil)
	})
	return defaultRegistry
}

// AnalyzePolyglot executes all matching analyzers, each against its language's target, and
// combines their dependency demands.
func (r *AnalyzerRegistry) AnalyzePolyglot(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	matched := r.DetectAll(repoPath)
	if len(matched) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoAnalyzer, repoPath)
	}
	targets := r.Targets()

	primaryNeeds, err := matched[0].Analyze(ctx, repoPath, targets[matched[0].Language()])
	if err != nil {
		return nil, fmt.Errorf("primary analyzer %s failed: %w", matched[0].Language(), err)
	}
	r.reconcileDeclared(matched[0].Language(), primaryNeeds)

	for i := 1; i < len(matched); i++ {
		secNeeds, sErr := matched[i].Analyze(ctx, repoPath, targets[matched[i].Language()])
		if sErr != nil {
			return nil, fmt.Errorf("secondary analyzer %s failed: %w", matched[i].Language(), sErr)
		}
		r.reconcileDeclared(matched[i].Language(), secNeeds)
		mergeRepoNeeds(primaryNeeds, secNeeds)
	}

	return primaryNeeds, nil
}

// mergeRepoNeeds merges dependencies and capabilities from secondary analyzer results and
// from the sub-projects of a repository. A dependency whose demandIdentity dst already
// lists is not appended again: two sub-projects demanding one package are one demand of
// the repository. A row whose own language has no framework takes the first framework a
// merged result names.
func mergeRepoNeeds(dst, src *RepoNeeds) {
	if src == nil {
		return
	}
	if dst.Framework == "" {
		dst.Framework = src.Framework
	}
	dst.Languages = appendUniqueStr(dst.Languages, src.Language)
	for _, lang := range src.Languages {
		dst.Languages = appendUniqueStr(dst.Languages, lang)
	}
	for _, bk := range src.BuilderKits {
		dst.BuilderKits = appendUniqueStr(dst.BuilderKits, bk)
	}
	dst.Dependencies = appendNewDemands(dst.Dependencies, src.Dependencies)
	dst.StandardLibraryImports = appendNewDemands(dst.StandardLibraryImports, src.StandardLibraryImports)
	for _, capKey := range src.Capabilities.Required {
		dst.Capabilities.Required = appendUniqueCap(dst.Capabilities.Required, capKey)
	}
	for _, deprecation := range src.Deprecations {
		dst.Deprecations = appendUniqueStr(dst.Deprecations, deprecation)
	}
	calculateReadiness(dst)
}

// appendNewDemands appends every demand in src whose demandIdentity neither dst nor an
// earlier demand in src carries.
func appendNewDemands(dst, src []DependencyDemand) []DependencyDemand {
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, dep := range dst {
		seen[demandIdentity(dep)] = struct{}{}
	}
	for _, dep := range src {
		key := demandIdentity(dep)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, dep)
	}
	return dst
}

// demandIdentity keys a dependency demand by ecosystem and normalised package name. Go
// module paths are case-sensitive and compare exactly. PyPI names are normalised per
// PEP 503, so typing-extensions, typing_extensions and Typing.Extensions are one package.
// Other names compare case-insensitively: CMake spells find_package(CUDA) where Meson
// spells dependency('cuda').
func demandIdentity(dep DependencyDemand) string {
	name := dep.Package
	switch dep.Ecosystem {
	case "go":
	case "pypi":
		name = normalizePyPIName(name)
	default:
		name = strings.ToLower(name)
	}
	return dep.Ecosystem + "\x00" + name
}

// normalizePyPIName applies PEP 503 name normalisation: every run of "-", "_" and "."
// becomes a single "-", and the result is lower-cased.
func normalizePyPIName(name string) string {
	var sb strings.Builder
	sb.Grow(len(name))
	inRun := false
	for _, r := range strings.ToLower(name) {
		if r == '-' || r == '_' || r == '.' {
			inRun = true
			continue
		}
		if inRun {
			sb.WriteByte('-')
			inRun = false
		}
		sb.WriteRune(r)
	}
	if inRun {
		sb.WriteByte('-')
	}
	return sb.String()
}

func appendUniqueStr(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

func appendUniqueCap(slice []CapabilityKey, val CapabilityKey) []CapabilityKey {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}
