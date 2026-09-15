package bump

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

// errNoJSONObject is returned when a package manager's output carries no JSON object.
var errNoJSONObject = errors.New("bump: no JSON object in command output")

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

// DiscoverNodePackages finds all directories containing package.json: the root
// plus every workspace member.
//
// This used to return the workspace members *instead of* the root, so a pnpm
// monorepo's root toolchain — turbo, prettier, eslint, lefthook, commitlint —
// never reported drift (issue #97). Resolution now lives in
// internal/nodemanifest, shared with internal/docdistill, which had the
// mirror-image bug of reading only the root.
func DiscoverNodePackages(repoPath string) []string {
	dirs, err := nodemanifest.DiscoverPackageDirs(repoPath)
	if err != nil {
		return nil
	}
	return dirs
}

// readConfined reads rel below root after confining it with util.ConfinePath, so
// a caller-supplied repository path can never read outside root.
func readConfined(root, rel string) ([]byte, error) {
	path, err := util.ConfinePath(root, rel)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path is confined to root by util.ConfinePath above.
	return os.ReadFile(path)
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
			candidates, fbErr = scanPackageJSONStatic(repoPath, dirRel, opts)
			if fbErr != nil {
				return nil, fmt.Errorf("scan Node package %s: %w", dirRel, errors.Join(err, fbErr))
			}
		}
		allCandidates = append(allCandidates, candidates...)
	}

	return allCandidates, nil
}

// extractJSONObject returns the outermost JSON object embedded in combined command
// output. Package managers interleave warnings on stderr with their JSON report, so the
// report is located by its first '{' and last '}' rather than parsed verbatim.
func extractJSONObject(out string) ([]byte, error) {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return nil, errNoJSONObject
	}
	return []byte(out[start : end+1]), nil
}

func scanNodePackageDir(ctx context.Context, dir, dirRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	// pnpm exits non-zero when outdated packages exist, so the exit status alone is
	// not an error: only an empty report is.
	out, err := util.RunCommand(ctx, dir, "pnpm", "outdated", "--json")
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("pnpm outdated in %s: %w", dirRel, err)
	}

	raw, err := extractJSONObject(out)
	if err != nil {
		return nil, err
	}

	var outdated map[string]pnpmOutdatedItem
	if err := json.Unmarshal(raw, &outdated); err != nil {
		return nil, fmt.Errorf("parse pnpm outdated report for %s: %w", dirRel, err)
	}

	var candidates []UpgradeCandidate
	for pkg, item := range outdated {
		cand, ok := nodeUpgradeCandidate(pkg, item, dirRel, opts)
		if !ok {
			continue
		}
		candidates = append(candidates, cand)
		if opts.MaxCandidates > 0 && len(candidates) >= opts.MaxCandidates {
			break
		}
	}

	return candidates, nil
}

// nodeUpgradeCandidate converts one pnpm outdated entry into an upgrade candidate,
// reporting false when the package is current or filtered out by the channel policy.
func nodeUpgradeCandidate(pkg string, item pnpmOutdatedItem, dirRel string, opts ScanOptions) (UpgradeCandidate, bool) {
	if item.Latest == "" || item.Current == item.Latest {
		return UpgradeCandidate{}, false
	}
	ch := ClassifyChannel(item.Latest)
	if !opts.IncludePrerelease && ch != ChannelStable {
		return UpgradeCandidate{}, false
	}
	return UpgradeCandidate{
		Package:        pkg,
		CurrentVersion: item.Current,
		TargetVersion:  item.Latest,
		Channel:        ch,
		ManifestType:   "package.json",
		ModuleDir:      dirRel,
	}, true
}

func scanPackageJSONStatic(repoPath, dirRel string, opts ScanOptions) ([]UpgradeCandidate, error) {
	data, err := readConfined(repoPath, filepath.Join(dirRel, "package.json"))
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
