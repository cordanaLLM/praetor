package needs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// HarvestRepoItem represents a single repository entry from dev-inventory.json.
type HarvestRepoItem struct {
	Name          string   `json:"Name"`
	Type          string   `json:"Type"`
	Path          string   `json:"Path"`
	Remote        string   `json:"Remote"`
	Branch        string   `json:"Branch"`
	DirtyCount    int      `json:"DirtyCount"`
	UnpushedCount int      `json:"UnpushedCount"`
	HasPatches    bool     `json:"HasPatches"`
	StatusDetails []string `json:"StatusDetails"`
}

// CodifyHarvestedInventory reads a harvest bundle and produces codified RepoNeeds manifests.
func CodifyHarvestedInventory(ctx context.Context, harvestPath string) ([]RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	inventoryFile := filepath.Join(harvestPath, "dev-inventory", "dev-inventory.json")
	if !util.FileExists(inventoryFile) {
		return nil, fmt.Errorf("inventory file not found: %s", inventoryFile)
	}

	items, err := loadInventoryItems(inventoryFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load inventory: %w", err)
	}

	results := make([]RepoNeeds, 0, len(items))
	patchesDir := filepath.Join(harvestPath, "dev-patches")

	for _, item := range items {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		repoNeeds, codifyErr := codifySingleHarvestRepo(item, patchesDir)
		if codifyErr != nil {
			return nil, fmt.Errorf("failed to codify harvested repo %q: %w", item.Name, codifyErr)
		}
		results = append(results, repoNeeds)
	}

	return results, nil
}

func loadInventoryItems(path string) ([]HarvestRepoItem, error) {
	// #nosec G304 -- path is filepath.Join(harvestPath, "dev-inventory",
	// "dev-inventory.json"); every component after the caller-selected bundle root is a
	// constant.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", path, err)
	}
	var items []HarvestRepoItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("failed to parse %q: %w", path, err)
	}
	return items, nil
}

func codifySingleHarvestRepo(item HarvestRepoItem, patchesDir string) (RepoNeeds, error) {
	repoIdentifier := item.Remote
	if repoIdentifier == "" {
		repoIdentifier = item.Name
	}

	patchDeps, err := extractPatchDependencies(item.Name, patchesDir)
	if err != nil {
		return RepoNeeds{}, err
	}

	lang := inferLanguageFromItem(item, patchDeps)
	if lang == LanguageUnsupported {
		return unsupportedHarvestRepo(repoIdentifier), nil
	}
	repoNeeds := RepoNeeds{
		Version:      1,
		Repository:   repoIdentifier,
		Language:     lang,
		Languages:    []string{lang},
		Framework:    determineDefaultFramework(lang),
		BuilderKits:  determineDefaultBuilderKits(lang),
		Capabilities: CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies: make([]DependencyDemand, 0),
		UpdatedAt:    time.Now().UTC(),
	}

	for _, depPkg := range patchDeps {
		demand := mapInferredDependency(depPkg, lang)
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}

	calculateReadiness(&repoNeeds)
	return repoNeeds, nil
}

// unsupportedHarvestRepo records a harvested repository whose language could not be
// determined. It carries no framework, builder kit, dependencies or readiness score: any
// of those would be a Go default presented as a finding about the repository.
func unsupportedHarvestRepo(repository string) RepoNeeds {
	return RepoNeeds{
		Version:      1,
		Repository:   repository,
		Language:     LanguageUnsupported,
		Languages:    []string{LanguageUnsupported},
		BuilderKits:  make([]string, 0),
		Capabilities: CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies: make([]DependencyDemand, 0),
		UpdatedAt:    time.Now().UTC(),
	}
}

