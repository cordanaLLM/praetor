package bump

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

type goModuleJSON struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Main    bool          `json:"Main"`
	Update  *goModuleJSON `json:"Update,omitempty"`
}

// DiscoverGoModules finds directories containing go.mod, returning no partial result.
// Deprecated: use DiscoverGoModulesChecked to distinguish an empty tree from a read failure.
func DiscoverGoModules(ctx context.Context, repoPath string) []string {
	modules, err := DiscoverGoModulesChecked(ctx, repoPath)
	if err != nil {
		return nil
	}
	return modules
}

// DiscoverGoModulesChecked finds bounded modules and reports incomplete discovery.
func DiscoverGoModulesChecked(ctx context.Context, repoPath string) ([]string, error) {
	dirs, err := parseGoWork(ctx, filepath.Join(repoPath, "go.work"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	modules, err := existingGoModules(repoPath, dirs)
	if err != nil || len(modules) > 0 {
		return modules, err
	}
	rootModule, err := existingGoModules(repoPath, []string{"."})
	if err != nil || len(rootModule) > 0 {
		return rootModule, err
	}
	return walkGoModules(repoPath)
}

func existingGoModules(repoPath string, dirs []string) ([]string, error) {
	if len(dirs) > MaxDiscoveredModules {
		return nil, fmt.Errorf("module discovery exceeds %d directories", MaxDiscoveredModules)
	}
	var modules []string
	for _, dir := range dirs {
		path, err := util.ConfinePath(repoPath, filepath.Join(dir, "go.mod"))
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect module: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("module manifest must be regular: %s", path)
		}
		modules = append(modules, dir)
	}
	return modules, nil
}

type goModuleWalker struct {
	repoPath string
	modules  []string
	visited  int
}

func walkGoModules(repoPath string) ([]string, error) {
	walker := goModuleWalker{repoPath: repoPath}
	if err := filepath.WalkDir(repoPath, walker.visit); err != nil {
		return nil, fmt.Errorf("discover Go modules: %w", err)
	}
	return walker.modules, nil
}

func (walker *goModuleWalker) visit(path string, entry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	walker.visited++
	if walker.visited > 100000 {
		return fmt.Errorf("module discovery exceeds 100000 entries")
	}
	if !entry.IsDir() {
		return nil
	}
	rel, err := filepath.Rel(walker.repoPath, path)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	if skipModuleDirectory(rel, entry.Name()) {
		return filepath.SkipDir
	}
	found, err := existingGoModules(walker.repoPath, []string{rel})
	if err != nil {
		return err
	}
	walker.modules = append(walker.modules, found...)
	if len(walker.modules) > MaxDiscoveredModules {
		return fmt.Errorf("module discovery exceeds %d directories", MaxDiscoveredModules)
	}
	return nil
}

func skipModuleDirectory(rel, name string) bool {
	return strings.Count(rel, string(filepath.Separator)) > 2 || strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules"
}

func parseGoWork(ctx context.Context, path string) ([]string, error) {
	var dirs []string
	data, err := readManifest(ctx, filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return nil, err
	}

	inUseBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "use (") {
			inUseBlock = true
			continue
		}
		if inUseBlock && trimmed == ")" {
			inUseBlock = false
			continue
		}
		if inUseBlock && trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			clean := strings.Trim(trimmed, `"' `)
			dirs = append(dirs, filepath.Clean(clean))
		} else if strings.HasPrefix(trimmed, "use ") {
			clean := strings.TrimPrefix(trimmed, "use ")
			clean = strings.Trim(clean, `"' `)
			dirs = append(dirs, filepath.Clean(clean))
		}
	}
	return dirs, nil
}

