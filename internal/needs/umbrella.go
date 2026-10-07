package needs

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"path/filepath"
	"slices"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// UmbrellaStatus is the verdict on one import of a framework's umbrella package.
type UmbrellaStatus string

const (
	// UmbrellaRecommend: the project wires some, not all, of the umbrella's groupings; the
	// recommended sub-package imports replace the umbrella import.
	UmbrellaRecommend UmbrellaStatus = "recommend"
	// UmbrellaJustified: the project wires every grouping; the umbrella import is justified.
	UmbrellaJustified UmbrellaStatus = "justified"
	// UmbrellaUnmapped: the project references no grouping, only identifiers no grouping
	// describes or a blank or dot import; there is nothing to recommend.
	UmbrellaUnmapped UmbrellaStatus = "unmapped"
	// UmbrellaUnknown: the project imports the framework's root package, but the capability
	// contract describes no groupings for it. It is reported, never guessed at.
	UmbrellaUnknown UmbrellaStatus = "unknown"
)

// UmbrellaFinding is one umbrella package a Go project of the repository imports.
type UmbrellaFinding struct {
	// Project is the Go project's directory relative to the repository root, "." for the root.
	Project  string         `json:"project"`
	Umbrella string         `json:"umbrella"`
	Status   UmbrellaStatus `json:"status"`
	// Files are the source files importing the umbrella, relative to the repository root.
	Files []string `json:"files"`
	// Groupings are the umbrella's groupings the project references, sorted; TotalGroupings
	// counts every grouping the contract describes.
	Groupings      []string `json:"groupings,omitempty"`
	TotalGroupings int      `json:"total_groupings,omitempty"`
	// Unmapped are the umbrella identifiers the project references that no grouping names,
	// and a blank or dot import of it: each keeps the umbrella import.
	Unmapped        []string                 `json:"unmapped,omitempty"`
	Recommendations []UmbrellaRecommendation `json:"recommendations,omitempty"`
	// Measurement is what the switch removes from the build; set for UmbrellaRecommend only.
	Measurement *UmbrellaMeasurement `json:"measurement,omitempty"`
}

// UmbrellaRecommendation is one sub-package import that replaces part of an umbrella import,
// the groupings it covers and the capabilities the framework declares for it.
type UmbrellaRecommendation struct {
	Import       string          `json:"import"`
	Groupings    []string        `json:"groupings"`
	Capabilities []CapabilityKey `json:"capabilities,omitempty"`
}

// umbrellaUse is what one Go project's sources reference of one umbrella candidate.
type umbrellaUse struct {
	// files are the project's sources importing the umbrella, slash-separated and relative
	// to the project.
	files []string
	// refs maps every package of the project that imports the umbrella (its import path) to
	// the identifiers its files select from it; "_" and "." stand for a blank and a dot import.
	refs map[string]map[string]struct{}
}

// inspectUmbrellaImports reports the imports of the selected go framework's umbrella packages
// in every Go project of repo. A project whose scan failed (failed) is skipped: the report
// already lists it. A framework that is not configured, or not a go framework, has none.
func inspectUmbrellaImports(ctx context.Context, repo *fleetRepo, failed []SubprojectFailure, framework *FrameworkIndex) ([]UmbrellaFinding, error) {
	if repo == nil || framework == nil || framework.Name == "" || !isGoEcosystem(framework.Ecosystem) {
		return nil, nil
	}
	skipped := make(map[string]bool, len(failed))
	for _, failure := range failed {
		skipped[failure.Dir] = true
	}
	candidates := umbrellaCandidates(framework)
	var findings []UmbrellaFinding
	for _, dir := range repo.subprojects {
		project := relativeTo(repo.root, []string{dir})[0]
		if skipped[project] || !util.FileExists(filepath.Join(dir, "go.mod")) {
			continue
		}
		found, err := inspectProjectUmbrellas(ctx, dir, project, framework, candidates)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}
	return findings, nil
}

func isGoEcosystem(ecosystem string) bool {
	return ecosystem == "" || ecosystem == contractEcosystemGo
}

// umbrellaCandidates maps every import that may be an umbrella of framework to its
// description: each umbrella the contract describes, and the framework's root package,
// undescribed (nil) unless the contract describes it or declares it an ordinary package.
func umbrellaCandidates(framework *FrameworkIndex) map[string]*FrameworkUmbrella {
	candidates := make(map[string]*FrameworkUmbrella, len(framework.Umbrellas)+1)
	for importPath := range framework.Umbrellas {
		described := framework.Umbrellas[importPath]
		candidates[importPath] = &described
	}
	_, described := candidates[framework.Name]
	if _, declared := framework.Packages[framework.Name]; !described && !declared {
		candidates[framework.Name] = nil
	}
	return candidates
}

