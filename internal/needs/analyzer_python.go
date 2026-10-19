package needs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/pymanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

// PythonAnalyzer extracts Python dependencies from requirements.txt or pyproject.toml.
type PythonAnalyzer struct{}

// NewPythonAnalyzer initializes a PythonAnalyzer instance.
func NewPythonAnalyzer() *PythonAnalyzer {
	return &PythonAnalyzer{}
}

// Language returns the language identifier.
func (a *PythonAnalyzer) Language() string {
	return "python"
}

// Detect checks if the repository contains Python project indicators.
func (a *PythonAnalyzer) Detect(repoPath string) bool {
	return util.FileExists(filepath.Join(repoPath, "requirements.txt")) ||
		util.FileExists(filepath.Join(repoPath, "pyproject.toml")) ||
		util.FileExists(filepath.Join(repoPath, "setup.py"))
}

// Analyze extracts Python dependencies and maps them to capabilities of the python target
// framework.
func (a *PythonAnalyzer) Analyze(ctx context.Context, repoPath string, target Target) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoName, fallback := projectRepositoryName("", repoPath)
	repoNeeds := &RepoNeeds{
		Version:            1,
		Repository:         repoName,
		RepositoryFallback: fallback,
		Language:           "python",
		Languages:          []string{"python"},
		Capabilities:       CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies:       make([]DependencyDemand, 0),
		UpdatedAt:          time.Now().UTC(),
	}

	deps, err := parsePythonDependencies(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Python dependencies in %q: %w", repoPath, err)
	}
	for _, pkg := range slices.Sorted(maps.Keys(deps)) {
		demand := pythonClassifier.classify(pkg, deps[pkg], target.RoutingKit())
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}
	target.applyTo(repoNeeds)

	if declErr := loadExistingDeclarations(ctx, repoPath, repoNeeds); declErr != nil {
		return nil, fmt.Errorf("failed to load existing declarations: %w", declErr)
	}
	calculateReadiness(repoNeeds)
	return repoNeeds, nil
}

func parsePythonDependencies(repoPath string) (map[string]string, error) {
	deps := make(map[string]string)
	manifests := []struct {
		name  string
		parse func(string, map[string]string) error
	}{
		{"requirements.txt", parseRequirementsFile},
		{"pyproject.toml", parsePyprojectFile},
		{"setup.py", parseSetupPyFile},
	}

	for _, m := range manifests {
		path := filepath.Join(repoPath, m.name)
		if !util.FileExists(path) {
			continue
		}
		if err := m.parse(path, deps); err != nil {
			return nil, err
		}
	}
	return deps, nil
}

// readManifest reads a repository-local manifest into memory safely.
func readManifest(path string) ([]byte, error) {
	// #nosec G304 -- path is filepath.Join(dir, "<constant filename>") for a repository
	// the caller already selected or, for the Cargo workspace-root search, one of its
	// parent directories (the search Cargo itself runs), or a path already confined to its
	// bundle directory by util.ConfinePath; no component is unvalidated user input.
	data, err := util.ReadFileLimited(path, 1024*1024)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", path, err)
	}
	return data, nil
}

// scanManifestLines applies visit to each trimmed line of path and reports read errors,
// which bufio.Scanner otherwise hides behind a false Scan() exactly like end of file.
func scanManifestLines(path string, visit func(line string)) error {
	data, err := readManifest(path)
	if err != nil {
		return err
	}
	return scanManifestData(path, data, visit)
}

// scanManifestData applies visit to each trimmed line of data, the manifest read from path,
// for a caller that prepares the bytes before they are split into lines.
func scanManifestData(path string, data []byte, visit func(line string)) error {
	if scanErr := scanBoundedLines(bufio.NewScanner(bytes.NewReader(data)), func(line string) {
		visit(strings.TrimSpace(line))
	}); scanErr != nil {
		return fmt.Errorf("failed to read %q: %w", path, scanErr)
	}
	return nil
}

// MaxScannedLines bounds manifest and patch scans without silently dropping a suffix.
const MaxScannedLines = 200000

// scanBoundedLines visits raw lines and reports read errors or a line limit breach.
func scanBoundedLines(scanner *bufio.Scanner, visit func(string)) error {
	for lines := 0; lines < MaxScannedLines; lines++ {
		if !scanner.Scan() {
			return scanner.Err()
		}
		visit(scanner.Text())
	}
	if scanner.Scan() {
		return fmt.Errorf("manifest exceeds %d lines", MaxScannedLines)
	}
	return scanner.Err()
}

func parseRequirementsFile(path string, deps map[string]string) error {
	return scanManifestLines(path, func(line string) {
		parsePythonReqLine(line, deps)
	})
}