// inferLanguageFromItem guesses a harvested repository's language. Dependencies already
// extracted from its patch are the stronger signal and take precedence over the name.
// Neither signal -> LanguageUnsupported, never a Go default (BUG-864).
func inferLanguageFromItem(item HarvestRepoItem, patchDeps []string) string {
	if lang := inferLanguageFromDeps(patchDeps); lang != "" {
		return lang
	}

	nameLower := strings.ToLower(item.Name)
	switch {
	case strings.Contains(nameLower, "svelte") || nameLower == "pelorus":
		return "typescript"
	case strings.Contains(nameLower, "python") || isArrStackName(nameLower):
		return "python"
	case strings.Contains(nameLower, "ffmpeg") || nameLower == "vmafx" || strings.Contains(nameLower, "gpu"):
		return "native"
	case strings.Contains(nameLower, "rust"):
		return "rust"
	default:
		return LanguageUnsupported
	}
}

// isArrStackName reports whether a repository name is one of the *arr projects, matching
// on whole name segments rather than on the substring "arr".
func isArrStackName(nameLower string) bool {
	// The *arr media-automation projects. Matching the bare substring "arr" instead
	// classifies every repository whose name merely contains those three letters
	// (barrier, narrative-api, go-arrow) as Python.
	arrStackNames := map[string]struct{}{
		"sonarr": {}, "radarr": {}, "lidarr": {}, "readarr": {}, "prowlarr": {},
		"bazarr": {}, "whisparr": {}, "tdarr": {}, "mylar": {},
	}
	segments := strings.FieldsFunc(nameLower, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/'
	})
	for _, seg := range segments {
		if _, ok := arrStackNames[seg]; ok {
			return true
		}
	}
	return false
}

// inferLanguageFromDeps derives the language from the shape of the harvested dependency
// names: a Go module path is unambiguous, everything else stays undecided.
func inferLanguageFromDeps(deps []string) string {
	for _, dep := range deps {
		first := strings.Split(dep, "/")[0]
		if strings.Contains(first, ".") && strings.Contains(dep, "/") {
			return "go"
		}
	}
	return ""
}

func determineDefaultFramework(lang string) string {
	switch lang {
	case "typescript":
		return "github.com/golusoris/sveltesentio"
	case "python":
		return "github.com/golusoris/pykit"
	case "rust":
		return "github.com/golusoris/rustkit"
	case "native":
		return "github.com/golusoris/template-native-gpu"
	default:
		return "github.com/golusoris/golusoris"
	}
}

func determineDefaultBuilderKits(lang string) []string {
	switch lang {
	case "typescript":
		return []string{"golusoris/sveltesentio"}
	case "python":
		return []string{"golusoris/pykit"}
	case "rust":
		return []string{"golusoris/rustkit"}
	case "native":
		return []string{"golusoris/template-native-gpu"}
	default:
		return []string{"golusoris/golusoris", "golusoris/goenvoy"}
	}
}

func extractPatchDependencies(repoName, patchesDir string) ([]string, error) {
	patchPath := locatePatchFile(repoName, patchesDir)
	if patchPath == "" {
		return make([]string, 0), nil
	}

	deps := make([]string, 0)
	scanner := patchDepScanner{}
	err := scanManifestLines(patchPath, func(line string) {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			return
		}
		for _, dep := range scanner.consume(strings.TrimPrefix(line, "+")) {
			deps = appendUniqueStr(deps, dep)
		}
	})
	if err != nil {
		return nil, err
	}
	return deps, nil
}

// locatePatchFile resolves the patch file for a harvested repository, if one exists. The
// repository name comes from the harvested inventory JSON, so the candidate path is
// confined to patchesDir before it is used.
func locatePatchFile(repoName, patchesDir string) string {
	for _, cand := range []string{repoName + ".patch", strings.ToLower(repoName) + ".patch"} {
		p, err := util.ConfinePath(patchesDir, cand)
		if err != nil {
			continue
		}
		if util.FileExists(p) {
			return p
		}
	}
	return ""
}

// patchDepScanner tracks the multi-line Go `import (` / `require (` blocks that a
// line-at-a-time matcher would otherwise miss entirely: gofmt puts the keyword and the
// quoted paths on different lines.
type patchDepScanner struct {
	inImportBlock  bool
	inRequireBlock bool
}