// inspectProjectUmbrellas judges every umbrella candidate the Go project at dir imports. The
// framework's own modules import it natively and are never judged.
func inspectProjectUmbrellas(ctx context.Context, dir, project string, framework *FrameworkIndex,
	candidates map[string]*FrameworkUmbrella) ([]UmbrellaFinding, error) {
	module, err := parseGoMod(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod of %s: %w", project, err)
	}
	if module.modulePath == "" || isFrameworkModule(framework, module.modulePath) {
		return nil, nil
	}
	uses, err := scanUmbrellaUses(ctx, dir, module, candidates)
	if err != nil {
		return nil, err
	}
	findings := make([]UmbrellaFinding, 0, len(uses))
	for _, umbrellaPath := range slices.Sorted(maps.Keys(uses)) {
		use, umbrella := uses[umbrellaPath], candidates[umbrellaPath]
		finding := judgeUmbrella(framework, umbrella, umbrellaPath, use)
		finding.Project = project
		for _, file := range use.files {
			finding.Files = append(finding.Files, path.Join(project, file))
		}
		if finding.Status == UmbrellaRecommend {
			measurement, err := measureUmbrellaSwitch(ctx, dir, umbrellaPath, finding.Unmapped, umbrellaReplacements(umbrella, use))
			if err != nil {
				return nil, err
			}
			finding.Measurement = &measurement
		}
		findings = append(findings, finding)
	}
	return findings, nil
}

// umbrellaScan collects, over one bounded walk of a project's sources (importScan), what
// each file references of the umbrella candidates it imports.
type umbrellaScan struct {
	root, modulePath string
	fset             *token.FileSet
	candidates       map[string]*FrameworkUmbrella
	uses             map[string]*umbrellaUse
}

func scanUmbrellaUses(ctx context.Context, dir string, module *goModFile, candidates map[string]*FrameworkUmbrella) (map[string]*umbrellaUse, error) {
	walk := newImportScan(ctx, dir, module.modulePath, module.ignore, maxImportScanEntries)
	scan := &umbrellaScan{root: walk.root, modulePath: module.modulePath, fset: walk.fset, candidates: candidates,
		uses: make(map[string]*umbrellaUse)}
	walk.onFile = scan.visitFile
	if err := walk.walk(); err != nil {
		return nil, err
	}
	return scan.uses, nil
}

// visitFile reads the imports of one source file and, when it imports an umbrella candidate,
// the identifiers it selects from it. A file that does not parse contributes nothing, as in
// the import scan (collectFileImports).
func (s *umbrellaScan) visitFile(file string) {
	header, err := parser.ParseFile(s.fset, file, nil, parser.ImportsOnly)
	if err != nil {
		return
	}
	pkg := s.packagePath(file)
	names := make(map[string]string)
	for _, spec := range util.GoImportSpecs(header) {
		umbrella, candidate := s.candidates[spec.Path]
		if !candidate {
			continue
		}
		use := s.use(spec.Path, file, pkg)
		switch name := umbrellaBinding(spec, umbrella); name {
		case "_", ".":
			use.reference(pkg, name)
		default:
			names[name] = spec.Path
		}
	}
	if len(names) > 0 {
		s.collectSelectors(file, pkg, names)
	}
}

// umbrellaBinding is the name an import of an umbrella binds: the rename it is written with,
// else the package name the contract declares, else the name its path implies
// (hiss.DefaultImportName).
func umbrellaBinding(spec util.GoImportSpec, umbrella *FrameworkUmbrella) string {
	switch {
	case spec.Name != "":
		return spec.Name
	case umbrella != nil && umbrella.Name != "":
		return umbrella.Name
	default:
		return hiss.DefaultImportName(spec.Path)
	}
}

// collectSelectors records every name.Identifier selector of the file whose name an umbrella
// import binds. A local declaration shadowing the name is not resolved: without type
// information the selector counts as a reference.
func (s *umbrellaScan) collectSelectors(file, pkg string, names map[string]string) {
	parsed, err := parser.ParseFile(s.fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		return
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, isIdent := selector.X.(*ast.Ident); isIdent {
			if umbrellaPath, bound := names[ident.Name]; bound {
				s.uses[umbrellaPath].reference(pkg, selector.Sel.Name)
			}
		}
		return true
	})
}

// packagePath is the import path of the project package the file belongs to.
func (s *umbrellaScan) packagePath(file string) string {
	rel, err := filepath.Rel(s.root, filepath.Dir(file))
	if err != nil || rel == "." {
		return s.modulePath
	}
	return s.modulePath + "/" + filepath.ToSlash(rel)
}

