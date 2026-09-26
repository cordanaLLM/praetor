package needs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// LanguageAnalyzer defines the contract for analyzing a repository's language-specific needs.
type LanguageAnalyzer interface {
	Language() string
	Detect(repoPath string) bool
	Analyze(ctx context.Context, repoPath string) (*RepoNeeds, error)
}

// ErrNoAnalyzer is returned by AnalyzePolyglot when no registered analyzer recognises a
// repository. Callers must surface it rather than substituting a default manifest.
var ErrNoAnalyzer = errors.New("needs: no matching language analyzer found for repository")

// AnalyzerRegistry maintains registered language analyzers for polyglot discovery.
type AnalyzerRegistry struct {
	mu        sync.RWMutex
	analyzers []LanguageAnalyzer
}

var (
	defaultRegistryOnce sync.Once
	defaultRegistry     *AnalyzerRegistry
)

// NewAnalyzerRegistry initializes an empty analyzer registry.
func NewAnalyzerRegistry() *AnalyzerRegistry {
	return &AnalyzerRegistry{
		analyzers: make([]LanguageAnalyzer, 0),
	}
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

// DefaultRegistry returns the singleton registry with all built-in language analyzers.
func DefaultRegistry() *AnalyzerRegistry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewAnalyzerRegistry()
		defaultRegistry.Register(NewGoAnalyzer())
		defaultRegistry.Register(NewNodeAnalyzer())
		defaultRegistry.Register(NewPythonAnalyzer())
		defaultRegistry.Register(NewRustAnalyzer())
		defaultRegistry.Register(NewNativeAnalyzer())
	})
	return defaultRegistry
}

// AnalyzePolyglot executes all matching analyzers and combines their dependency demands.
func (r *AnalyzerRegistry) AnalyzePolyglot(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	matched := r.DetectAll(repoPath)
	if len(matched) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoAnalyzer, repoPath)
	}

	primaryNeeds, err := matched[0].Analyze(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("primary analyzer %s failed: %w", matched[0].Language(), err)
	}

	for i := 1; i < len(matched); i++ {
		secNeeds, sErr := matched[i].Analyze(ctx, repoPath)
		if sErr != nil {
			return nil, fmt.Errorf("secondary analyzer %s failed: %w", matched[i].Language(), sErr)
		}
		mergeRepoNeeds(primaryNeeds, secNeeds)
	}

	return primaryNeeds, nil
}

// mergeRepoNeeds merges dependencies and capabilities from secondary analyzer results and
// from the sub-projects of a repository. A dependency whose demandIdentity dst already
// lists is not appended again: two sub-projects demanding one package are one demand of
// the repository.
func mergeRepoNeeds(dst, src *RepoNeeds) {
	if src == nil {
		return
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