// consume extracts the dependencies declared by one added patch line.
func (s *patchDepScanner) consume(line string) []string {
	trimmed := strings.TrimSpace(line)
	if s.inImportBlock || s.inRequireBlock {
		return s.consumeBlockLine(trimmed)
	}
	switch {
	case strings.HasPrefix(trimmed, "import ("):
		s.inImportBlock = true
		return nil
	case strings.HasPrefix(trimmed, "require ("):
		s.inRequireBlock = true
		return nil
	}
	return parsePatchDependencyLine(trimmed)
}

// consumeBlockLine extracts a dependency from inside an import or require block.
func (s *patchDepScanner) consumeBlockLine(trimmed string) []string {
	if strings.HasPrefix(trimmed, ")") {
		s.inImportBlock = false
		s.inRequireBlock = false
		return nil
	}
	if trimmed == "" || strings.HasPrefix(trimmed, "//") {
		return nil
	}
	if s.inImportBlock {
		return nonEmpty(extractQuotedString(trimmed))
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return nil
	}
	return nonEmpty(fields[0])
}

// nonEmpty wraps a single value into a slice, dropping the empty string.
func nonEmpty(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}

// parsePatchDependencyLine extracts the dependency declared by a single-line import,
// require, JavaScript require() call, Meson dependency() call or Python import.
func parsePatchDependencyLine(trimmed string) []string {
	if quoted := quotedDependency(trimmed); quoted != "" {
		return []string{quoted}
	}
	if after, ok := strings.CutPrefix(trimmed, "require "); ok {
		fields := strings.Fields(after)
		if len(fields) >= 2 {
			return []string{fields[0]}
		}
	}
	return pythonImportModule(trimmed)
}

// quotedDependency returns the quoted argument of an import/require/dependency form.
func quotedDependency(trimmed string) string {
	markers := []string{"import ", "require ", "require(", "dependency(", "from '", "from \""}
	for _, marker := range markers {
		idx := strings.Index(trimmed, marker)
		if idx == -1 {
			continue
		}
		if quoted := extractQuotedString(trimmed[idx:]); quoted != "" {
			return quoted
		}
	}
	return ""
}

// pythonImportModule extracts the top-level module of an unquoted Python import.
func pythonImportModule(trimmed string) []string {
	var spec string
	switch {
	case strings.HasPrefix(trimmed, "from "):
		rest := strings.TrimPrefix(trimmed, "from ")
		idx := strings.Index(rest, " import ")
		if idx == -1 {
			return nil
		}
		spec = strings.TrimSpace(rest[:idx])
	case strings.HasPrefix(trimmed, "import "):
		spec = strings.TrimSpace(strings.TrimPrefix(trimmed, "import "))
	default:
		return nil
	}

	spec = strings.Split(spec, " ")[0]
	spec = strings.Split(spec, ",")[0]
	spec = strings.Split(spec, ".")[0]
	spec = strings.TrimLeft(spec, ".")
	if spec == "" || strings.ContainsAny(spec, `"'()`) {
		return nil
	}
	return []string{spec}
}

func mapInferredDependency(pkg, lang string) DependencyDemand {
	switch lang {
	case "typescript":
		return mapNodeDependency(pkg, "latest")
	case "python":
		return mapPythonDependency(pkg, "latest")
	case "rust":
		return mapRustDependency(pkg, "latest")
	case "native":
		return mapNativeDependency(pkg, "latest")
	default:
		return mapHarvestedGoDependency(pkg)
	}
}

// mapHarvestedGoDependency resolves a harvested Go import path against the catalog.
func mapHarvestedGoDependency(pkg string) DependencyDemand {
	matched, found := MatchPackage(pkg)
	if found {
		return DependencyDemand{
			Package:              pkg,
			Language:             "go",
			Ecosystem:            "go",
			Capability:           matched.Capability,
			Status:               matched.Status,
			GolusorisReplacement: matched.GolusorisReplacement,
			TargetBuilderKit:     "golusoris/golusoris",
			Notes:                matched.Notes,
		}
	}
	return DependencyDemand{
		Package:          pkg,
		Language:         "go",
		Ecosystem:        "go",
		Capability:       CapabilityKey("go.external." + cleanDepKey(pkg)),
		Status:           StatusGap,
		TargetBuilderKit: "golusoris/golusoris",
		Notes:            "Harvested external dependency from patch diff",
	}
}
