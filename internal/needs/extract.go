package needs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cordanallm/praetor/internal/util"
	"github.com/golusoris/golusoris/core/astx"
	"github.com/golusoris/golusoris/core/codec/yaml"
)

// ScanRepo extracts framework capability needs and dependency mappings from a
// repository using the static catalog only. Prefer ScanRepoWith when a
// framework index (capabilities.yaml) is available.
func ScanRepo(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	return ScanRepoWith(ctx, repoPath, nil)
}

// ScanRepoWith is ScanRepo resolving dependencies against idx first (the
// framework's own capabilities.yaml contract), then the static catalog.
func ScanRepoWith(ctx context.Context, repoPath string, idx *FrameworkIndex) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	modulePath, goVer, directDeps, err := parseGoMod(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	astImports, err := scanASTImports(ctx, repoPath, modulePath)
	if err != nil {
		return nil, fmt.Errorf("failed to scan AST imports: %w", err)
	}

	repoNeeds := &RepoNeeds{
		Version:    1,
		Repository: modulePath,
		Language:   "go",
		GoVersion:  goVer,
		Framework:  defaultFrameworkModule,
		UpdatedAt:  time.Now().UTC(),
	}

	loadExistingDeclarations(repoPath, repoNeeds)
	buildDependencyDemands(directDeps, astImports, repoNeeds, idx)
	calculateReadiness(repoNeeds)

	return repoNeeds, nil
}

// parseGoMod extracts the module path, go version, and direct dependencies
// from go.mod via golang.org/x/mod (astx.ParseGoMod) — the go command's own parser.
func parseGoMod(goModPath string) (string, string, map[string]string, error) {
	if !util.FileExists(goModPath) {
		return "unknown", "1.27", make(map[string]string), nil
	}
	mod, err := astx.ParseGoMod(goModPath)
	if err != nil {
		return "", "", nil, err
	}
	directDeps := make(map[string]string)
	for _, r := range mod.Direct() {
		directDeps[r.Path] = r.Version
	}
	return mod.Module, mod.Go, directDeps, nil
}

// legacySkipDirs are praetor-specific directories excluded on top of the astx
// defaults (vendor, testdata, node_modules, dot- and underscore-prefixed).
var legacySkipDirs = []string{"scratch", "cache"}

// scanASTImports traverses the repo and extracts all unique third-party imports.
func scanASTImports(ctx context.Context, rootDir, modulePath string) (map[string]struct{}, error) {
	thirdParty := make(map[string]struct{})
	err := astx.Walk(ctx, rootDir, astx.WalkOptions{SkipDirs: legacySkipDirs}, func(path string) error {
		imports, parseErr := astx.Imports(path)
		if parseErr != nil {
			return nil // Skip unparseable generated code gracefully
		}
		for _, imp := range imports {
			if astx.IsThirdParty(imp, modulePath) {
				thirdParty[imp] = struct{}{}
			}
		}
		return nil
	})
	return thirdParty, err
}

// shouldSkipDir checks whether the directory should be skipped during fleet traversal.
func shouldSkipDir(info os.FileInfo, path string) bool {
	if !info.IsDir() {
		return false
	}
	name := info.Name()
	if name == "." || name == ".." {
		// The walk root itself ("." when --path=.) must never be skipped.
		return false
	}
	return name == "vendor" || name == ".git" || name == ".devcontainer" ||
		name == "node_modules" || strings.HasPrefix(name, ".") ||
		name == "scratch" || name == "cache" || strings.Contains(path, "/scratch") ||
		strings.Contains(path, "/.workingdir")
}

// loadExistingDeclarations checks for existing .needs.yaml or .standards.yaml declarations.
func loadExistingDeclarations(repoPath string, repoNeeds *RepoNeeds) {
	needsPath := filepath.Join(repoPath, ".needs.yaml")
	if util.FileExists(needsPath) {
		data, err := os.ReadFile(needsPath)
		if err == nil {
			var existing RepoNeeds
			if yaml.UnmarshalLenient(data, &existing) == nil {
				repoNeeds.Capabilities = existing.Capabilities
				return
			}
		}
	}

	standardsPath := filepath.Join(repoPath, ".standards.yaml")
	if util.FileExists(standardsPath) {
		data, err := os.ReadFile(standardsPath)
		if err == nil {
			var st struct {
				Needs CapabilityDeclaration `yaml:"needs"`
			}
			if yaml.UnmarshalLenient(data, &st) == nil && len(st.Needs.Required) > 0 {
				repoNeeds.Capabilities = st.Needs
			}
		}
	}
}

// buildDependencyDemands maps discovered packages to capabilities and Golusoris
// replacements. Resolution order: the framework's own contract (idx), the
// static catalog, native Go ecosystem libraries, then a custom gap.
func buildDependencyDemands(directDeps map[string]string, astImports map[string]struct{}, repoNeeds *RepoNeeds, idx *FrameworkIndex) {
	pkgSet := make(map[string]string)
	for pkg, ver := range directDeps {
		pkgSet[pkg] = ver
	}
	for imp := range astImports {
		if _, ok := pkgSet[imp]; !ok {
			pkgSet[imp] = ""
		}
	}

	demands := make([]DependencyDemand, 0, len(pkgSet))
	for pkg, ver := range pkgSet {
		demands = append(demands, classifyDependency(pkg, ver, idx))
	}

	sort.Slice(demands, func(i, j int) bool {
		return demands[i].Package < demands[j].Package
	})
	repoNeeds.Dependencies = demands
}