// parsePythonReqLine records one PEP 508 requirement through the one requirement reader
// (pymanifest.ParseRequirement), which discards extras, environment markers and trailing
// comments and skips blank, comment and option lines.
func parsePythonReqLine(line string, deps map[string]string) {
	if requirement, ok := pymanifest.ParseRequirement(line); ok {
		deps[requirement.Name] = requirement.Version
	}
}

type pyprojectDocument struct {
	Project struct {
		Dependencies         []string            `toml:"dependencies"`
		OptionalDependencies map[string][]string `toml:"optional-dependencies"`
	} `toml:"project"`
	DependencyGroups map[string][]string `toml:"dependency-groups"`
	Tool             struct {
		Poetry struct {
			Dependencies    map[string]any `toml:"dependencies"`
			DevDependencies map[string]any `toml:"dev-dependencies"`
			Group           map[string]struct {
				Dependencies map[string]any `toml:"dependencies"`
			} `toml:"group"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

// parsePyprojectPoetry reads all Poetry sections from a pyproject.toml document.
func parsePyprojectPoetry(path string, doc *pyprojectDocument, deps map[string]string) error {
	poetry := doc.Tool.Poetry
	if err := parsePoetryDependencies(poetry.Dependencies, deps); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := parsePoetryDependencies(poetry.DevDependencies, deps); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	groups := slices.Sorted(maps.Keys(poetry.Group))
	if len(groups) > MaxScannedLines {
		return fmt.Errorf("%s Poetry groups exceed %d", path, MaxScannedLines)
	}
	for _, group := range groups {
		if err := parsePoetryDependencies(poetry.Group[group].Dependencies, deps); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

// parsePyprojectFile reads standard dependency arrays and Poetry dependency tables.
func parsePyprojectFile(path string, deps map[string]string) error {
	data, err := readManifest(path)
	if err != nil {
		return err
	}
	var document pyprojectDocument
	if err := util.DecodeTOML(path, data, &document); err != nil {
		return err
	}
	if err := parsePythonLists(document.Project.Dependencies, deps); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := parsePythonListGroups(document.Project.OptionalDependencies, deps); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := parsePythonListGroups(document.DependencyGroups, deps); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return parsePyprojectPoetry(path, &document, deps)
}

// parsePythonListGroups reads bounded named requirement lists.
func parsePythonListGroups(groups map[string][]string, deps map[string]string) error {
	keys := slices.Sorted(maps.Keys(groups))
	if len(keys) > MaxScannedLines {
		return fmt.Errorf("dependency groups exceed %d", MaxScannedLines)
	}
	for _, key := range keys {
		if err := parsePythonLists(groups[key], deps); err != nil {
			return err
		}
	}
	return nil
}

// parsePythonLists records bounded PEP 508 requirement strings.
func parsePythonLists(requirements []string, deps map[string]string) error {
	if len(requirements) > MaxScannedLines {
		return fmt.Errorf("dependency list exceeds %d", MaxScannedLines)
	}
	for _, requirement := range requirements {
		parsePythonReqLine(requirement, deps)
	}
	return nil
}

// parsePoetryDependencies records bounded Poetry dependency tables.
func parsePoetryDependencies(dependencies map[string]any, deps map[string]string) error {
	keys := slices.Sorted(maps.Keys(dependencies))
	if len(keys) > MaxScannedLines {
		return fmt.Errorf("poetry dependencies exceed %d", MaxScannedLines)
	}
	for _, name := range keys {
		if strings.EqualFold(name, "python") {
			continue
		}
		version := poetryVersion(dependencies[name])
		deps[strings.ToLower(name)] = version
	}
	return nil
}

// poetryVersion reads a Poetry version string or its version field.
func poetryVersion(value any) string {
	if version, ok := value.(string); ok {
		return version
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	version, isString := fields["version"].(string)
	if !isString {
		return ""
	}
	return version
}

// parseInlineRequirementList splits a comma-separated requirement list fragment.
func parseInlineRequirementList(fragment string, deps map[string]string) {
	for _, part := range strings.Split(fragment, ",") {
		clean := strings.TrimSpace(strings.Trim(strings.TrimSpace(part), `"'`))
		if clean != "" {
			parsePythonReqLine(clean, deps)
		}
	}
}

// parseSetupPyFile extracts the install_requires list of a setup.py, which is the only
// dependency declaration a setup.py-only repository has.
func parseSetupPyFile(path string, deps map[string]string) error {
	inList := false
	return scanManifestLines(path, func(line string) {
		if !inList {
			idx := strings.Index(line, "install_requires")
			if idx == -1 {
				return
			}
			open := strings.Index(line[idx:], "[")
			if open == -1 {
				return
			}
			inList = true
			line = line[idx+open+1:]
		}
		if closing := strings.Index(line, "]"); closing >= 0 {
			inList = false
			line = line[:closing]
		}
		parseInlineRequirementList(line, deps)
	})
}
