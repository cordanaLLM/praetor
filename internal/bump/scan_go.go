package bump

import (
	"bufio"
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

// ScanGoDependencies inspects Go modules in repoPath for available upgrades.
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
	var allCandidates []UpgradeCandidate
	for _, modRel := range modules {
		modDir := filepath.Join(repoPath, modRel)
		candidates, scanErr := scanGoModuleDir(ctx, modDir, modRel, opts)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if scanErr != nil || len(candidates) == 0 {
			var fallbackErr error
			candidates, fallbackErr = scanGoModFallback(ctx, modDir, modRel, opts)
			if fallbackErr != nil {
				return nil, fmt.Errorf("scan module %s: %w", modRel, errors.Join(scanErr, fallbackErr))
			}
		}
		allCandidates = append(allCandidates, candidates...)
	}
	return allCandidates, nil
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
		if !ok {
			continue
		}
		candidates = append(candidates, candidate)
		if opts.MaxCandidates > 0 && len(candidates) >= opts.MaxCandidates {
			return candidates, nil
		}
	}
	return nil, fmt.Errorf("go module decoder exhausted bound")
}

func goUpgradeCandidate(mod goModuleJSON, modRel string, opts ScanOptions) (UpgradeCandidate, bool) {
	if mod.Main {
		return UpgradeCandidate{}, false
	}
	targetVer := mod.Version
	if mod.Update != nil {
		targetVer = mod.Update.Version
	}
	ch := ClassifyChannel(targetVer)
	if (!opts.IncludePrerelease && ch != ChannelStable) || (mod.Update == nil && ch == ChannelStable) {
		return UpgradeCandidate{}, false
	}
	return UpgradeCandidate{
		Package: mod.Path, CurrentVersion: mod.Version, TargetVersion: targetVer,
		Channel: ch, ManifestType: "go.mod", ModuleDir: modRel,
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
// fallback edit in applyGoUpdate matches on the literal "<package> <current version>", so
// a placeholder current version can never match and the fallback always fails.
func CurrentGoModVersion(ctx context.Context, repoPath, pkg string) (version, moduleDir string, err error) {
	modules := DiscoverGoModules(ctx, repoPath)
	if len(modules) == 0 {
		modules = []string{"."}
	}
	for i := 0; i < len(modules) && i < MaxDiscoveredModules; i++ {
		modDir := filepath.Join(repoPath, modules[i])
		found, scanErr := requiredVersionIn(filepath.Join(modDir, "go.mod"), pkg)
		if scanErr != nil {
			continue
		}
		if found != "" {
			return found, modules[i], nil
		}
	}
	return "", "", fmt.Errorf("%w: %s under %s", ErrPackageNotRequired, pkg, repoPath)
}

// closeManifest closes a manifest opened for reading. A close error on a read-only file
// carries no data loss, so it is inspected and deliberately dropped rather than assigned
// to the blank identifier.
func closeManifest(file *os.File) {
	if err := file.Close(); err != nil {
		return
	}
}

func requiredVersionIn(goModPath, pkg string) (string, error) {
	file, err := os.Open(goModPath) // #nosec G304 -- go.mod path assembled from a caller-supplied repo root and a discovered module dir.
	if err != nil {
		return "", fmt.Errorf("open %s: %w", goModPath, err)
	}
	defer closeManifest(file)

	scanner := bufio.NewScanner(file)
	for lines := 0; lines < MaxManifestLines && scanner.Scan(); lines++ {
		fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "require "))
		if len(fields) >= 2 && fields[0] == pkg && strings.HasPrefix(fields[1], "v") {
			return fields[1], nil
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return "", fmt.Errorf("scan %s: %w", goModPath, scanErr)
	}
	return "", nil
}

func scanGoModFallback(ctx context.Context, modDir, modRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
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
		candidate, ok := fallbackGoCandidate(line, modRel, opts)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan Go manifest: %w", err)
	}
	return candidates, nil
}

func fallbackGoCandidate(line, modRel string, opts ScanOptions) (UpgradeCandidate, bool) {
	matches := requireRegex.FindStringSubmatch(line)
	if len(matches) != 3 {
		return UpgradeCandidate{}, false
	}
	version := "v" + matches[2]
	channel := ClassifyChannel(version)
	if !opts.IncludePrerelease && channel != ChannelStable {
		return UpgradeCandidate{}, false
	}
	return UpgradeCandidate{
		Package: matches[1], CurrentVersion: version, TargetVersion: version,
		Channel: channel, ManifestType: "go.mod", ModuleDir: modRel,
	}, true
}