// nativePrefixes are Go-ecosystem runtime libraries that need no framework
// replacement: the extended standard library and protobuf/grpc codegen runtimes.
var nativePrefixes = []string{"golang.org/x/", "google.golang.org/protobuf", "google.golang.org/genproto"}

// FleetPrefixEnv lists extra first-party module prefixes (comma-separated)
// that must never be reported as framework gaps.
const FleetPrefixEnv = "PRAETOR_FLEET_PREFIXES"

// FleetPrefixes are module prefixes owned by the fleet itself: the framework,
// its org libraries, and sibling organisations. Dependencies on them are
// first-party, never gaps.
var FleetPrefixes = []string{
	defaultFrameworkModule, "github.com/golusoris/", "github.com/lusoris/",
	"github.com/cordanallm/", "github.com/cordanaLLM/", "cauda.dev/", "cordana.dev/",
}

func fleetPrefixes() []string {
	out := append([]string{}, FleetPrefixes...)
	for _, p := range strings.Split(os.Getenv(FleetPrefixEnv), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func classifyDependency(pkg, ver string, idx *FrameworkIndex) DependencyDemand {
	demand := DependencyDemand{Package: pkg, Version: ver}
	for _, p := range fleetPrefixes() {
		if strings.HasPrefix(pkg, p) {
			demand.Capability = CapabilityKey("fleet." + sanitizePackageName(pkg))
			demand.Status = StatusNative
			demand.Notes = "First-party fleet module; not a framework gap"
			return demand
		}
	}
	if idx != nil {
		if target, caps, ok := idx.ResolveReplacement(pkg); ok {
			demand.Status = StatusCovered
			demand.GolusorisReplacement = target
			if len(caps) > 0 {
				demand.Capability = caps[0]
			} else {
				demand.Capability = CapabilityKey("custom." + sanitizePackageName(pkg))
			}
			demand.Notes = "Superseded per the framework capabilities.yaml contract"
			return demand
		}
	}
	if entry, found := MatchPackage(pkg); found {
		demand.Capability = entry.Capability
		demand.Status = entry.Status
		demand.GolusorisReplacement = entry.GolusorisReplacement
		demand.Notes = entry.Notes
		return demand
	}
	for _, p := range nativePrefixes {
		if strings.HasPrefix(pkg, p) {
			demand.Capability = CapabilityKey("native." + sanitizePackageName(pkg))
			demand.Status = StatusNative
			demand.Notes = "Go ecosystem runtime library; no framework replacement needed"
			return demand
		}
	}
	demand.Capability = CapabilityKey("custom." + sanitizePackageName(pkg))
	demand.Status = StatusGap
	demand.Notes = "Third-party package without native Golusoris equivalent"
	return demand
}

// majorSuffixRE matches a trailing Go major-version path element (/v2, /v10).
var majorSuffixRE = regexp.MustCompile(`^v[0-9]+$`)

// sanitizePackageName converts an import path into a capability identifier.
// Major-version suffixes and generic leaf names (api, v2, pkg, lib, internal)
// are skipped so that github.com/foo/bar/v2/api yields "bar", not "api".
func sanitizePackageName(pkg string) string {
	generic := map[string]bool{"api": true, "pkg": true, "lib": true, "internal": true, "go": true, "core": true, "client": true, "server": true, "types": true}
	parts := strings.Split(pkg, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.ToLower(parts[i])
		if p == "" || majorSuffixRE.MatchString(p) || generic[p] {
			continue
		}
		if i == 0 && strings.Contains(p, ".") {
			// Only a host is left (e.g. gopkg.in) — use the next element.
			continue
		}
		return strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimPrefix(p, "go-"))
	}
	return "lib"
}

// calculateReadiness computes the framework adoption score and dependency counts.
func calculateReadiness(repoNeeds *RepoNeeds) {
	total := len(repoNeeds.Dependencies)
	covered := 0
	gap := 0

	for _, d := range repoNeeds.Dependencies {
		if d.Status == StatusCovered || d.Status == StatusAdapterAvailable || d.Status == StatusNative {
			covered++
		} else if d.Status == StatusGap {
			gap++
		}
	}

	score := 100.0
	if total > 0 {
		score = (float64(covered) / float64(total)) * 100.0
	}

	repoNeeds.Readiness = ReadinessMetrics{
		Score:               score,
		TotalThirdPartyDeps: total,
		CoveredDeps:         covered,
		GapDeps:             gap,
	}
}

// WriteNeedsManifest serializes the RepoNeeds to .needs.yaml.
func WriteNeedsManifest(repoPath string, repoNeeds *RepoNeeds) error {
	targetFile := filepath.Join(repoPath, ".needs.yaml")
	if err := yaml.WriteFile(targetFile, repoNeeds, 0o644); err != nil {
		return fmt.Errorf("failed to write needs manifest: %w", err)
	}
	return nil
}
