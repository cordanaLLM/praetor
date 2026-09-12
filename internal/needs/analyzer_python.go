package needs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// Analyze extracts Python dependencies and maps them to pykit capabilities.
func (a *PythonAnalyzer) Analyze(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoName := filepath.Base(repoPath)
	repoNeeds := &RepoNeeds{
		Version:      1,
		Repository:   repoName,
		Language:     "python",
		Languages:    []string{"python"},
		Framework:    "github.com/golusoris/pykit",
		BuilderKits:  []string{"golusoris/pykit"},
		Capabilities: CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies: make([]DependencyDemand, 0),
		UpdatedAt:    time.Now().UTC(),
	}

	deps, err := parsePythonDependencies(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Python dependencies in %q: %w", repoPath, err)
	}
	for _, pkg := range sortedKeys(deps) {
		demand := mapPythonDependency(pkg, deps[pkg])
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}

	if declErr := loadExistingDeclarations(repoPath, repoNeeds); declErr != nil {
		return nil, fmt.Errorf("failed to load existing declarations: %w", declErr)
	}
	calculateReadiness(repoNeeds)
	return repoNeeds, nil
}

// sortedKeys returns the map keys in lexical order so that dependency lists (and every
// report derived from them) are deterministic across runs.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// openManifest opens a repository-local manifest for line scanning.
func openManifest(path string) (*os.File, error) {
	// #nosec G304 -- path is either filepath.Join(repoPath, "<constant filename>") for a
	// repository the caller already selected, or a path already confined to its bundle
	// directory by util.ConfinePath; no component is unvalidated user input.
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %q: %w", path, err)
	}
	return file, nil
}

// scanManifestLines applies visit to each trimmed line of path and reports read errors,
// which bufio.Scanner otherwise hides behind a false Scan() exactly like end of file.
func scanManifestLines(path string, visit func(line string)) (err error) {
	file, openErr := openManifest(path)
	if openErr != nil {
		return openErr
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close %q: %w", path, cerr)
		}
	}()

	if scanErr := scanBoundedLines(bufio.NewScanner(file), func(line string) {
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
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			return
		}
		parsePythonReqLine(line, deps)
	})
}

// parsePythonReqLine records one PEP 508 requirement, discarding extras, environment
// markers and trailing comments before the name/version split.
func parsePythonReqLine(line string, deps map[string]string) {
	spec := stripRequirementDecorations(line)
	if spec == "" {
		return
	}

	separators := []string{"===", "==", ">=", "<=", "~=", "!=", ">", "<", "="}
	pkg := spec
	ver := ""
	for _, sep := range separators {
		idx := strings.Index(spec, sep)
		if idx == -1 {
			continue
		}
		pkg = strings.TrimSpace(spec[:idx])
		ver = strings.TrimSpace(spec[idx+len(sep):])
		break
	}
	if pkg != "" {
		deps[strings.ToLower(pkg)] = ver
	}
}

// stripRequirementDecorations removes comments, environment markers and extras from a
// requirement specifier: `requests[security]>=2 ; python_version<"3.8"  # note`.
func stripRequirementDecorations(line string) string {
	spec := strings.TrimSpace(line)
	if idx := strings.Index(spec, "#"); idx >= 0 {
		spec = strings.TrimSpace(spec[:idx])
	}
	if idx := strings.Index(spec, ";"); idx >= 0 {
		spec = strings.TrimSpace(spec[:idx])
	}
	if open := strings.Index(spec, "["); open >= 0 {
		if closing := strings.Index(spec[open:], "]"); closing >= 0 {
			spec = spec[:open] + spec[open+closing+1:]
		}
	}
	return strings.TrimSpace(strings.Trim(spec, `"',`))
}

// pyprojectState tracks which pyproject.toml construct the scanner is inside.
type pyprojectState struct {
	inListTable  bool // inside a multi-line dependency list
	inPoetryDeps bool // inside a [tool.poetry.*dependencies] table
	anyKeyIsList bool // inside a table whose every key holds a requirement list
}

// parsePyprojectFile reads PEP 621 `dependencies`/`optional-dependencies` lists in both
// their single-line and multi-line spellings, plus Poetry dependency tables.
func parsePyprojectFile(path string, deps map[string]string) error {
	st := pyprojectState{}
	return scanManifestLines(path, func(line string) {
		st.consume(line, deps)
	})
}

// consume classifies a single trimmed pyproject.toml line.
func (s *pyprojectState) consume(line string, deps map[string]string) {
	if strings.HasPrefix(line, "[") {
		s.inListTable = false
		s.inPoetryDeps = strings.HasPrefix(line, "[tool.poetry") && strings.Contains(line, "dependencies")
		s.anyKeyIsList = strings.Contains(line, "optional-dependencies") ||
			strings.Contains(line, "dependency-groups")
		return
	}
	if s.inListTable {
		if strings.HasPrefix(line, "]") {
			s.inListTable = false
			return
		}
		parsePythonReqLine(line, deps)
		return
	}
	if s.inPoetryDeps {
		parsePoetryDepLine(line, deps)
		return
	}
	s.inListTable = parseDependencyListAssignment(line, deps, s.anyKeyIsList)
}

// parseDependencyListAssignment handles `<name> = [ ... ]` assignments. It records every
// entry present on the same line and reports whether the list continues on later lines.
func parseDependencyListAssignment(line string, deps map[string]string, anyKey bool) bool {
	open := strings.Index(line, "= [")
	if open == -1 {
		return false
	}
	if !anyKey && !isDependencyListKey(strings.TrimSpace(line[:open])) {
		return false
	}
	rest := line[open+len("= ["):]
	if closing := strings.Index(rest, "]"); closing >= 0 {
		parseInlineRequirementList(rest[:closing], deps)
		return false
	}
	parseInlineRequirementList(rest, deps)
	return true
}

// isDependencyListKey reports whether a TOML key holds a PEP 508 requirement list.
func isDependencyListKey(key string) bool {
	key = strings.Trim(key, `"'`)
	return key == "dependencies" || strings.HasSuffix(key, "-dependencies") ||
		strings.HasSuffix(key, "_requires") || strings.HasSuffix(key, "install_requires")
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

// parsePoetryDepLine records one `name = "^1.2"` or `name = { version = "1" }` entry.
func parsePoetryDepLine(line string, deps map[string]string) {
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	name, value, ok := splitTOMLAssignment(line)
	if !ok || strings.EqualFold(name, "python") {
		return
	}
	deps[strings.ToLower(name)] = value
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

func mapPythonDependency(pkg, ver string) DependencyDemand {
	mapping, found := lookupPythonCatalog(strings.ToLower(pkg))
	if found {
		return DependencyDemand{
			Package:              pkg,
			Version:              ver,
			Language:             "python",
			Ecosystem:            "pypi",
			Capability:           mapping.Capability,
			Status:               mapping.Status,
			GolusorisReplacement: mapping.Replacement,
			TargetBuilderKit:     "golusoris/pykit",
			Notes:                mapping.Notes,
		}
	}
	return DependencyDemand{
		Package:          pkg,
		Version:          ver,
		Language:         "python",
		Ecosystem:        "pypi",
		Capability:       CapabilityKey("python.external." + cleanDepKey(pkg)),
		Status:           StatusGap,
		TargetBuilderKit: "golusoris/pykit",
		Notes:            "External PyPI dependency requiring PyKit adapter or evaluation",
	}
}
