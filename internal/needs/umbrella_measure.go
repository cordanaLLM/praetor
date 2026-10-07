package needs

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// moduleGraphTimeout bounds the go list run of one measurement.
	moduleGraphTimeout = 2 * time.Minute
	// maxModuleGraphPackages bounds the packages one measurement reads from go list.
	maxModuleGraphPackages = 100000
	// maxMeasurementReasonBytes bounds the go list diagnostic a not-measured reason quotes.
	maxMeasurementReasonBytes = 300
	// moduleGraphTemplate prints, per package of the build, its import path, whether only a
	// dependency matched it (DepOnly), its module, whether that is the main module, and its
	// imports, tab-separated.
	moduleGraphTemplate = "{{.ImportPath}}\t{{.DepOnly}}\t{{with .Module}}{{.Path}}{{end}}\t" +
		"{{with .Module}}{{.Main}}{{end}}\t{{join .Imports \" \"}}"
)

// offlineGoListEnvironment keeps a measurement's go list off the network: no module proxy
// (GOPROXY=off), no toolchain download (GOTOOLCHAIN=local) and no workspace file above the
// project (GOWORK=off). A module the cache does not hold fails the run instead of being
// fetched.
var offlineGoListEnvironment = []string{"GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off"}

// UmbrellaMeasurement is what switching from an umbrella import to the recommended
// sub-package imports removes from a project's build, read from `go list -deps ./...` run
// offline. Modules count the dependency modules that provide a package of the build (the
// main module excluded); packages count every package of the build, the standard library
// included. Without the module graph, Measured is false and Reason says why: a count is never
// estimated.
type UmbrellaMeasurement struct {
	Measured       bool   `json:"measured"`
	Reason         string `json:"reason,omitempty"`
	ModulesBefore  int    `json:"modules_before,omitempty"`
	ModulesAfter   int    `json:"modules_after,omitempty"`
	PackagesBefore int    `json:"packages_before,omitempty"`
	PackagesAfter  int    `json:"packages_after,omitempty"`
}

func notMeasured(reason string) UmbrellaMeasurement {
	return UmbrellaMeasurement{Reason: reason}
}

// measureUmbrellaSwitch measures the switch for the project at dir: the build as go list
// reports it, against the same graph with every project package's umbrella import replaced
// by its replacements. An identifier no grouping describes keeps the umbrella import, so the
// switch is not measured. Only a cancelled caller context is an error.
func measureUmbrellaSwitch(ctx context.Context, dir, umbrella string, unmapped []string, replacements map[string][]string) (UmbrellaMeasurement, error) {
	if len(unmapped) > 0 {
		return notMeasured(fmt.Sprintf("the umbrella import stays for %s, which no grouping describes", strings.Join(unmapped, ", "))), nil
	}
	listing, err := listModuleGraph(ctx, dir)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return UmbrellaMeasurement{}, ctxErr
		}
		return notMeasured("module graph unavailable offline: " + measurementReason(err)), nil
	}
	graph, err := parseModuleGraph(listing, maxModuleGraphPackages)
	if err != nil {
		return notMeasured(err.Error()), nil
	}
	return graph.measureSwitch(umbrella, replacements), nil
}

// measurementReason renders a go list failure on one bounded line.
func measurementReason(err error) string {
	return util.TruncateExcerpt(strings.Join(strings.Fields(err.Error()), " "), maxMeasurementReasonBytes)
}

// listModuleGraph runs go list over the project at dir without network access. The -mod
// flag on the command line overrides a -mod=mod in GOFLAGS, so go.mod and go.sum are never
// edited and a missing module is never resolved.
func listModuleGraph(ctx context.Context, dir string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, moduleGraphTimeout)
	defer cancel()
	ctx, err := util.WithCommandEnvironment(ctx, util.InheritedEnvironment(offlineGoListEnvironment))
	if err != nil {
		return nil, err
	}
	result, err := util.RunCommandBytes(ctx, dir, "go", util.MaxCommandOutputBytes,
		"list", moduleGraphMode(dir), "-deps", "-f", moduleGraphTemplate, "./...")
	if err != nil {
		return nil, util.CommandDiagnostic(err, result.Stderr)
	}
	return result.Stdout, nil
}

// moduleGraphMode reads a vendoring project from its vendor directory and every other one
// read-only from the module cache.
func moduleGraphMode(dir string) string {
	if util.FileExists(filepath.Join(dir, "vendor", "modules.txt")) {
		return "-mod=vendor"
	}
	return "-mod=readonly"
}

// graphPackage is one package of a go list listing.
type graphPackage struct {
	module  string
	main    bool
	own     bool
	imports []string
}

// moduleGraph is a go list listing keyed by import path.
type moduleGraph map[string]graphPackage