// ScanGoDependencies returns the dependency inventory of every Go module in repoPath: one
// entry per requirement a go.mod declares, whether or not an upgrade exists for it. An
// entry whose TargetVersion differs from its CurrentVersion is an upgrade candidate; an
// up-to-date requirement carries TargetVersion == CurrentVersion.
func ScanGoDependencies(ctx context.Context, repoPath string, opts ScanOptions) ([]UpgradeCandidate, error) {
	if ctx == nil {
		return nil, fmt.Errorf("scan Go dependencies requires context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	modules, err := DiscoverGoModulesChecked(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	var inventory []UpgradeCandidate
	for _, modRel := range modules {
		found, err := scanGoModule(ctx, filepath.Join(repoPath, modRel), modRel, opts)
		if err != nil {
			return nil, fmt.Errorf("scan module %s: %w", modRel, err)
		}
		inventory = append(inventory, found...)
	}
	return inventory, nil
}

// scanGoModule returns one module's inventory. The requirements its go.mod declares are
// the inventory; `go list -m -u` only supplies their selected versions and upgrade
// targets, so the inventory never depends on network state.
func scanGoModule(ctx context.Context, modDir, modRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	return manifestInventory(ctx,
		func() ([]UpgradeCandidate, error) { return scanGoModStatic(ctx, modDir, modRel) },
		func() ([]UpgradeCandidate, error) { return scanGoModuleDir(ctx, modDir, modRel, opts) })
}

func scanGoModuleDir(ctx context.Context, modDir, modRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	out, err := util.RunCommandBytes(ctx, modDir, "go", 16<<20, "list", "-m", "-u", "-json", "all")
	if err != nil {
		return nil, fmt.Errorf("go list modules: %w", err)
	}
	return decodeGoModules(string(out.Stdout), modRel, opts)
}

func decodeGoModules(output, modRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	var candidates []UpgradeCandidate
	decoder := json.NewDecoder(strings.NewReader(output))
	for i := 0; i <= 10000; i++ {
		var mod goModuleJSON
		err := decoder.Decode(&mod)
		if errors.Is(err, io.EOF) {
			return candidates, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode Go module report: %w", err)
		}
		if i == 10000 {
			return nil, fmt.Errorf("go module report exceeds 10000 records")
		}
		candidate, ok := goUpgradeCandidate(mod, modRel, opts)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	return nil, fmt.Errorf("go module decoder exhausted bound")
}

// goUpgradeCandidate converts one `go list -m -u` record into an inventory entry. Every
// module except the main one is reported: an up-to-date module, or one whose only update
// the channel policy refuses, carries its current version as its target.
func goUpgradeCandidate(mod goModuleJSON, modRel string, opts ScanOptions) (UpgradeCandidate, bool) {
	if mod.Main {
		return UpgradeCandidate{}, false
	}
	targetVer := mod.Version
	if mod.Update != nil && upgradeAllowed(mod.Update.Version, opts) {
		targetVer = mod.Update.Version
	}
	return UpgradeCandidate{
		Package: mod.Path, CurrentVersion: mod.Version, TargetVersion: targetVer,
		Channel: ClassifyChannel(targetVer), ManifestType: "go.mod", ModuleDir: modRel,
	}, true
}

var requireRegex = regexp.MustCompile(`^\s*([a-zA-Z0-9.\-_/]+)\s+v([0-9a-zA-Z.\-_+]+)`)

// MaxManifestLines is the scalar upper bound (HISS-02) on the number of manifest lines a
// single scan reads. No real go.mod approaches it; a pathological or hostile file cannot
// make the scanner loop unbounded.
const MaxManifestLines = 100000

// MaxDiscoveredModules is the scalar upper bound (HISS-02) on the modules a single
// lookup walks.
const MaxDiscoveredModules = 1024

// ErrPackageNotRequired reports that a package is not required by any go.mod in the tree.
var ErrPackageNotRequired = errors.New("package not required by any go.mod")

// CurrentGoModVersion returns the version a go.mod under repoPath currently requires for
// pkg, together with the module directory (relative to repoPath) that requires it.
//
// Callers building an UpgradeCandidate must use it instead of a placeholder: the go.mod
// fallback edit in applyGoUpdate rewrites only the require line naming the package at the
// candidate's current version, and refuses an empty or placeholder current version.
//
// Every go.mod is read under readManifest's rule, so a manifest the writer would refuse is
// reported instead of scanned. A missing go.mod is skipped; a cancelled caller gets its
// context error back.
func CurrentGoModVersion(ctx context.Context, repoPath, pkg string) (version, moduleDir string, err error) {
	if ctx == nil {
		return "", "", fmt.Errorf("current go.mod version: context cannot be nil")
	}
	modules := DiscoverGoModules(ctx, repoPath)
	if len(modules) == 0 {
		modules = []string{"."}
	}
	var refused error
	for i := 0; i < len(modules) && i < MaxDiscoveredModules; i++ {
		found, readErr := requiredVersionIn(ctx, repoPath, modules[i], pkg)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", "", ctxErr
		}
		if found != "" {
			return found, modules[i], nil
		}
		if readErr != nil {
			refused = errors.Join(refused, readErr)
		}
	}
	if refused != nil {
		return "", "", fmt.Errorf("find %s under %s: %w", pkg, repoPath, refused)
	}
	return "", "", fmt.Errorf("%w: %s under %s", ErrPackageNotRequired, pkg, repoPath)
}

// requiredVersionIn returns the version the go.mod in moduleRel requires for pkg, or "" when
// there is no go.mod or its first MaxManifestLines lines do not require it. The read is
// readManifest's, confined to repoPath, so this lookup, the scanners and the writer share one
// manifest rule.
func requiredVersionIn(ctx context.Context, repoPath, moduleRel, pkg string) (string, error) {
	name := filepath.Join(moduleRel, "go.mod")
	data, err := readManifest(ctx, repoPath, name)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	inRequire := false
	for lines := 0; lines < MaxManifestLines && scanner.Scan(); lines++ {
		version, found := requiredModuleVersion(scanner.Text(), &inRequire, pkg)
		if found && strings.HasPrefix(version, "v") {
			return version, nil
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return "", fmt.Errorf("scan %s: %w", name, scanErr)
	}
	return "", nil
}

// requiredModuleVersion advances require-block state over one go.mod line and, when the
// line is a requirement naming pkg, returns the version token it carries. It is the one
// matcher the version lookup and the fallback edit share: require lines come from
// gomanifest.RequirementLine, so comment, exclude and replace lines never match.
func requiredModuleVersion(raw string, inRequire *bool, pkg string) (string, bool) {
	line, isRequirement := gomanifest.RequirementLine(raw, inRequire)
	fields := strings.Fields(line)
	if !isRequirement || len(fields) < 2 || fields[0] != pkg {
		return "", false
	}
	return fields[1], true
}

// scanGoModStatic returns the requirements modDir's go.mod declares, each with its
// declared version as both current and target: the module's inventory before any
// upstream report says which of them has an upgrade.
func scanGoModStatic(ctx context.Context, modDir, modRel string) ([]UpgradeCandidate, error) {
	data, err := readManifest(ctx, modDir, "go.mod")
	if err != nil {
		return nil, err
	}
	var candidates []UpgradeCandidate
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	inRequire := false
	for lines := 0; lines <= MaxManifestLines && scanner.Scan(); lines++ {
		if lines == MaxManifestLines {
			return nil, fmt.Errorf("manifest exceeds %d lines", MaxManifestLines)
		}
		line, isRequirement := gomanifest.RequirementLine(scanner.Text(), &inRequire)
		if !isRequirement {
			continue
		}
		candidate, ok := fallbackGoCandidate(line, modRel)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan Go manifest: %w", err)
	}
	return candidates, nil
}

func fallbackGoCandidate(line, modRel string) (UpgradeCandidate, bool) {
	matches := requireRegex.FindStringSubmatch(line)
	if len(matches) != 3 {
		return UpgradeCandidate{}, false
	}
	version := "v" + matches[2]
	return UpgradeCandidate{
		Package: matches[1], CurrentVersion: version, TargetVersion: version,
		Channel: ClassifyChannel(version), ManifestType: "go.mod", ModuleDir: modRel,
	}, true
}
