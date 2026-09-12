package needs

import (
	"bufio"
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
		repoNeeds := codifySingleHarvestRepo(item, patchesDir)
		results = append(results, repoNeeds)
	}

	return results, nil
}

func loadInventoryItems(path string) ([]HarvestRepoItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var items []HarvestRepoItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func codifySingleHarvestRepo(item HarvestRepoItem, patchesDir string) RepoNeeds {
	repoIdentifier := item.Remote
	if repoIdentifier == "" {
		repoIdentifier = item.Name
	}

	lang := inferLanguageFromItem(item)
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

	patchDeps := extractPatchDependencies(item.Name, patchesDir)
	for _, depPkg := range patchDeps {
		demand := mapInferredDependency(depPkg, lang)
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}

	calculateReadiness(&repoNeeds)
	return repoNeeds
}

func inferLanguageFromItem(item HarvestRepoItem) string {
	nameLower := strings.ToLower(item.Name)
	switch {
	case strings.Contains(nameLower, "svelte") || nameLower == "pelorus":
		return "typescript"
	case strings.Contains(nameLower, "python") || strings.Contains(nameLower, "arr"):
		return "python"
	case strings.Contains(nameLower, "ffmpeg") || nameLower == "vmafx" || strings.Contains(nameLower, "gpu"):
		return "native"
	case strings.Contains(nameLower, "rust"):
		return "rust"
	default:
		return "go"
	}
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

func extractPatchDependencies(repoName, patchesDir string) []string {
	deps := make([]string, 0)
	candidateNames := []string{
		repoName + ".patch",
		strings.ToLower(repoName) + ".patch",
	}

	var patchPath string
	for _, cand := range candidateNames {
		p := filepath.Join(patchesDir, cand)
		if util.FileExists(p) {
			patchPath = p
			break
		}
	}
	if patchPath == "" {
		return deps
	}

	file, err := os.Open(patchPath)
	if err != nil {
		return deps
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lines := 0; lines < MaxScannedLines && scanner.Scan(); lines++ {
		line := scanner.Text()
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			dep := parsePatchDependencyLine(strings.TrimPrefix(line, "+"))
			if dep != "" {
				deps = appendUniqueStr(deps, dep)
			}
		}
	}
	return deps
}

func parsePatchDependencyLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.Contains(trimmed, "import ") || strings.Contains(trimmed, "require ") || strings.Contains(trimmed, "dependency(") {
		return extractQuotedString(trimmed)
	}
	return ""
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
}
