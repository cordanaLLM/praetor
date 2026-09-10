package bump

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/standards/internal/util"
	"gopkg.in/yaml.v3"
)

type pnpmOutdatedItem struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Wanted  string `json:"wanted"`
}

type packageJSONFormat struct {
	Name            string            `json:"name"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

type pnpmWorkspaceConfig struct {
	Packages []string `yaml:"packages"`
}

// DiscoverNodePackages finds all directories containing package.json.
func DiscoverNodePackages(repoPath string) []string {
	var pkgDirs []string

	// 1. Check pnpm-workspace.yaml
	wsPath := filepath.Join(repoPath, "pnpm-workspace.yaml")
	if util.FileExists(wsPath) {
		wsDirs := parsePnpmWorkspace(repoPath, wsPath)
		if len(wsDirs) > 0 {
			return wsDirs
		}
	}

	// 2. Check root package.json
	if util.FileExists(filepath.Join(repoPath, "package.json")) {
		pkgDirs = append(pkgDirs, ".")
		return pkgDirs
	}

	return pkgDirs
}

func parsePnpmWorkspace(repoPath, wsPath string) []string {
	var dirs []string
	data, err := os.ReadFile(wsPath)
	if err != nil {
		return dirs
	}

	var cfg pnpmWorkspaceConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return dirs
	}

	for _, pattern := range cfg.Packages {
		cleanPattern := strings.TrimPrefix(pattern, "./")
		matches, err := filepath.Glob(filepath.Join(repoPath, cleanPattern))
		if err == nil {
			for _, m := range matches {
				if util.FileExists(filepath.Join(m, "package.json")) {
					rel, relErr := filepath.Rel(repoPath, m)
					if relErr == nil {
						dirs = append(dirs, rel)
					}
				}
			}
		}
	}

	return dirs
}

// ScanNodeDependencies inspects Node/pnpm packages in repoPath for upgrades.
func ScanNodeDependencies(ctx context.Context, repoPath string, opts ScanOptions) ([]UpgradeCandidate, error) {
	pkgDirs := DiscoverNodePackages(repoPath)
	if len(pkgDirs) == 0 {
		return nil, nil
	}

	var allCandidates []UpgradeCandidate
	for _, dirRel := range pkgDirs {
		dir := filepath.Join(repoPath, dirRel)
		candidates, err := scanNodePackageDir(ctx, dir, dirRel, opts)
		if err != nil {
			var fbErr error
			candidates, fbErr = scanPackageJSONStatic(dir, dirRel, opts)
			if fbErr != nil {
				continue
			}
		}
		allCandidates = append(allCandidates, candidates...)
	}

	return allCandidates, nil
}

func scanNodePackageDir(ctx context.Context, dir, dirRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	cmd := exec.CommandContext(ctx, "pnpm", "outdated", "--json")
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, err
	}

	var outdated map[string]pnpmOutdatedItem
	if err := json.Unmarshal(out, &outdated); err != nil {
		return nil, err
	}

	var candidates []UpgradeCandidate
	for pkg, item := range outdated {
		if item.Latest == "" || item.Current == item.Latest {
			continue
		}

		ch := ClassifyChannel(item.Latest)
		if !opts.IncludePrerelease && ch != ChannelStable {
			continue
		}

		candidates = append(candidates, UpgradeCandidate{
			Package:        pkg,
			CurrentVersion: item.Current,
			TargetVersion:  item.Latest,
			Channel:        ch,
			ManifestType:   "package.json",
			ModuleDir:      dirRel,
		})

		if opts.MaxCandidates > 0 && len(candidates) >= opts.MaxCandidates {
			break
		}
	}

	return candidates, nil
}

func scanPackageJSONStatic(dir, dirRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	pkgFile := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(pkgFile)
	if err != nil {
		return nil, err
	}

	var pj packageJSONFormat
	if err := json.Unmarshal(data, &pj); err != nil {
		return nil, err
	}

	var candidates []UpgradeCandidate
	extractFromMap := func(deps map[string]string) {
		for pkg, ver := range deps {
			cleanVer := strings.TrimPrefix(ver, "^")
			cleanVer = strings.TrimPrefix(cleanVer, "~")
			ch := ClassifyChannel(cleanVer)
			if !opts.IncludePrerelease && ch != ChannelStable {
				continue
			}
			candidates = append(candidates, UpgradeCandidate{
				Package:        pkg,
				CurrentVersion: cleanVer,
				TargetVersion:  cleanVer,
				Channel:        ch,
				ManifestType:   "package.json",
				ModuleDir:      dirRel,
			})
		}
	}

	extractFromMap(pj.Dependencies)
	extractFromMap(pj.DevDependencies)
	return candidates, nil
}