// use returns the record of umbrellaPath, noting that file of package pkg imports it.
func (s *umbrellaScan) use(umbrellaPath, file, pkg string) *umbrellaUse {
	use := s.uses[umbrellaPath]
	if use == nil {
		use = &umbrellaUse{refs: make(map[string]map[string]struct{})}
		s.uses[umbrellaPath] = use
	}
	if rel, err := filepath.Rel(s.root, file); err == nil {
		use.files = appendUniqueStr(use.files, filepath.ToSlash(rel))
	}
	if use.refs[pkg] == nil {
		use.refs[pkg] = make(map[string]struct{})
	}
	return use
}

func (u *umbrellaUse) reference(pkg, ident string) {
	if u.refs[pkg] == nil {
		u.refs[pkg] = make(map[string]struct{})
	}
	u.refs[pkg][ident] = struct{}{}
}

// identifiers returns every identifier the project references from the umbrella, sorted.
func (u *umbrellaUse) identifiers() []string {
	union := make(map[string]struct{})
	for _, idents := range u.refs {
		maps.Copy(union, idents)
	}
	return slices.Sorted(maps.Keys(union))
}

// judgeUmbrella compares the groupings a project references with every grouping the
// umbrella describes. An undescribed umbrella is unknown.
func judgeUmbrella(framework *FrameworkIndex, umbrella *FrameworkUmbrella, umbrellaPath string, use *umbrellaUse) UmbrellaFinding {
	finding := UmbrellaFinding{Umbrella: umbrellaPath, Status: UmbrellaUnknown}
	if umbrella == nil {
		return finding
	}
	groupings := umbrellaGroupingPackages(umbrella)
	for _, ident := range use.identifiers() {
		if _, grouping := groupings[ident]; grouping {
			finding.Groupings = append(finding.Groupings, ident)
			continue
		}
		finding.Unmapped = append(finding.Unmapped, unmappedReference(ident))
	}
	finding.TotalGroupings = len(umbrella.Groupings)
	switch {
	case len(finding.Groupings) == finding.TotalGroupings:
		finding.Status = UmbrellaJustified
	case len(finding.Groupings) == 0:
		finding.Status = UmbrellaUnmapped
	default:
		finding.Status = UmbrellaRecommend
		finding.Recommendations = recommendUmbrellaImports(framework, umbrella, finding.Groupings)
	}
	return finding
}

func unmappedReference(ident string) string {
	switch ident {
	case "_":
		return "a blank import"
	case ".":
		return "a dot import"
	default:
		return ident
	}
}

// umbrellaGroupingPackages maps each grouping name of the umbrella to its packages.
func umbrellaGroupingPackages(umbrella *FrameworkUmbrella) map[string][]string {
	groupings := make(map[string][]string, len(umbrella.Groupings))
	for _, grouping := range umbrella.Groupings {
		groupings[grouping.Name] = grouping.Packages
	}
	return groupings
}

// recommendUmbrellaImports lists, in import order, every package the referenced groupings
// group, with the groupings it covers and the capabilities the framework declares for it.
func recommendUmbrellaImports(framework *FrameworkIndex, umbrella *FrameworkUmbrella, referenced []string) []UmbrellaRecommendation {
	byImport := make(map[string]*UmbrellaRecommendation)
	for _, grouping := range umbrella.Groupings {
		if !slices.Contains(referenced, grouping.Name) {
			continue
		}
		for _, pkg := range grouping.Packages {
			recommendation := byImport[pkg]
			if recommendation == nil {
				recommendation = &UmbrellaRecommendation{Import: pkg, Capabilities: slices.Clone(framework.Packages[pkg].Capabilities)}
				byImport[pkg] = recommendation
			}
			recommendation.Groupings = appendUniqueStr(recommendation.Groupings, grouping.Name)
		}
	}
	out := make([]UmbrellaRecommendation, 0, len(byImport))
	for _, pkg := range slices.Sorted(maps.Keys(byImport)) {
		out = append(out, *byImport[pkg])
	}
	return out
}

// umbrellaReplacements maps every project package importing the umbrella onto the packages
// that replace its import: those of the groupings its own files reference.
func umbrellaReplacements(umbrella *FrameworkUmbrella, use *umbrellaUse) map[string][]string {
	groupings := umbrellaGroupingPackages(umbrella)
	out := make(map[string][]string, len(use.refs))
	for pkg, idents := range use.refs {
		replacement := make([]string, 0)
		for _, ident := range slices.Sorted(maps.Keys(idents)) {
			for _, sub := range groupings[ident] {
				replacement = appendUniqueStr(replacement, sub)
			}
		}
		out[pkg] = replacement
	}
	return out
}
