package needs

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// RustAnalyzer extracts Rust crate dependencies from Cargo.toml.
type RustAnalyzer struct{}

// NewRustAnalyzer initializes a RustAnalyzer instance.
func NewRustAnalyzer() *RustAnalyzer {
	return &RustAnalyzer{}
}

// Language returns the language identifier.
func (a *RustAnalyzer) Language() string {
	return "rust"
}

// Detect checks if the repository contains Cargo.toml.
func (a *RustAnalyzer) Detect(repoPath string) bool {
	return util.FileExists(filepath.Join(repoPath, "Cargo.toml"))
}

// Analyze extracts dependencies from Cargo.toml and maps to rustkit capabilities.
func (a *RustAnalyzer) Analyze(ctx context.Context, repoPath string) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoName := filepath.Base(repoPath)
	repoNeeds := &RepoNeeds{
		Version:      1,
		Repository:   repoName,
		Language:     "rust",
		Languages:    []string{"rust"},
		Framework:    "github.com/golusoris/rustkit",
		BuilderKits:  []string{"golusoris/rustkit"},
		Capabilities: CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies: make([]DependencyDemand, 0),
		UpdatedAt:    time.Now().UTC(),
	}

	deps, err := parseCargoToml(filepath.Join(repoPath, "Cargo.toml"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Cargo.toml in %q: %w", repoPath, err)
	}
	for _, pkg := range slices.Sorted(maps.Keys(deps)) {
		demand := mapRustDependency(pkg, deps[pkg])
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}

	if declErr := loadExistingDeclarations(repoPath, repoNeeds); declErr != nil {
		return nil, fmt.Errorf("failed to load existing declarations: %w", declErr)
	}
	calculateReadiness(repoNeeds)
	return repoNeeds, nil
}

// cargoState tracks which Cargo.toml table the line scanner is inside.
type cargoState struct {
	inDepsTable bool   // inside a [*dependencies] table of `name = version` entries
	subTableFor string // crate name when inside a [dependencies.<crate>] sub-table
}

// parseCargoToml collects crate dependencies from every dependency table spelling Cargo
// accepts: [dependencies], [dev-dependencies], [build-dependencies],
// [workspace.dependencies], [target.'cfg(...)'.dependencies] and the
// [dependencies.<crate>] sub-table form, including inline `{ version = "1" }` tables.
func parseCargoToml(cargoPath string) (map[string]string, error) {
	deps := make(map[string]string)
	st := cargoState{}
	if err := scanManifestLines(cargoPath, func(line string) {
		st.consume(line, deps)
	}); err != nil {
		return nil, err
	}
	return deps, nil
}

// consume classifies a single trimmed Cargo.toml line.
func (s *cargoState) consume(line string, deps map[string]string) {
	if strings.HasPrefix(line, "[") {
		s.enterTable(strings.Trim(line, "[]"), deps)
		return
	}
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	if s.subTableFor != "" {
		if name, value, ok := splitTOMLAssignment(line); ok && name == "version" {
			deps[s.subTableFor] = value
		}
		return
	}
	if !s.inDepsTable {
		return
	}
	if name, value, ok := splitTOMLAssignment(line); ok {
		deps[name] = value
	}
}

// enterTable updates the scanner state for a TOML table header.
func (s *cargoState) enterTable(header string, deps map[string]string) {
	s.inDepsTable = false
	s.subTableFor = ""

	segments := strings.Split(header, ".")
	last := strings.Trim(segments[len(segments)-1], `"'`)
	if isCargoDependencyTable(last) {
		s.inDepsTable = true
		return
	}
	if len(segments) >= 2 && isCargoDependencyTable(strings.Trim(segments[len(segments)-2], `"'`)) {
		s.subTableFor = last
		if _, exists := deps[last]; !exists {
			deps[last] = ""
		}
	}
}

// isCargoDependencyTable reports whether a table name holds crate dependencies.
func isCargoDependencyTable(name string) bool {
	return name == "dependencies" || name == "dev-dependencies" ||
		name == "build-dependencies"
}

// splitTOMLAssignment splits `key = value` and normalises the value: a bare string loses
// its quotes, an inline table is reduced to its `version` field.
func splitTOMLAssignment(line string) (string, string, bool) {
	idx := strings.Index(line, "=")
	if idx <= 0 {
		return "", "", false
	}
	name := strings.Trim(strings.TrimSpace(line[:idx]), `"'`)
	if name == "" {
		return "", "", false
	}
	value := strings.TrimSpace(line[idx+1:])
	if strings.HasPrefix(value, "{") {
		return name, inlineTableVersion(value), true
	}
	if comment := strings.Index(value, "#"); comment >= 0 {
		value = strings.TrimSpace(value[:comment])
	}
	return name, strings.Trim(value, `"',`), true
}

// inlineTableVersion extracts the `version` field of a TOML inline table, returning an
// empty string when the table pins the dependency by path, git revision or workspace.
func inlineTableVersion(value string) string {
	body := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(value), "{"), "}")
	for _, field := range strings.Split(body, ",") {
		name, fieldValue, ok := splitTOMLScalar(field)
		if ok && name == "version" {
			return fieldValue
		}
	}
	return ""
}

// splitTOMLScalar splits a single `key = "value"` pair without recursing into tables.
func splitTOMLScalar(field string) (string, string, bool) {
	idx := strings.Index(field, "=")
	if idx <= 0 {
		return "", "", false
	}
	name := strings.Trim(strings.TrimSpace(field[:idx]), `"'`)
	value := strings.Trim(strings.TrimSpace(field[idx+1:]), `"',`)
	if name == "" {
		return "", "", false
	}
	return name, value, true
}

func mapRustDependency(pkg, ver string) DependencyDemand {
	mapping, found := lookupRustCatalog(strings.ToLower(pkg))
	if found {
		return DependencyDemand{
			Package:              pkg,
			Version:              ver,
			Language:             "rust",
			Ecosystem:            "cargo",
			Capability:           mapping.Capability,
			Status:               mapping.Status,
			GolusorisReplacement: mapping.Replacement,
			TargetBuilderKit:     "golusoris/rustkit",
			Notes:                mapping.Notes,
		}
	}
	return DependencyDemand{
		Package:          pkg,
		Version:          ver,
		Language:         "rust",
		Ecosystem:        "cargo",
		Capability:       CapabilityKey("rust.external." + cleanDepKey(pkg)),
		Status:           StatusGap,
		TargetBuilderKit: "golusoris/rustkit",
		Notes:            "External Cargo crate requiring RustKit adapter or evaluation",
	}
}