// parseModuleGraph reads a listing printed with moduleGraphTemplate, refusing one of more
// than limit packages or a line of another shape.
func parseModuleGraph(listing []byte, limit int) (moduleGraph, error) {
	lines := strings.Split(strings.TrimRight(string(listing), "\r\n"), "\n")
	if len(lines) > limit {
		return nil, fmt.Errorf("go list listed more than %d packages", limit)
	}
	graph := make(moduleGraph, len(lines))
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || fields[0] == "" {
			return nil, fmt.Errorf("unexpected go list line %q", util.TruncateExcerpt(line, 120))
		}
		graph[fields[0]] = graphPackage{own: fields[1] == "false", module: fields[2], main: fields[3] == "true",
			imports: strings.Fields(fields[4])}
	}
	if len(graph) == 0 {
		return nil, errors.New("go list listed no package")
	}
	return graph, nil
}

// measureSwitch counts the build before and after the switch. Every replacement must be a
// package of the build already: the umbrella imports what it groups, so one that is missing
// means the contract and the listed framework disagree, and nothing is counted.
func (g moduleGraph) measureSwitch(umbrella string, replacements map[string][]string) UmbrellaMeasurement {
	if _, listed := g[umbrella]; !listed {
		return notMeasured(fmt.Sprintf("go list does not list the umbrella %s", umbrella))
	}
	for _, pkg := range slices.Sorted(maps.Keys(replacements)) {
		for _, sub := range replacements[pkg] {
			if _, listed := g[sub]; !listed {
				return notMeasured(fmt.Sprintf("go list does not list %s, which the umbrella groups", sub))
			}
		}
	}
	after, err := g.reachAfterSwitch(umbrella, replacements)
	if err != nil {
		return notMeasured(err.Error())
	}
	before := make(map[string]struct{}, len(g))
	for pkg := range g {
		before[pkg] = struct{}{}
	}
	return UmbrellaMeasurement{Measured: true,
		ModulesBefore: g.moduleCount(before), ModulesAfter: g.moduleCount(after),
		PackagesBefore: len(before), PackagesAfter: len(after)}
}

// reachAfterSwitch returns every package of the build once each project package's umbrella
// import is replaced. The go command adds dependencies no import names (the runtime and what
// it imports), so every listed package the project's imports do not reach, and the runtime
// itself, stays a root: the switch cannot remove them.
func (g moduleGraph) reachAfterSwitch(umbrella string, replacements map[string][]string) (map[string]struct{}, error) {
	sorted := slices.Sorted(maps.Keys(g))
	own := make([]string, 0, len(g))
	for _, pkg := range sorted {
		if g[pkg].own {
			own = append(own, pkg)
		}
	}
	explicit, err := g.reach(own, func(pkg string) ([]string, error) { return g[pkg].imports, nil })
	if err != nil {
		return nil, err
	}
	roots := own
	for _, pkg := range sorted {
		if _, imported := explicit[pkg]; !imported || pkg == "runtime" {
			roots = append(roots, pkg)
		}
	}
	return g.reach(roots, func(pkg string) ([]string, error) { return g.switchedImports(pkg, umbrella, replacements) })
}

// reach returns every listed package reachable from roots through next. The walk visits each
// listed package at most once.
func (g moduleGraph) reach(roots []string, next func(pkg string) ([]string, error)) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(g))
	queue := make([]string, 0, len(g))
	enqueue := func(pkg string) {
		_, listed := g[pkg]
		if _, done := seen[pkg]; listed && !done {
			seen[pkg] = struct{}{}
			queue = append(queue, pkg)
		}
	}
	for _, root := range roots {
		enqueue(root)
	}
	for i := 0; i < len(queue) && i < len(g); i++ {
		imports, err := next(queue[i])
		if err != nil {
			return nil, err
		}
		for _, imported := range imports {
			enqueue(imported)
		}
	}
	return seen, nil
}

// switchedImports returns a package's imports after the switch. A project package importing
// the umbrella in a file the source scan did not read (a directory the scan skips, such as a
// root-level cache/) cannot be switched, so nothing is counted.
func (g moduleGraph) switchedImports(pkg, umbrella string, replacements map[string][]string) ([]string, error) {
	entry := g[pkg]
	if !entry.own || !slices.Contains(entry.imports, umbrella) {
		return entry.imports, nil
	}
	replacement, scanned := replacements[pkg]
	if !scanned {
		return nil, fmt.Errorf("package %s imports the umbrella from a source the scan did not read", pkg)
	}
	switched := make([]string, 0, len(entry.imports)+len(replacement))
	for _, imported := range entry.imports {
		if imported != umbrella {
			switched = append(switched, imported)
		}
	}
	return append(switched, replacement...), nil
}

// moduleCount counts the dependency modules providing the packages: the main module and the
// standard library (no module) are not counted.
func (g moduleGraph) moduleCount(packages map[string]struct{}) int {
	modules := make(map[string]struct{})
	for pkg := range packages {
		if entry := g[pkg]; entry.module != "" && !entry.main {
			modules[entry.module] = struct{}{}
		}
	}
	return len(modules)
}
