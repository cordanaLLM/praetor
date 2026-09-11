package bump

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

type goModuleJSON struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Main    bool          `json:"Main"`
	Update  *goModuleJSON `json:"Update,omitempty"`
}

// DiscoverGoModules finds all directories containing a go.mod file.
func DiscoverGoModules(repoPath string) []string {
	var modules []string

	// 1. Check if go.work exists
	goWorkPath := filepath.Join(repoPath, "go.work")
	if util.FileExists(goWorkPath) {
		workDirs := parseGoWork(goWorkPath)
		for _, dir := range workDirs {
			fullDir := filepath.Join(repoPath, dir)
			if util.FileExists(filepath.Join(fullDir, "go.mod")) {
				modules = append(modules, dir)
			}
		}
		if len(modules) > 0 {
			return modules
		}
	}

	// 2. Check root go.mod
	if util.FileExists(filepath.Join(repoPath, "go.mod")) {
		modules = append(modules, ".")
		return modules
	}

	// 3. Walk subdirectories up to depth 3 looking for go.mod
	if walkErr := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(repoPath, path)
		if err != nil || rel == "." {
			return nil
		}
		if strings.Count(rel, string(filepath.Separator)) > 2 {
			return filepath.SkipDir
		}
		if strings.HasPrefix(rel, ".") || strings.HasPrefix(rel, "vendor") || strings.HasPrefix(rel, "node_modules") {
			return filepath.SkipDir
		}
		if util.FileExists(filepath.Join(path, "go.mod")) {
			modules = append(modules, rel)
		}
		return nil
	}); walkErr != nil {
		return modules
	}

	return modules
}

func parseGoWork(path string) []string {
	var dirs []string
	data, err := os.ReadFile(path)
	if err != nil {
		return dirs
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
	return dirs
}

// ScanGoDependencies inspects Go modules in repoPath for available upgrades.
func ScanGoDependencies(ctx context.Context, repoPath string, opts ScanOptions) ([]UpgradeCandidate, error) {
	modules := DiscoverGoModules(repoPath)
	if len(modules) == 0 {
		return nil, nil
	}

	var allCandidates []UpgradeCandidate
	for _, modRel := range modules {
		modDir := filepath.Join(repoPath, modRel)
		candidates, err := scanGoModuleDir(ctx, modDir, modRel, opts)
		if err != nil || len(candidates) == 0 {
			fallback, fbErr := scanGoModFallback(modDir, modRel, opts)
			if fbErr == nil && len(fallback) > 0 {
				candidates = fallback
			}
		}
		allCandidates = append(allCandidates, candidates...)
	}

	return allCandidates, nil
}

func scanGoModuleDir(ctx context.Context, modDir, modRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-u", "-json", "all")
	cmd.Dir = modDir
	cmd.Env = append(os.Environ(), "GOPROXY=https://proxy.golang.org,direct")

	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, err
	}

	var candidates []UpgradeCandidate
	decoder := json.NewDecoder(strings.NewReader(string(out)))

	for i := 0; i < 10000; i++ {
		var mod goModuleJSON
		if decErr := decoder.Decode(&mod); decErr != nil {
			break
		}
		if mod.Main {
			continue
		}

		targetVer := mod.Version
		if mod.Update != nil {
			targetVer = mod.Update.Version
		}

		ch := ClassifyChannel(targetVer)
		if !opts.IncludePrerelease && ch != ChannelStable {
			continue
		}

		if mod.Update != nil || ch != ChannelStable {
			candidates = append(candidates, UpgradeCandidate{
				Package:        mod.Path,
				CurrentVersion: mod.Version,
				TargetVersion:  targetVer,
				Channel:        ch,
				ManifestType:   "go.mod",
				ModuleDir:      modRel,
			})
		}

		if opts.MaxCandidates > 0 && len(candidates) >= opts.MaxCandidates {
			break
		}
	}

	return candidates, nil
}

var requireRegex = regexp.MustCompile(`^\s*([a-zA-Z0-9.\-_/]+)\s+v([0-9a-zA-Z.\-_+]+)`)

func scanGoModFallback(modDir, modRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	goModFile := filepath.Join(modDir, "go.mod")
	file, err := os.Open(goModFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var candidates []UpgradeCandidate
	scanner := bufio.NewScanner(file)
	inRequire := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "require (") {
			inRequire = true
			continue
		}
		if inRequire && line == ")" {
			inRequire = false
			continue
		}

		if inRequire || strings.HasPrefix(line, "require ") {
			clean := strings.TrimPrefix(line, "require ")
			matches := requireRegex.FindStringSubmatch(clean)
			if len(matches) == 3 {
				pkg := matches[1]
				curVer := "v" + matches[2]
				ch := ClassifyChannel(curVer)
				if !opts.IncludePrerelease && ch != ChannelStable {
					continue
				}
				candidates = append(candidates, UpgradeCandidate{
					Package:        pkg,
					CurrentVersion: curVer,
					TargetVersion:  curVer, // Same version if offline
					Channel:        ch,
					ManifestType:   "go.mod",
					ModuleDir:      modRel,
				})
			}
		}
	}

	return candidates, nil
}
